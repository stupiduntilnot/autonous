package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stupiduntilnot/autonous/internal/config"
	"github.com/stupiduntilnot/autonous/internal/db"
)

func testSupervisorDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.OpenDB(filepath.Join(t.TempDir(), "sup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestAtomicSwitchSymlink(t *testing.T) {
	base := t.TempDir()
	oldTarget := filepath.Join(base, "old.bin")
	newTarget := filepath.Join(base, "new.bin")
	active := filepath.Join(base, "worker.current")
	if err := os.WriteFile(oldTarget, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newTarget, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldTarget, active); err != nil {
		t.Fatal(err)
	}

	if err := atomicSwitchSymlink(active, newTarget); err != nil {
		t.Fatalf("atomicSwitchSymlink failed: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(active)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(newTarget)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("expected %s, got %s", newTarget, resolved)
	}
}

func TestDeployApprovedArtifact(t *testing.T) {
	database := testSupervisorDB(t)
	base := t.TempDir()
	workerBin := filepath.Join(base, "worker.current")
	candidate := filepath.Join(base, "candidate-worker")
	content := []byte("worker-binary")
	if err := os.WriteFile(candidate, content, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	sumHex := hex.EncodeToString(sum[:])

	if err := db.InsertArtifact(database, "tx-1", "", candidate, db.ArtifactStatusApproved); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE artifacts SET sha256 = ? WHERE tx_id = ?`, sumHex, "tx-1"); err != nil {
		t.Fatal(err)
	}
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.SupervisorConfig{WorkerBin: workerBin}

	if err := deployApprovedArtifact(cfg, database, supEventID); err != nil {
		t.Fatalf("deployApprovedArtifact failed: %v", err)
	}

	resolved, err := filepath.EvalSymlinks(workerBin)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("expected active symlink to %s, got %s", candidate, resolved)
	}
	artifact, err := db.GetArtifactByTxID(database, "tx-1")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != db.ArtifactStatusDeployedUnstable {
		t.Fatalf("unexpected status: %s", artifact.Status)
	}
}

func TestPromoteLatestDeployedArtifact(t *testing.T) {
	database := testSupervisorDB(t)
	if err := db.InsertArtifact(database, "tx-u1", "", "/state/artifacts/tx-u1/worker", db.ArtifactStatusDeployedUnstable); err != nil {
		t.Fatal(err)
	}
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := promoteLatestDeployedArtifact(database, supEventID); err != nil {
		t.Fatalf("promoteLatestDeployedArtifact failed: %v", err)
	}
	artifact, err := db.GetArtifactByTxID(database, "tx-u1")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != db.ArtifactStatusPromoted {
		t.Fatalf("unexpected status: %s", artifact.Status)
	}
}

func TestAttemptArtifactRollback(t *testing.T) {
	database := testSupervisorDB(t)
	baseDir := t.TempDir()
	active := filepath.Join(baseDir, "worker.current")
	baseBin := filepath.Join(baseDir, "base-worker")
	newBin := filepath.Join(baseDir, "new-worker")
	if err := os.WriteFile(baseBin, []byte("base"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newBin, active); err != nil {
		t.Fatal(err)
	}

	if err := db.InsertArtifact(database, "tx-base", "", baseBin, db.ArtifactStatusPromoted); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertArtifact(database, "tx-new", "tx-base", newBin, db.ArtifactStatusDeployedUnstable); err != nil {
		t.Fatal(err)
	}
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.SupervisorConfig{WorkerBin: active}

	ok, err := attemptArtifactRollback(cfg, database, supEventID)
	if err != nil {
		t.Fatalf("attemptArtifactRollback failed: %v", err)
	}
	if !ok {
		t.Fatal("expected rollback success")
	}
	resolved, err := filepath.EvalSymlinks(active)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(baseBin)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("expected active to point to base binary, got %s", resolved)
	}
	artifact, err := db.GetArtifactByTxID(database, "tx-new")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != db.ArtifactStatusRolledBack {
		t.Fatalf("unexpected status: %s", artifact.Status)
	}
}

func TestProcessPendingRollback(t *testing.T) {
	database := testSupervisorDB(t)
	baseDir := t.TempDir()
	active := filepath.Join(baseDir, "worker.current")
	baseBin := filepath.Join(baseDir, "base-worker")
	newBin := filepath.Join(baseDir, "new-worker")
	if err := os.WriteFile(baseBin, []byte("base"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newBin, active); err != nil {
		t.Fatal(err)
	}

	if err := db.InsertArtifact(database, "tx-base", "", baseBin, db.ArtifactStatusPromoted); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertArtifact(database, "tx-pending", "tx-base", newBin, db.ArtifactStatusRollbackPending); err != nil {
		t.Fatal(err)
	}
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.SupervisorConfig{WorkerBin: active}

	ok, err := processPendingRollback(cfg, database, supEventID)
	if err != nil {
		t.Fatalf("processPendingRollback failed: %v", err)
	}
	if !ok {
		t.Fatal("expected rollback success")
	}
	resolved, err := filepath.EvalSymlinks(active)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(baseBin)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("expected active to point to base binary, got %s", resolved)
	}
	artifact, err := db.GetArtifactByTxID(database, "tx-pending")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != db.ArtifactStatusRolledBack {
		t.Fatalf("unexpected status: %s", artifact.Status)
	}
}

func TestEnsureBootstrapArtifactRecord(t *testing.T) {
	database := testSupervisorDB(t)
	base := t.TempDir()
	worker := filepath.Join(base, "worker")
	if err := os.WriteFile(worker, []byte("bootstrap"), 0o755); err != nil {
		t.Fatal(err)
	}
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.SupervisorConfig{WorkerBin: worker}
	if err := ensureBootstrapArtifactRecord(cfg, database, supEventID); err != nil {
		t.Fatalf("ensureBootstrapArtifactRecord failed: %v", err)
	}
	a, err := db.GetArtifactByTxID(database, "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != db.ArtifactStatusPromoted {
		t.Fatalf("unexpected status: %s", a.Status)
	}
}

func TestStartAutoPromoteWatcher(t *testing.T) {
	database := testSupervisorDB(t)
	if err := db.InsertArtifact(database, "tx-auto-promote", "", "/state/artifacts/tx-auto-promote/worker", db.ArtifactStatusDeployedUnstable); err != nil {
		t.Fatal(err)
	}
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{"role": "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	var exited atomic.Bool
	startAutoPromoteWatcher(database, supEventID, time.Now(), 50*time.Millisecond, &exited)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		artifact, err := db.GetArtifactByTxID(database, "tx-auto-promote")
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Status == db.ArtifactStatusPromoted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected auto promote to move status to promoted")
}
