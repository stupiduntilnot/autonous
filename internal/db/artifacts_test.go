package db

import (
	"errors"
	"testing"
)

func TestInsertAndGetArtifactByTxID(t *testing.T) {
	database := testDB(t)

	err := InsertArtifact(database, "tx-1", "base-0", "/state/artifacts/tx-1/worker", ArtifactStatusCreated)
	if err != nil {
		t.Fatalf("InsertArtifact failed: %v", err)
	}

	got, err := GetArtifactByTxID(database, "tx-1")
	if err != nil {
		t.Fatalf("GetArtifactByTxID failed: %v", err)
	}
	if got.TxID != "tx-1" {
		t.Fatalf("unexpected tx_id: %s", got.TxID)
	}
	if got.Status != ArtifactStatusCreated {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	if !got.BaseTxID.Valid || got.BaseTxID.String != "base-0" {
		t.Fatalf("unexpected base_tx_id: %+v", got.BaseTxID)
	}
}

func TestGetArtifactByTxID_NotFound(t *testing.T) {
	database := testDB(t)

	_, err := GetArtifactByTxID(database, "missing")
	if !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("expected ErrArtifactNotFound, got: %v", err)
	}
}

func TestTransitionArtifactStatus_Valid(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-2", "", "/state/artifacts/tx-2/worker", ArtifactStatusCreated); err != nil {
		t.Fatal(err)
	}

	ok, err := TransitionArtifactStatus(database, "tx-2", ArtifactStatusCreated, ArtifactStatusBuilding, "")
	if err != nil {
		t.Fatalf("TransitionArtifactStatus failed: %v", err)
	}
	if !ok {
		t.Fatal("expected transition success")
	}
	got, err := GetArtifactByTxID(database, "tx-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ArtifactStatusBuilding {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}

func TestTransitionArtifactStatus_InvalidTransition(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-3", "", "/state/artifacts/tx-3/worker", ArtifactStatusCreated); err != nil {
		t.Fatal(err)
	}

	_, err := TransitionArtifactStatus(database, "tx-3", ArtifactStatusCreated, ArtifactStatusApproved, "")
	if !errors.Is(err, ErrInvalidStatusTransit) {
		t.Fatalf("expected ErrInvalidStatusTransit, got: %v", err)
	}
}

func TestTransitionArtifactStatus_StatusMismatch(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-4", "", "/state/artifacts/tx-4/worker", ArtifactStatusCreated); err != nil {
		t.Fatal(err)
	}

	ok, err := TransitionArtifactStatus(database, "tx-4", ArtifactStatusBuilding, ArtifactStatusTesting, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected transition failure on status mismatch")
	}
}

func TestCleanupInProgressArtifacts(t *testing.T) {
	database := testDB(t)

	cases := []struct {
		txID   string
		status string
	}{
		{"tx-a", ArtifactStatusBuilding},
		{"tx-b", ArtifactStatusTesting},
		{"tx-c", ArtifactStatusSelfChecking},
		{"tx-d", ArtifactStatusDeploying},
		{"tx-e", ArtifactStatusStaged},
	}
	for _, c := range cases {
		if err := InsertArtifact(database, c.txID, "", "/state/artifacts/"+c.txID+"/worker", c.status); err != nil {
			t.Fatal(err)
		}
	}

	n, err := CleanupInProgressArtifacts(database)
	if err != nil {
		t.Fatalf("CleanupInProgressArtifacts failed: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 rows updated, got %d", n)
	}

	assertStatus := func(txID, want string) {
		t.Helper()
		got, err := GetArtifactByTxID(database, txID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Fatalf("tx=%s status=%s want=%s", txID, got.Status, want)
		}
	}
	assertStatus("tx-a", ArtifactStatusBuildFailed)
	assertStatus("tx-b", ArtifactStatusTestFailed)
	assertStatus("tx-c", ArtifactStatusSelfCheckFailed)
	assertStatus("tx-d", ArtifactStatusDeployFailed)
	assertStatus("tx-e", ArtifactStatusStaged)
}

func TestClaimApprovedArtifactForDeploy(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-approved-1", "", "/state/artifacts/tx-approved-1/worker", ArtifactStatusApproved); err != nil {
		t.Fatal(err)
	}
	if err := InsertArtifact(database, "tx-staged-1", "", "/state/artifacts/tx-staged-1/worker", ArtifactStatusStaged); err != nil {
		t.Fatal(err)
	}

	got, err := ClaimApprovedArtifactForDeploy(database)
	if err != nil {
		t.Fatalf("ClaimApprovedArtifactForDeploy failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected claimed artifact")
	}
	if got.TxID != "tx-approved-1" {
		t.Fatalf("unexpected tx_id: %s", got.TxID)
	}
	if got.Status != ArtifactStatusDeploying {
		t.Fatalf("unexpected claimed status: %s", got.Status)
	}

	stored, err := GetArtifactByTxID(database, "tx-approved-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != ArtifactStatusDeploying {
		t.Fatalf("unexpected stored status: %s", stored.Status)
	}
	if !stored.DeployStartedAt.Valid {
		t.Fatal("expected deploy_started_at to be set")
	}
}

func TestMarkArtifactDeployCompletedAndFailed(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-deploy-1", "", "/state/artifacts/tx-deploy-1/worker", ArtifactStatusApproved); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimApprovedArtifactForDeploy(database); err != nil {
		t.Fatal(err)
	}

	ok, err := MarkArtifactDeployCompleted(database, "tx-deploy-1")
	if err != nil {
		t.Fatalf("MarkArtifactDeployCompleted failed: %v", err)
	}
	if !ok {
		t.Fatal("expected completed=true")
	}
	updated, err := GetArtifactByTxID(database, "tx-deploy-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != ArtifactStatusDeployedUnstable {
		t.Fatalf("unexpected status: %s", updated.Status)
	}
	if !updated.DeployFinishedAt.Valid {
		t.Fatal("expected deploy_finished_at to be set")
	}

	if err := InsertArtifact(database, "tx-deploy-2", "", "/state/artifacts/tx-deploy-2/worker", ArtifactStatusApproved); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimApprovedArtifactForDeploy(database); err != nil {
		t.Fatal(err)
	}
	ok, err = MarkArtifactDeployFailed(database, "tx-deploy-2", "sha mismatch")
	if err != nil {
		t.Fatalf("MarkArtifactDeployFailed failed: %v", err)
	}
	if !ok {
		t.Fatal("expected failed=true")
	}
	failed, err := GetArtifactByTxID(database, "tx-deploy-2")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != ArtifactStatusDeployFailed {
		t.Fatalf("unexpected status: %s", failed.Status)
	}
	if !failed.LastError.Valid || failed.LastError.String == "" {
		t.Fatal("expected last_error to be set")
	}
}

func TestLatestPromotedTxIDAndMetadataSetters(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-p1", "", "/state/artifacts/tx-p1/worker", ArtifactStatusPromoted); err != nil {
		t.Fatal(err)
	}
	if err := InsertArtifact(database, "tx-p2", "", "/state/artifacts/tx-p2/worker", ArtifactStatusPromoted); err != nil {
		t.Fatal(err)
	}
	got, err := LatestPromotedTxID(database)
	if err != nil {
		t.Fatal(err)
	}
	if got != "tx-p2" {
		t.Fatalf("unexpected latest promoted tx: %s", got)
	}

	if err := InsertArtifact(database, "tx-meta", "", "/state/artifacts/tx-meta/worker", ArtifactStatusBuilding); err != nil {
		t.Fatal(err)
	}
	if err := SetArtifactBuildMetadata(database, "tx-meta", "abc123", "rev123"); err != nil {
		t.Fatal(err)
	}
	if err := SetArtifactTestSummary(database, "tx-meta", `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if err := SetArtifactSelfCheckSummary(database, "tx-meta", `{"self_check":"skipped"}`); err != nil {
		t.Fatal(err)
	}
	artifact, err := GetArtifactByTxID(database, "tx-meta")
	if err != nil {
		t.Fatal(err)
	}
	if !artifact.SHA256.Valid || artifact.SHA256.String != "abc123" {
		t.Fatalf("unexpected sha: %+v", artifact.SHA256)
	}
	if !artifact.GitRevision.Valid || artifact.GitRevision.String != "rev123" {
		t.Fatalf("unexpected revision: %+v", artifact.GitRevision)
	}
	if !artifact.TestSummary.Valid || artifact.TestSummary.String == "" {
		t.Fatalf("unexpected test summary: %+v", artifact.TestSummary)
	}
	if !artifact.SelfCheckSummary.Valid || artifact.SelfCheckSummary.String == "" {
		t.Fatalf("unexpected self-check summary: %+v", artifact.SelfCheckSummary)
	}
}

func TestLatestArtifactByStatusAndPromoteRollbackTransitions(t *testing.T) {
	database := testDB(t)
	if err := InsertArtifact(database, "tx-u1", "", "/state/artifacts/tx-u1/worker", ArtifactStatusDeployedUnstable); err != nil {
		t.Fatal(err)
	}
	if err := InsertArtifact(database, "tx-u2", "", "/state/artifacts/tx-u2/worker", ArtifactStatusDeployedUnstable); err != nil {
		t.Fatal(err)
	}

	latest, err := LatestArtifactByStatus(database, ArtifactStatusDeployedUnstable)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.TxID != "tx-u2" {
		t.Fatalf("unexpected latest unstable artifact: %+v", latest)
	}

	ok, err := MarkArtifactPromoted(database, "tx-u2")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected promoted transition success")
	}
	promoted, err := GetArtifactByTxID(database, "tx-u2")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != ArtifactStatusPromoted {
		t.Fatalf("unexpected status: %s", promoted.Status)
	}

	ok, err = MarkArtifactRollbackPending(database, "tx-u1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected rollback_pending transition success")
	}
	ok, err = MarkArtifactRolledBack(database, "tx-u1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected rolled_back transition success")
	}
	rb, err := GetArtifactByTxID(database, "tx-u1")
	if err != nil {
		t.Fatal(err)
	}
	if rb.Status != ArtifactStatusRolledBack {
		t.Fatalf("unexpected status: %s", rb.Status)
	}
}

func TestEnsureBootstrapPromotedArtifact(t *testing.T) {
	database := testDB(t)
	if err := EnsureBootstrapPromotedArtifact(database, "bootstrap", "/state/artifacts/bootstrap/worker"); err != nil {
		t.Fatal(err)
	}
	got, err := GetArtifactByTxID(database, "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ArtifactStatusPromoted {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	// idempotent
	if err := EnsureBootstrapPromotedArtifact(database, "bootstrap", "/state/artifacts/bootstrap/worker"); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := database.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE tx_id = ?`, "bootstrap").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("expected single bootstrap artifact, got %d", cnt)
	}
}
