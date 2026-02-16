package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/autonous/autonous/internal/config"
	"github.com/autonous/autonous/internal/db"
)

func main() {
	cfg := config.LoadSupervisorConfig()
	database, err := db.OpenDB(cfg.StateDBPath)
	if err != nil {
		log.Fatalf("[supervisor] %v", err)
	}
	defer database.Close()

	if err := db.InitSupervisorSchema(database); err != nil {
		log.Fatalf("[supervisor] failed to init schema: %v", err)
	}

	// Initialize current_good_rev if not set.
	if rev := gitHeadRev(cfg.WorkspaceDir); rev != "" {
		existing, _ := getState(database, "current_good_rev")
		if existing == "" {
			markGoodRevision(database, rev)
			log.Printf("[supervisor] initialized current_good_rev=%s", rev)
		}
	}

	var crashTimes []time.Time

	log.Printf("[supervisor] running worker=%s", cfg.WorkerBin)

	for {
		instanceID, err := nextWorkerInstanceID(database)
		if err != nil {
			log.Fatalf("[supervisor] failed to get instance id: %v", err)
		}
		startedAt := time.Now()

		log.Printf("[supervisor] starting worker instance %s", instanceID)

		cmd := exec.Command(cfg.WorkerBin)
		cmd.Env = append(os.Environ(), "WORKER_INSTANCE_ID="+instanceID)
		cmd.Stdin = nil
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			log.Fatalf("[supervisor] failed to start worker binary %s: %v", cfg.WorkerBin, err)
		}

		err = cmd.Wait()
		uptime := time.Since(startedAt)

		if err == nil {
			log.Printf("[supervisor] worker %s exited normally; restarting in %ds", instanceID, cfg.RestartDelaySeconds)
		} else {
			log.Printf("[supervisor] worker %s exited with error: %v; uptime=%ds", instanceID, err, int(uptime.Seconds()))
		}

		stableThreshold := time.Duration(cfg.StableRunSeconds) * time.Second
		if uptime >= stableThreshold {
			if rev := gitHeadRev(cfg.WorkspaceDir); rev != "" {
				markGoodRevision(database, rev)
			}
			crashTimes = nil
		} else {
			now := time.Now()
			crashTimes = append(crashTimes, now)
			window := time.Duration(cfg.CrashWindowSeconds) * time.Second
			// Retain only crashes within the window.
			filtered := crashTimes[:0]
			for _, t := range crashTimes {
				if now.Sub(t) <= window {
					filtered = append(filtered, t)
				}
			}
			crashTimes = filtered

			if len(crashTimes) >= cfg.CrashThreshold {
				reason := fmt.Sprintf("crash_loop threshold=%d window=%ds", cfg.CrashThreshold, cfg.CrashWindowSeconds)
				setState(database, "last_failure_reason", reason)
				attemptRollback(&cfg, database)
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
	rev := strings.TrimSpace(string(out))
	return rev
}

func attemptRollback(cfg *config.SupervisorConfig, database *sql.DB) {
	rev, _ := getState(database, "current_good_rev")
	if rev == "" {
		log.Printf("[supervisor] rollback skipped: no current_good_rev")
		return
	}

	if !cfg.AutoRollback {
		log.Printf("[supervisor] crash threshold reached; auto rollback disabled. target_rev=%s", rev)
		return
	}

	log.Printf("[supervisor] crash threshold reached; rolling back workspace to %s", rev)

	gitCmd := exec.Command("git", "-C", cfg.WorkspaceDir, "checkout", rev, "--", ".")
	if err := gitCmd.Run(); err != nil {
		setState(database, "last_failure_reason", "rollback_failed_git_checkout")
		log.Printf("[supervisor] rollback failed: git checkout returned error: %v", err)
		return
	}

	buildCmd := exec.Command("go", "build", "-o", cfg.WorkerBin, "./cmd/worker")
	buildCmd.Dir = cfg.WorkspaceDir
	if err := buildCmd.Run(); err != nil {
		setState(database, "last_failure_reason", "rollback_failed_build")
		log.Printf("[supervisor] rollback failed: worker build returned error: %v", err)
		return
	}

	log.Printf("[supervisor] rollback applied and worker rebuilt at rev=%s", rev)
}

func getState(database *sql.DB, key string) (string, error) {
	var value string
	err := database.QueryRow("SELECT value FROM supervisor_state WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func setState(database *sql.DB, key, value string) {
	database.Exec(
		"INSERT INTO supervisor_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
}

func markGoodRevision(database *sql.DB, revision string) {
	database.Exec(
		`INSERT INTO supervisor_revisions (revision, build_ok, health_ok, promoted_at)
		 VALUES (?, 1, 1, unixepoch())
		 ON CONFLICT(revision) DO UPDATE SET build_ok = 1, health_ok = 1, promoted_at = unixepoch()`,
		revision,
	)
	setState(database, "current_good_rev", revision)
}

func nextWorkerInstanceID(database *sql.DB) (string, error) {
	currentStr, _ := getState(database, "worker_instance_seq")
	current := 0
	if currentStr != "" {
		fmt.Sscanf(currentStr, "%d", &current)
	}
	next := current + 1
	setState(database, "worker_instance_seq", fmt.Sprintf("%d", next))
	return fmt.Sprintf("W%06d", next), nil
}
