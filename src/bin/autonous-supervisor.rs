use anyhow::{Context, Result};
use rusqlite::{params, Connection, OptionalExtension};
use std::env;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};
use tokio::time::sleep;

#[derive(Debug, Clone)]
struct Config {
    /// Path to the worker binary that this supervisor will manage and restart.
    worker_bin: String,
    /// The root directory of the workspace where the worker code resides. Used for Git operations and relative paths.
    workspace_dir: String,
    /// Path to the SQLite database file used by the supervisor to persist its state (e.g., good revisions, crash counts).
    state_db_path: String,
    /// Delay in seconds before restarting a worker process after it exits.
    restart_delay_seconds: u64,
    /// The time window in seconds during which worker crashes are counted towards the crash threshold.
    crash_window_seconds: u64,
    /// The number of crashes within `crash_window_seconds` that triggers a rollback or specific failure handling.
    crash_threshold: usize,
    /// The minimum duration in seconds a worker must run to be considered "stable" and clear the crash counter.
    stable_run_seconds: u64,
    /// A boolean flag indicating whether the supervisor should automatically attempt to roll back to a known good revision upon reaching the crash threshold.
    auto_rollback: bool,
}

impl Config {
    fn from_env() -> Self {
        Self {
            worker_bin: env::var("WORKER_BIN")
                .unwrap_or_else(|_| "/workspace/target/release/autonous-worker".to_string()),
            workspace_dir: env::var("WORKSPACE_DIR").unwrap_or_else(|_| "/workspace".to_string()),
            state_db_path: env::var("TG_DB_PATH").unwrap_or_else(|_| "/state/agent.db".to_string()),
            restart_delay_seconds: env::var("SUPERVISOR_RESTART_DELAY_SECONDS")
                .ok()
                .and_then(|v| v.parse::<u64>().ok())
                .unwrap_or(1),
            crash_window_seconds: env::var("SUPERVISOR_CRASH_WINDOW_SECONDS")
                .ok()
                .and_then(|v| v.parse::<u64>().ok())
                .unwrap_or(300),
            crash_threshold: env::var("SUPERVISOR_CRASH_THRESHOLD")
                .ok()
                .and_then(|v| v.parse::<usize>().ok())
                .unwrap_or(3),
            stable_run_seconds: env::var("SUPERVISOR_STABLE_RUN_SECONDS")
                .ok()
                .and_then(|v| v.parse::<u64>().ok())
                .unwrap_or(30),
            auto_rollback: env::var("SUPERVISOR_AUTO_ROLLBACK")
                .ok()
                .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
                .unwrap_or(false),
        }
    }
}

struct SupervisorState {
    db: Connection,
}

impl SupervisorState {
    fn new(db_path: &str) -> Result<Self> {
        if let Some(parent) = Path::new(db_path).parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("failed to create state db parent dir {}", parent.display()))?;
        }
        let db = Connection::open(db_path).with_context(|| format!("failed to open db at {db_path}"))?;
        let state = Self { db };
        state.init_schema()?;
        Ok(state)
    }

    fn init_schema(&self) -> Result<()> {
        self.db.execute_batch(
            "
            -- Table to store information about different code revisions, used for rollback.
            CREATE TABLE IF NOT EXISTS supervisor_revisions (
                revision TEXT PRIMARY KEY, -- Git commit hash or other unique revision identifier.
                build_ok INTEGER NOT NULL DEFAULT 0, -- 1 if the revision successfully built, 0 otherwise.
                health_ok INTEGER NOT NULL DEFAULT 0, -- 1 if the worker from this revision ran stably, 0 otherwise.
                promoted_at INTEGER, -- Unix timestamp when this revision was last marked as good/stable.
                failure_reason TEXT -- Reason for failure if this revision was not good.
            );
            CREATE TABLE IF NOT EXISTS supervisor_state (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL
            );
            ",
        )?;
        Ok(())
    }

    fn get_state(&self, key: &str) -> Result<Option<String>> {
        let value = self
            .db
            .query_row(
                "SELECT value FROM supervisor_state WHERE key = ?1",
                params![key],
                |r| r.get(0),
            )
            .optional()?;
        Ok(value)
    }

    fn set_state(&self, key: &str, value: &str) -> Result<()> {
        self.db.execute(
            "INSERT INTO supervisor_state (key, value) VALUES (?1, ?2)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            params![key, value],
        )?;
        Ok(())
    }

    fn mark_good_revision(&self, revision: &str) -> Result<()> {
        self.db.execute(
            "INSERT INTO supervisor_revisions (revision, build_ok, health_ok, promoted_at)
             VALUES (?1, 1, 1, unixepoch())
             ON CONFLICT(revision) DO UPDATE SET build_ok = 1, health_ok = 1, promoted_at = unixepoch()",
            params![revision],
        )?;
        self.set_state("current_good_rev", revision)?;
        Ok(())
    }

    fn set_failure(&self, reason: &str) -> Result<()> {
        self.set_state("last_failure_reason", reason)
    }

    fn next_worker_instance_id(&self) -> Result<String> {
        let current = self
            .get_state("worker_instance_seq")?
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(0);
        let next = current + 1;
        self.set_state("worker_instance_seq", &next.to_string())?;
        Ok(format!("W{:06}", next))
    }
}

fn git_head_rev(workspace_dir: &str) -> Option<String> {
    let output = Command::new("git")
        .arg("-C")
        .arg(workspace_dir)
        .arg("rev-parse")
        .arg("HEAD")
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    let text = String::from_utf8_lossy(&output.stdout).trim().to_string();
    if text.is_empty() {
        None
    } else {
        Some(text)
    }
}

fn attempt_rollback(config: &Config, state: &SupervisorState) -> Result<()> {
    let Some(rev) = state.get_state("current_good_rev")? else {
        eprintln!("[supervisor] rollback skipped: no current_good_rev");
        return Ok(());
    };

    if !config.auto_rollback {
        eprintln!("[supervisor] crash threshold reached; auto rollback disabled. target_rev={rev}");
        return Ok(());
    }

    eprintln!("[supervisor] crash threshold reached; rolling back workspace to {rev}");

    let status = Command::new("git")
        .arg("-C")
        .arg(&config.workspace_dir)
        .arg("checkout")
        .arg(&rev)
        .arg("--")
        .arg(".")
        .status()
        .context("failed to execute git checkout for rollback")?;

    if !status.success() {
        state.set_failure("rollback_failed_git_checkout")?;
        anyhow::bail!("rollback failed: git checkout returned non-zero");
    }

    let build_status = Command::new("cargo")
        .arg("build")
        .arg("--release")
        .arg("--manifest-path")
        .arg(format!("{}/Cargo.toml", config.workspace_dir))
        .arg("--bin")
        .arg("autonous-worker")
        .status()
        .context("failed to run cargo build during rollback")?;

    if !build_status.success() {
        state.set_failure("rollback_failed_build")?;
        anyhow::bail!("rollback failed: worker build returned non-zero");
    }

    eprintln!("[supervisor] rollback applied and worker rebuilt at rev={rev}");
    Ok(())
}

#[tokio::main]
async fn main() -> Result<()> {
    let config = Config::from_env();
    let state = SupervisorState::new(&config.state_db_path)?;

    if let Some(rev) = git_head_rev(&config.workspace_dir) {
        if state.get_state("current_good_rev")?.is_none() {
            state.mark_good_revision(&rev)?;
            eprintln!("[supervisor] initialized current_good_rev={rev}");
        }
    }

    let mut crash_times: Vec<Instant> = Vec::new();

    eprintln!("[supervisor] running worker={}", config.worker_bin);

    loop {
        let worker_instance_id = state.next_worker_instance_id()?;
        let started_at = Instant::now();

        eprintln!("[supervisor] starting worker instance {worker_instance_id}");

        let mut child = Command::new(&config.worker_bin)
            .env("WORKER_INSTANCE_ID", &worker_instance_id)
            .stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit())
            .spawn()
            .with_context(|| format!("failed to start worker binary {}", config.worker_bin))?;

        let status = child.wait().context("failed waiting for worker process")?;
        let uptime = started_at.elapsed();

        if status.success() {
            eprintln!("[supervisor] worker {worker_instance_id} exited normally; restarting in {}s", config.restart_delay_seconds);
        } else {
            eprintln!(
                "[supervisor] worker {worker_instance_id} exited with status {:?}; uptime={}s",
                status.code(),
                uptime.as_secs()
            );
        }

        if uptime >= Duration::from_secs(config.stable_run_seconds) {
            if let Some(rev) = git_head_rev(&config.workspace_dir) {
                let _ = state.mark_good_revision(&rev);
            }
            crash_times.clear();
        } else {
            crash_times.push(Instant::now());
            let window = Duration::from_secs(config.crash_window_seconds);
            crash_times.retain(|t| t.elapsed() <= window);

            if crash_times.len() >= config.crash_threshold {
                let reason = format!(
                    "crash_loop threshold={} window={}s",
                    config.crash_threshold, config.crash_window_seconds
                );
                let _ = state.set_failure(&reason);
                let _ = attempt_rollback(&config, &state);
                crash_times.clear();
            }
        }

        sleep(Duration::from_secs(config.restart_delay_seconds)).await;
    }
}
