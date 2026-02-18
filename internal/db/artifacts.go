package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	ArtifactStatusCreated          = "created"
	ArtifactStatusBuilding         = "building"
	ArtifactStatusBuildFailed      = "build_failed"
	ArtifactStatusTesting          = "testing"
	ArtifactStatusTestFailed       = "test_failed"
	ArtifactStatusSelfChecking     = "self_checking"
	ArtifactStatusSelfCheckFailed  = "self_check_failed"
	ArtifactStatusStaged           = "staged"
	ArtifactStatusApproved         = "approved"
	ArtifactStatusDeploying        = "deploying"
	ArtifactStatusDeployedUnstable = "deployed_unstable"
	ArtifactStatusPromoted         = "promoted"
	ArtifactStatusRollbackPending  = "rollback_pending"
	ArtifactStatusRolledBack       = "rolled_back"
	ArtifactStatusDeployFailed     = "deploy_failed"
	ArtifactStatusCancelled        = "cancelled"
)

var (
	ErrArtifactNotFound     = errors.New("artifact not found")
	ErrInvalidStatusTransit = errors.New("invalid artifact status transition")
)

type Artifact struct {
	ID                int64
	TxID              string
	BaseTxID          sql.NullString
	BinPath           string
	SHA256            sql.NullString
	GitRevision       sql.NullString
	BuildStartedAt    sql.NullInt64
	BuildFinishedAt   sql.NullInt64
	TestSummary       sql.NullString
	SelfCheckSummary  sql.NullString
	ApprovalChatID    sql.NullInt64
	ApprovalMessageID sql.NullInt64
	DeployStartedAt   sql.NullInt64
	DeployFinishedAt  sql.NullInt64
	Status            string
	LastError         sql.NullString
	CreatedAt         int64
	UpdatedAt         int64
}

var artifactStatusTransitions = map[string]map[string]struct{}{
	ArtifactStatusCreated: {
		ArtifactStatusBuilding: struct{}{},
	},
	ArtifactStatusBuilding: {
		ArtifactStatusTesting:     struct{}{},
		ArtifactStatusBuildFailed: struct{}{},
	},
	ArtifactStatusTesting: {
		ArtifactStatusSelfChecking: struct{}{},
		ArtifactStatusTestFailed:   struct{}{},
	},
	ArtifactStatusSelfChecking: {
		ArtifactStatusStaged:          struct{}{},
		ArtifactStatusSelfCheckFailed: struct{}{},
	},
	ArtifactStatusStaged: {
		ArtifactStatusApproved:  struct{}{},
		ArtifactStatusCancelled: struct{}{},
	},
	ArtifactStatusApproved: {
		ArtifactStatusDeploying: struct{}{},
	},
	ArtifactStatusDeploying: {
		ArtifactStatusDeployedUnstable: struct{}{},
		ArtifactStatusDeployFailed:     struct{}{},
	},
	ArtifactStatusDeployedUnstable: {
		ArtifactStatusPromoted:        struct{}{},
		ArtifactStatusRollbackPending: struct{}{},
	},
	ArtifactStatusRollbackPending: {
		ArtifactStatusRolledBack: struct{}{},
	},
}

func IsValidArtifactStatusTransition(from, to string) bool {
	next, ok := artifactStatusTransitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

func InsertArtifact(database *sql.DB, txID, baseTxID, binPath, status string) error {
	txID = strings.TrimSpace(txID)
	binPath = strings.TrimSpace(binPath)
	status = strings.TrimSpace(status)
	if txID == "" {
		return fmt.Errorf("tx_id cannot be empty")
	}
	if binPath == "" {
		return fmt.Errorf("bin_path cannot be empty")
	}
	if status == "" {
		return fmt.Errorf("status cannot be empty")
	}
	_, err := database.Exec(
		`INSERT INTO artifacts (tx_id, base_tx_id, bin_path, status) VALUES (?, ?, ?, ?)`,
		txID, nullIfEmpty(baseTxID), binPath, status,
	)
	return err
}

func GetArtifactByTxID(database *sql.DB, txID string) (*Artifact, error) {
	row := database.QueryRow(
		`SELECT id, tx_id, base_tx_id, bin_path, sha256, git_revision,
		        build_started_at, build_finished_at, test_summary, self_check_summary,
		        approval_chat_id, approval_message_id, deploy_started_at, deploy_finished_at,
		        status, last_error, created_at, updated_at
		   FROM artifacts
		  WHERE tx_id = ?`,
		txID,
	)
	var a Artifact
	if err := row.Scan(
		&a.ID, &a.TxID, &a.BaseTxID, &a.BinPath, &a.SHA256, &a.GitRevision,
		&a.BuildStartedAt, &a.BuildFinishedAt, &a.TestSummary, &a.SelfCheckSummary,
		&a.ApprovalChatID, &a.ApprovalMessageID, &a.DeployStartedAt, &a.DeployFinishedAt,
		&a.Status, &a.LastError, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtifactNotFound
		}
		return nil, err
	}
	return &a, nil
}

func TransitionArtifactStatus(database *sql.DB, txID, fromStatus, toStatus, lastError string) (bool, error) {
	if !IsValidArtifactStatusTransition(fromStatus, toStatus) {
		return false, fmt.Errorf("%w: %s -> %s", ErrInvalidStatusTransit, fromStatus, toStatus)
	}

	res, err := database.Exec(
		`UPDATE artifacts
		    SET status = ?, last_error = ?, updated_at = unixepoch()
		  WHERE tx_id = ? AND status = ?`,
		toStatus, nullIfEmpty(lastError), txID, fromStatus,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func CleanupInProgressArtifacts(database *sql.DB) (int64, error) {
	res, err := database.Exec(
		`UPDATE artifacts
		    SET status = CASE status
		        WHEN ? THEN ?
		        WHEN ? THEN ?
		        WHEN ? THEN ?
		        WHEN ? THEN ?
		        ELSE status
		    END,
		    updated_at = unixepoch(),
		    last_error = 'interrupted during startup cleanup'
		  WHERE status IN (?, ?, ?, ?)`,
		ArtifactStatusBuilding, ArtifactStatusBuildFailed,
		ArtifactStatusTesting, ArtifactStatusTestFailed,
		ArtifactStatusSelfChecking, ArtifactStatusSelfCheckFailed,
		ArtifactStatusDeploying, ArtifactStatusDeployFailed,
		ArtifactStatusBuilding, ArtifactStatusTesting, ArtifactStatusSelfChecking, ArtifactStatusDeploying,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
