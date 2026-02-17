package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
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

		// Log worker.spawned.
		db.LogEvent(database, &supEventID, db.EventWorkerSpawned, map[string]any{
			"pid": cmd.Process.Pid,
		})

		err = cmd.Wait()
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
				attemptRollback(&cfg, database, supEventID)
				crashTimes = nil
			}
		}

		time.Sleep(time.Duration(cfg.RestartDelaySeconds) * time.Second)
	}
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
