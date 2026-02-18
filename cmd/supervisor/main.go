package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stupiduntilnot/autonous/internal/config"
	"github.com/stupiduntilnot/autonous/internal/db"
)

func main() {
	cfg := config.LoadSupervisorConfig()
	database, err := db.OpenDB(cfg.StateDBPath)
	if err != nil {
		log.Fatalf("[supervisor] %v", err)
	}
	defer database.Close()

	if err := db.InitSchema(database); err != nil {
		log.Fatalf("[supervisor] failed to init schema: %v", err)
	}

	// Log process.started for supervisor.
	supEventID, err := db.LogEvent(database, nil, db.EventProcessStarted, map[string]any{
		"role":    "supervisor",
		"pid":     os.Getpid(),
		"version": gitHeadRev(cfg.WorkspaceDir),
	})
	if err != nil {
		log.Fatalf("[supervisor] failed to log process.started: %v", err)
	}

	if err := ensureBootstrapArtifactRecord(&cfg, database, supEventID); err != nil {
		log.Printf("[supervisor] bootstrap artifact init failed: %v", err)
	}

	// M5 startup cleanup: normalize interrupted in-progress artifact states.
	if cleaned, cerr := db.CleanupInProgressArtifacts(database); cerr != nil {
		log.Printf("[supervisor] startup cleanup failed: %v", cerr)
	} else if cleaned > 0 {
		log.Printf("[supervisor] startup cleanup updated %d in-progress artifacts", cleaned)
		db.LogEvent(database, &supEventID, "update.cleanup.completed", map[string]any{
			"affected_rows": cleaned,
		})
	}

	// Initialize current_good_rev if no revision.promoted event exists.
	if rev := gitHeadRev(cfg.WorkspaceDir); rev != "" {
		existing, _ := db.CurrentGoodRev(database)
		if existing == "" {
			db.LogEvent(database, &supEventID, db.EventRevisionPromoted, map[string]any{"revision": rev})
			log.Printf("[supervisor] initialized current_good_rev=%s", rev)
		}
	}

	var crashTimes []time.Time

	log.Printf("[supervisor] running worker=%s", cfg.WorkerBin)

	for {
		if err := deployApprovedArtifact(&cfg, database, supEventID); err != nil {
			log.Printf("[supervisor] deploy approved artifact failed: %v", err)
		}

		seq, err := db.NextWorkerSeq(database, supEventID)
		if err != nil {
			log.Fatalf("[supervisor] failed to get worker seq: %v", err)
		}
		instanceID := fmt.Sprintf("W%06d", seq)
		startedAt := time.Now()

		log.Printf("[supervisor] starting worker instance %s", instanceID)

		cmd := exec.Command(cfg.WorkerBin)
		cmd.Env = append(os.Environ(),
			"WORKER_INSTANCE_ID="+instanceID,
			fmt.Sprintf("PARENT_PROCESS_ID=%d", supEventID),
		)
		cmd.Stdin = nil
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			log.Fatalf("[supervisor] failed to start worker binary %s: %v", cfg.WorkerBin, err)
		}

		var workerExited atomic.Bool
		startAutoPromoteWatcher(database, supEventID, startedAt, time.Duration(cfg.StableRunSeconds)*time.Second, &workerExited)

		// Log worker.spawned.
		db.LogEvent(database, &supEventID, db.EventWorkerSpawned, map[string]any{
			"pid": cmd.Process.Pid,
		})

		err = cmd.Wait()
		workerExited.Store(true)
		uptime := time.Since(startedAt)

		// Log worker.exited.
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		db.LogEvent(database, &supEventID, db.EventWorkerExited, map[string]any{
			"exit_code":      exitCode,
			"uptime_seconds": int(uptime.Seconds()),
		})

		if err == nil {
			log.Printf("[supervisor] worker %s exited normally; restarting in %ds", instanceID, cfg.RestartDelaySeconds)
		} else {
			log.Printf("[supervisor] worker %s exited with error: %v; uptime=%ds", instanceID, err, int(uptime.Seconds()))
		}

		stableThreshold := time.Duration(cfg.StableRunSeconds) * time.Second
		if uptime >= stableThreshold {
			if err := promoteLatestDeployedArtifact(database, supEventID); err != nil {
				log.Printf("[supervisor] promote artifact failed: %v", err)
			}
			if rev := gitHeadRev(cfg.WorkspaceDir); rev != "" {
				db.LogEvent(database, &supEventID, db.EventRevisionPromoted, map[string]any{"revision": rev})
			}
			crashTimes = nil
		} else {
			now := time.Now()
			crashTimes = append(crashTimes, now)
			window := time.Duration(cfg.CrashWindowSeconds) * time.Second
			filtered := crashTimes[:0]
			for _, t := range crashTimes {
				if now.Sub(t) <= window {
					filtered = append(filtered, t)
				}
			}
			crashTimes = filtered

			if len(crashTimes) >= cfg.CrashThreshold {
				db.LogEvent(database, &supEventID, db.EventCrashLoopDetected, map[string]any{
					"threshold":      cfg.CrashThreshold,
					"window_seconds": cfg.CrashWindowSeconds,
				})
				rolledBack, err := attemptArtifactRollback(&cfg, database, supEventID)
				if err != nil {
					log.Printf("[supervisor] artifact rollback failed: %v", err)
				}
				if !rolledBack {
					attemptRollback(&cfg, database, supEventID)
				}
				crashTimes = nil
			}
		}

		time.Sleep(time.Duration(cfg.RestartDelaySeconds) * time.Second)
	}
}

func ensureBootstrapArtifactRecord(cfg *config.SupervisorConfig, database *sql.DB, supEventID int64) error {
	latest, err := db.LatestPromotedTxID(database)
	if err != nil {
		return err
	}
	if latest != "" {
		return nil
	}

	binPath := cfg.WorkerBin
	if resolved, rerr := filepath.EvalSymlinks(cfg.WorkerBin); rerr == nil && strings.TrimSpace(resolved) != "" {
		binPath = resolved
	}
	if strings.TrimSpace(binPath) == "" {
		return fmt.Errorf("empty worker bin for bootstrap artifact")
	}
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("bootstrap binary missing at %s: %w", binPath, err)
	}
	if err := db.EnsureBootstrapPromotedArtifact(database, "bootstrap", binPath); err != nil {
		return err
	}
	db.LogEvent(database, &supEventID, "update.promoted", map[string]any{
		"tx_id":      "bootstrap",
		"base_tx_id": "",
	})
	return nil
}

func promoteLatestDeployedArtifact(database *sql.DB, supEventID int64) error {
	artifact, err := db.LatestArtifactByStatus(database, db.ArtifactStatusDeployedUnstable)
	if err != nil {
		return err
	}
	if artifact == nil {
		return nil
	}
	ok, err := db.MarkArtifactPromoted(database, artifact.TxID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	db.LogEvent(database, &supEventID, "update.promoted", map[string]any{
		"tx_id":      artifact.TxID,
		"base_tx_id": nullStringToString(artifact.BaseTxID),
	})
	notifyArtifactStatus("promoted", artifact)
	return nil
}

func attemptArtifactRollback(cfg *config.SupervisorConfig, database *sql.DB, supEventID int64) (bool, error) {
	artifact, err := db.LatestArtifactByStatus(database, db.ArtifactStatusDeployedUnstable)
	if err != nil {
		return false, err
	}
	if artifact == nil {
		return false, nil
	}
	if !artifact.BaseTxID.Valid || strings.TrimSpace(artifact.BaseTxID.String) == "" {
		return false, nil
	}
	ok, err := db.MarkArtifactRollbackPending(database, artifact.TxID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	baseArtifact, err := db.GetArtifactByTxID(database, artifact.BaseTxID.String)
	if err != nil {
		return false, err
	}
	if err := atomicSwitchSymlink(cfg.WorkerBin, baseArtifact.BinPath); err != nil {
		db.LogEvent(database, &supEventID, db.EventRollbackAttempted, map[string]any{
			"target_tx_id": artifact.BaseTxID.String,
			"success":      false,
			"error":        err.Error(),
		})
		return false, err
	}
	ok, err = db.MarkArtifactRolledBack(database, artifact.TxID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	db.LogEvent(database, &supEventID, db.EventRollbackAttempted, map[string]any{
		"target_tx_id": artifact.BaseTxID.String,
		"success":      true,
	})
	db.LogEvent(database, &supEventID, "update.rollback.completed", map[string]any{
		"tx_id":      artifact.TxID,
		"base_tx_id": artifact.BaseTxID.String,
	})
	notifyArtifactStatus("rolled_back", artifact)
	return true, nil
}

func nullStringToString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func startAutoPromoteWatcher(database *sql.DB, supEventID int64, startedAt time.Time, stableAfter time.Duration, workerExited *atomic.Bool) {
	if stableAfter <= 0 {
		stableAfter = 30 * time.Second
	}
	go func() {
		timer := time.NewTimer(stableAfter)
		defer timer.Stop()
		<-timer.C
		if workerExited.Load() {
			return
		}
		if time.Since(startedAt) < stableAfter {
			return
		}
		if err := promoteLatestDeployedArtifact(database, supEventID); err != nil {
			log.Printf("[supervisor] auto promote failed: %v", err)
		}
	}()
}

func deployApprovedArtifact(cfg *config.SupervisorConfig, database *sql.DB, supEventID int64) error {
	artifact, err := db.ClaimApprovedArtifactForDeploy(database)
	if err != nil {
		return err
	}
	if artifact == nil {
		return nil
	}
	db.LogEvent(database, &supEventID, "update.deploy.started", map[string]any{
		"tx_id":      artifact.TxID,
		"bin_path":   artifact.BinPath,
		"target_bin": cfg.WorkerBin,
	})

	if artifact.SHA256.Valid && strings.TrimSpace(artifact.SHA256.String) != "" {
		sum, serr := fileSHA256Hex(artifact.BinPath)
		if serr != nil {
			_, _ = db.MarkArtifactDeployFailed(database, artifact.TxID, serr.Error())
			db.LogEvent(database, &supEventID, "update.deploy.failed", map[string]any{
				"tx_id":  artifact.TxID,
				"error":  serr.Error(),
				"reason": "sha256_read",
			})
			return serr
		}
		if !strings.EqualFold(sum, artifact.SHA256.String) {
			msg := fmt.Sprintf("sha256 mismatch: got=%s want=%s", sum, artifact.SHA256.String)
			_, _ = db.MarkArtifactDeployFailed(database, artifact.TxID, msg)
			db.LogEvent(database, &supEventID, "update.deploy.failed", map[string]any{
				"tx_id":  artifact.TxID,
				"error":  msg,
				"reason": "sha256_mismatch",
			})
			return fmt.Errorf("%s", msg)
		}
	}

	if err := atomicSwitchSymlink(cfg.WorkerBin, artifact.BinPath); err != nil {
		_, _ = db.MarkArtifactDeployFailed(database, artifact.TxID, err.Error())
		db.LogEvent(database, &supEventID, "update.deploy.failed", map[string]any{
			"tx_id":  artifact.TxID,
			"error":  err.Error(),
			"reason": "switch_symlink",
		})
		return err
	}
	if ok, err := db.MarkArtifactDeployCompleted(database, artifact.TxID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("failed to mark deployed_unstable for tx_id=%s", artifact.TxID)
	}
	db.LogEvent(database, &supEventID, "update.deploy.completed", map[string]any{
		"tx_id": artifact.TxID,
	})
	notifyArtifactStatus("deployed_unstable", artifact)
	log.Printf("[supervisor] deployed approved artifact tx_id=%s", artifact.TxID)
	return nil
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func atomicSwitchSymlink(activeBin, newBinPath string) error {
	if !filepath.IsAbs(activeBin) || !filepath.IsAbs(newBinPath) {
		return fmt.Errorf("active/new path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(activeBin), 0o755); err != nil {
		return err
	}
	tmpLink := activeBin + ".tmp"
	_ = os.Remove(tmpLink)
	if err := os.Symlink(newBinPath, tmpLink); err != nil {
		return err
	}
	if err := os.Rename(tmpLink, activeBin); err != nil {
		_ = os.Remove(tmpLink)
		return err
	}
	return nil
}

func notifyArtifactStatus(status string, artifact *db.Artifact) {
	if artifact == nil || !artifact.ApprovalChatID.Valid {
		return
	}
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return
	}
	text := fmt.Sprintf("update %s: tx_id=%s", status, artifact.TxID)
	if artifact.BaseTxID.Valid && strings.TrimSpace(artifact.BaseTxID.String) != "" {
		text += " base_tx_id=" + artifact.BaseTxID.String
	}
	if err := sendTelegramText(token, artifact.ApprovalChatID.Int64, text); err != nil {
		log.Printf("[supervisor] notify failed: %v", err)
	}
}

func sendTelegramText(botToken string, chatID int64, text string) error {
	endpoint := "https://api.telegram.org/bot" + botToken + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", fmt.Sprintf("%d", chatID))
	form.Set("text", text)
	resp, err := http.PostForm(endpoint, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram send status=%d", resp.StatusCode)
	}
	return nil
}

func gitHeadRev(workspaceDir string) string {
	out, err := exec.Command("git", "-C", workspaceDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func attemptRollback(cfg *config.SupervisorConfig, database *sql.DB, supEventID int64) {
	rev, _ := db.CurrentGoodRev(database)
	if rev == "" {
		log.Printf("[supervisor] rollback skipped: no current_good_rev")
		return
	}

	if !cfg.AutoRollback {
		log.Printf("[supervisor] crash threshold reached; auto rollback disabled. target_rev=%s", rev)
		db.LogEvent(database, &supEventID, db.EventRollbackAttempted, map[string]any{
			"target_revision": rev,
			"success":         false,
		})
		return
	}

	log.Printf("[supervisor] crash threshold reached; rolling back workspace to %s", rev)

	gitCmd := exec.Command("git", "-C", cfg.WorkspaceDir, "checkout", rev, "--", ".")
	if err := gitCmd.Run(); err != nil {
		log.Printf("[supervisor] rollback failed: git checkout returned error: %v", err)
		db.LogEvent(database, &supEventID, db.EventRollbackAttempted, map[string]any{
			"target_revision": rev,
			"success":         false,
		})
		return
	}

	buildCmd := exec.Command("go", "build", "-o", cfg.WorkerBin, "./cmd/worker")
	buildCmd.Dir = cfg.WorkspaceDir
	if err := buildCmd.Run(); err != nil {
		log.Printf("[supervisor] rollback failed: worker build returned error: %v", err)
		db.LogEvent(database, &supEventID, db.EventRollbackAttempted, map[string]any{
			"target_revision": rev,
			"success":         false,
		})
		return
	}

	db.LogEvent(database, &supEventID, db.EventRollbackAttempted, map[string]any{
		"target_revision": rev,
		"success":         true,
	})
	log.Printf("[supervisor] rollback applied and worker rebuilt at rev=%s", rev)
}
