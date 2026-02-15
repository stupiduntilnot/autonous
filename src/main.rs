use anyhow::{Context, Result};
use reqwest::Client;
use rusqlite::{params, Connection, OptionalExtension};
use serde::Deserialize;
use std::env;
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;
use tempfile::NamedTempFile;
use tokio::time::sleep;

#[derive(Debug, Clone)]
struct Config {
    api_base: String,
    offset: i64,
    timeout: u64,
    sleep_seconds: u64,
    drop_pending: bool,
    use_codex: bool,
    codex_workdir: PathBuf,
    history_window: usize,
}

impl Config {
    fn from_env() -> Result<Self> {
        let token = env::var("TELEGRAM_BOT_TOKEN")
            .context("TELEGRAM_BOT_TOKEN is required in environment")?;
        let offset = env::var("TG_OFFSET_START")
            .ok()
            .and_then(|v| v.parse::<i64>().ok())
            .unwrap_or(0);
        let timeout = env::var("TG_TIMEOUT")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(30);
        let sleep_seconds = env::var("TG_SLEEP_SECONDS")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(1);
        let drop_pending = env::var("TG_DROP_PENDING")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(true);
        let use_codex = env::var("USE_CODEX")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(true);
        let codex_workdir = PathBuf::from(
            env::var("CODEX_WORKDIR").unwrap_or_else(|_| "/workspace".to_string()),
        );
        let history_window = env::var("TG_HISTORY_WINDOW")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(12);

        Ok(Self {
            api_base: format!("https://api.telegram.org/bot{}", token),
            offset,
            timeout,
            sleep_seconds,
            drop_pending,
            use_codex,
            codex_workdir,
            history_window,
        })
    }
}

struct App {
    config: Config,
    http: Client,
    db: Connection,
}

impl App {
    fn new(config: Config, db: Connection) -> Result<Self> {
        let http = Client::builder()
            .timeout(Duration::from_secs(config.timeout + 10))
            .build()
            .context("failed to build http client")?;

        Ok(Self { config, http, db })
    }

    fn init_db(&self) -> Result<()> {
        self.db.execute_batch(
            "
            CREATE TABLE IF NOT EXISTS history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                chat_id INTEGER NOT NULL,
                role TEXT NOT NULL,
                text TEXT NOT NULL,
                created_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE TABLE IF NOT EXISTS kv (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL
            );
            ",
        )?;
        Ok(())
    }

    fn load_offset(&self) -> Result<Option<i64>> {
        let value: Option<String> = self
            .db
            .query_row("SELECT value FROM kv WHERE key = 'telegram_offset'", [], |r| r.get(0))
            .optional()?;
        Ok(value.and_then(|v| v.parse::<i64>().ok()))
    }

    fn save_offset(&self, offset: i64) -> Result<()> {
        self.db.execute(
            "INSERT INTO kv (key, value) VALUES ('telegram_offset', ?1)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            params![offset.to_string()],
        )?;
        Ok(())
    }

    fn append_history(&self, chat_id: i64, role: &str, text: &str) -> Result<()> {
        self.db.execute(
            "INSERT INTO history (chat_id, role, text) VALUES (?1, ?2, ?3)",
            params![chat_id, role, text],
        )?;
        Ok(())
    }

    fn recent_history(&self, chat_id: i64, limit: usize) -> Result<Vec<(String, String)>> {
        let mut stmt = self.db.prepare(
            "SELECT role, text
             FROM history
             WHERE chat_id = ?1
             ORDER BY id DESC
             LIMIT ?2",
        )?;
        let mut rows = stmt.query(params![chat_id, limit as i64])?;
        let mut out = Vec::new();
        while let Some(row) = rows.next()? {
            let role: String = row.get(0)?;
            let text: String = row.get(1)?;
            out.push((role, text));
        }
        out.reverse();
        Ok(out)
    }

    fn build_prompt_with_history(&self, chat_id: i64, user_text: &str) -> Result<String> {
        let history = self.recent_history(chat_id, self.config.history_window)?;
        let history_text = if history.is_empty() {
            "(no history yet)".to_string()
        } else {
            history
                .into_iter()
                .map(|(role, text)| format!("{role}: {}", text.replace('\n', " ")))
                .collect::<Vec<_>>()
                .join("\n")
        };
        let prompt = format!(
            "You are an autonomous coding agent controlled from Telegram.\n\
             Keep replies concise and executable.\n\
             If asked to modify files, do it directly in the workspace.\n\n\
             Recent chat history:\n\
             {history_text}\n\n\
             Current user message:\n\
             {user_text}\n"
        );
        Ok(prompt)
    }

    async fn bootstrap_offset(&self) -> Result<i64> {
        let updates = self.get_updates(0, 0).await?;
        let next = updates
            .last()
            .map(|u| u.update_id + 1)
            .unwrap_or(0_i64);
        Ok(next)
    }

    async fn get_updates(&self, offset: i64, timeout: u64) -> Result<Vec<Update>> {
        let response = self
            .http
            .get(format!("{}/getUpdates", self.config.api_base))
            .query(&[("timeout", timeout.to_string()), ("offset", offset.to_string())])
            .send()
            .await
            .context("telegram getUpdates request failed")?;

        let body = response.text().await?;
        let decoded: TelegramResponse<Vec<Update>> = serde_json::from_str(&body)
            .with_context(|| format!("failed to parse getUpdates response: {body}"))?;
        if !decoded.ok {
            return Ok(Vec::new());
        }
        Ok(decoded.result)
    }

    async fn send_message(&self, chat_id: i64, text: &str) -> Result<()> {
        let limited = truncate_for_telegram(text, 3900);
        let req = SendMessageRequest {
            chat_id,
            text: &limited,
        };

        self.http
            .post(format!("{}/sendMessage", self.config.api_base))
            .json(&req)
            .send()
            .await
            .context("telegram sendMessage request failed")?;
        Ok(())
    }

    fn ensure_codex_auth(&self) -> Result<()> {
        if !self.config.use_codex {
            return Ok(());
        }
        if !command_exists("codex") {
            return Ok(());
        }
        let Some(key) = env::var("OPENAI_API_KEY").ok() else {
            eprintln!("OPENAI_API_KEY missing; skipping codex login");
            return Ok(());
        };

        let mut child = Command::new("codex")
            .arg("login")
            .arg("--with-api-key")
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .context("failed to start codex login")?;
        if let Some(stdin) = child.stdin.as_mut() {
            stdin
                .write_all(key.as_bytes())
                .context("failed to write key to codex login stdin")?;
        }
        let _ = child.wait();
        Ok(())
    }

    fn run_agent(&self, prompt: &str) -> Result<String> {
        if !self.config.use_codex {
            return Ok(format!("Echo: {prompt}"));
        }
        if !command_exists("codex") {
            return Ok("Agent error: codex CLI not found on PATH.".to_string());
        }

        let out_file = NamedTempFile::new()?;
        let out_path = out_file.path().to_path_buf();

        let output = Command::new("codex")
            .arg("exec")
            .arg("--sandbox")
            .arg("workspace-write")
            .arg("--skip-git-repo-check")
            .arg("-C")
            .arg(&self.config.codex_workdir)
            .arg("--output-last-message")
            .arg(&out_path)
            .arg(prompt)
            .output()
            .context("failed to run codex exec")?;

        let reply = fs::read_to_string(&out_path).unwrap_or_default();
        if output.status.success() && !reply.trim().is_empty() {
            return Ok(reply);
        }

        let stderr = String::from_utf8_lossy(&output.stderr);
        let last = stderr.lines().last().unwrap_or("unknown codex error").trim();
        Ok(format!("Agent error: Codex failed ({last})."))
    }
}

#[derive(Debug, Deserialize)]
struct TelegramResponse<T> {
    ok: bool,
    result: T,
}

#[derive(Debug, Deserialize)]
struct Update {
    update_id: i64,
    message: Option<Message>,
}

#[derive(Debug, Deserialize)]
struct Message {
    chat: Chat,
    text: Option<String>,
}

#[derive(Debug, Deserialize)]
struct Chat {
    id: i64,
}

#[derive(serde::Serialize)]
struct SendMessageRequest<'a> {
    chat_id: i64,
    text: &'a str,
}

#[tokio::main]
async fn main() -> Result<()> {
    let mut config = Config::from_env()?;

    let db_path = env::var("TG_DB_PATH").unwrap_or_else(|_| "/state/agent.db".to_string());
    if let Some(parent) = Path::new(&db_path).parent() {
        fs::create_dir_all(parent)
            .with_context(|| format!("failed to create db directory {}", parent.display()))?;
    }
    let db = Connection::open(&db_path).with_context(|| format!("failed to open db at {db_path}"))?;

    let app = App::new(config.clone(), db)?;
    app.init_db()?;
    app.ensure_codex_auth()?;

    if let Some(saved) = app.load_offset()? {
        config.offset = saved;
    }
    if config.drop_pending {
        config.offset = app.bootstrap_offset().await?;
        app.save_offset(config.offset)?;
    }

    println!("agent loop running (Ctrl+C to stop)");
    loop {
        let updates = match app.get_updates(config.offset, config.timeout).await {
            Ok(v) => v,
            Err(err) => {
                eprintln!("getUpdates error: {err:#}");
                sleep(Duration::from_secs(config.sleep_seconds)).await;
                continue;
            }
        };

        for update in updates {
            config.offset = update.update_id + 1;
            let _ = app.save_offset(config.offset);

            let Some(message) = update.message else {
                continue;
            };
            let Some(text) = message.text else {
                continue;
            };
            if text.trim().is_empty() {
                continue;
            }

            let chat_id = message.chat.id;
            println!("recv chat_id={} text={}", chat_id, text);

            let _ = app.append_history(chat_id, "user", &text);
            let prompt = app.build_prompt_with_history(chat_id, &text)?;
            let reply = app
                .run_agent(&prompt)
                .unwrap_or_else(|e| format!("Agent error: {e:#}"));
            let _ = app.append_history(chat_id, "assistant", &reply);

            println!("send chat_id={} text={}", chat_id, reply);
            if let Err(err) = app.send_message(chat_id, &reply).await {
                eprintln!("sendMessage error: {err:#}");
            }
        }

        sleep(Duration::from_secs(config.sleep_seconds)).await;
    }
}

fn command_exists(name: &str) -> bool {
    Command::new("sh")
        .arg("-lc")
        .arg(format!("command -v {name} >/dev/null 2>&1"))
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

fn truncate_for_telegram(input: &str, max_chars: usize) -> String {
    input.chars().take(max_chars).collect()
}
