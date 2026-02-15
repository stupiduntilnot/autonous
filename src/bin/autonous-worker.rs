use anyhow::{Context, Result};
use reqwest::Client;
use rusqlite::{params, Connection, OptionalExtension};
use serde::Deserialize;
use std::env;
use std::path::Path;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

#[derive(Debug, Clone)]
struct Config {
    api_base: String,
    offset: i64,
    timeout: u64,
    sleep_seconds: u64,
    drop_pending: bool,
    pending_window_seconds: u64,
    worker_instance_id: String,
    suicide_every: u64,
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
        let pending_window_seconds = env::var("TG_PENDING_WINDOW_SECONDS")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(60);
        let worker_instance_id =
            env::var("WORKER_INSTANCE_ID").unwrap_or_else(|_| "W000000".to_string());
        let suicide_every = env::var("WORKER_SUICIDE_EVERY")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .filter(|v| *v > 0)
            .unwrap_or(2);

        Ok(Self {
            api_base: format!("https://api.telegram.org/bot{}", token),
            offset,
            timeout,
            sleep_seconds,
            drop_pending,
            pending_window_seconds,
            worker_instance_id,
            suicide_every,
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

    async fn bootstrap_offset(&self, pending_window_seconds: u64) -> Result<i64> {
        let updates = self.get_updates(0, 0).await?;
        if updates.is_empty() {
            return Ok(0);
        }

        let now = current_unix_timestamp();
        let cutoff = now.saturating_sub(pending_window_seconds as i64);

        if let Some(update_id) = updates
            .iter()
            .find(|u| {
                u.message
                    .as_ref()
                    .map(|m| m.date >= cutoff)
                    .unwrap_or(false)
            })
            .map(|u| u.update_id)
        {
            return Ok(update_id);
        }

        let next = updates.last().map(|u| u.update_id + 1).unwrap_or(0_i64);
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
    date: i64,
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
        std::fs::create_dir_all(parent)
            .with_context(|| format!("failed to create db directory {}", parent.display()))?;
    }
    let db = Connection::open(&db_path).with_context(|| format!("failed to open db at {db_path}"))?;

    let app = App::new(config.clone(), db)?;
    app.init_db()?;

    let had_saved_offset = if let Some(saved) = app.load_offset()? {
        config.offset = saved;
        true
    } else {
        false
    };

    if config.drop_pending && !had_saved_offset {
        config.offset = app.bootstrap_offset(config.pending_window_seconds).await?;
        app.save_offset(config.offset)?;
    }

    let mut handled_count: u64 = 0;

    println!(
        "dummy worker running id={} suicide_every={} pending_window_seconds={} had_saved_offset={}",
        config.worker_instance_id,
        config.suicide_every,
        config.pending_window_seconds,
        had_saved_offset
    );

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
            handled_count += 1;

            let reply = format!(
                "[DummyWorker:{} #{}] {}",
                config.worker_instance_id, handled_count, text
            );

            let _ = app.append_history(chat_id, "user", &text);
            let _ = app.append_history(chat_id, "assistant", &reply);

            println!("recv chat_id={} text={}", chat_id, text);
            println!("send chat_id={} text={}", chat_id, reply);

            if let Err(err) = app.send_message(chat_id, &reply).await {
                eprintln!("sendMessage error: {err:#}");
            }

            if handled_count % config.suicide_every == 0 {
                eprintln!(
                    "dummy worker id={} handled {} messages; exiting intentionally",
                    config.worker_instance_id, handled_count
                );
                std::process::exit(17);
            }
        }

        sleep(Duration::from_secs(config.sleep_seconds)).await;
    }
}

fn current_unix_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

fn truncate_for_telegram(input: &str, max_chars: usize) -> String {
    input.chars().take(max_chars).collect()
}
