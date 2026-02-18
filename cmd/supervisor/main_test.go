package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

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
