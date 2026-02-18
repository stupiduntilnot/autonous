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
