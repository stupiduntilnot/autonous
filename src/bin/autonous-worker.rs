use anyhow::{bail, Context, Result};
use reqwest::Client;
use rusqlite::{params, Connection, OptionalExtension, TransactionBehavior};
use serde::{Deserialize, Serialize};
use std::env;
use std::path::Path;
use std::process;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::sleep;

#[derive(Debug, Clone)]
struct Config {
    telegram_api_base: String,
    offset: i64,
    timeout: u64,
    sleep_seconds: u64,
    drop_pending: bool,
    pending_window_seconds: u64,
    pending_max_messages: usize,
    history_window: usize,
    worker_instance_id: String,
    run_id: String,
    suicide_every: u64,
    openai_api_key: String,
    openai_chat_completions_url: String,
    openai_model: String,
    system_prompt: String,
}

impl Config {
    fn from_env() -> Result<Self> {
        let telegram_token = env::var("TELEGRAM_BOT_TOKEN")
            .context("TELEGRAM_BOT_TOKEN is required in environment")?;
        let openai_api_key =
            env::var("OPENAI_API_KEY").context("OPENAI_API_KEY is required in environment")?;

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
            .unwrap_or(600);
        let pending_max_messages = env::var("TG_PENDING_MAX_MESSAGES")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .filter(|v| *v > 0)
            .unwrap_or(50);
        let history_window = env::var("TG_HISTORY_WINDOW")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(12);
        let worker_instance_id =
            env::var("WORKER_INSTANCE_ID").unwrap_or_else(|_| "W000000".to_string());
        let run_id = format!(
            "R{}-{}-{}",
            current_unix_timestamp(),
            process::id(),
            worker_instance_id
        );
        let suicide_every = env::var("WORKER_SUICIDE_EVERY")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(0);

        let openai_chat_completions_url = env::var("OPENAI_CHAT_COMPLETIONS_URL")
            .unwrap_or_else(|_| "https://api.openai.com/v1/chat/completions".to_string());
        let openai_model = env::var("OPENAI_MODEL").unwrap_or_else(|_| "gpt-4o-mini".to_string());
        let system_prompt = env::var("WORKER_SYSTEM_PROMPT").unwrap_or_else(|_| {
            "你是 autonous 的执行 Worker。回复简洁、准确；需要时给出可执行步骤。".to_string()
        });

        Ok(Self {
            telegram_api_base: format!("https://api.telegram.org/bot{}", telegram_token),
            offset,
            timeout,
            sleep_seconds,
            drop_pending,
            pending_window_seconds,
            pending_max_messages,
            history_window,
            worker_instance_id,
            run_id,
            suicide_every,
            openai_api_key,
            openai_chat_completions_url,
            openai_model,
            system_prompt,
        })
    }
}

#[derive(Debug, Clone)]
struct QueueTask {
    id: i64,
    chat_id: i64,
    update_id: i64,
    text: String,
}

struct App {
    config: Config,
    http: Client,
    db: Connection,
}

impl App {
    fn new(config: Config, db: Connection) -> Result<Self> {
        let http = Client::builder()
            .timeout(Duration::from_secs(config.timeout + 20))
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
            CREATE TABLE IF NOT EXISTS inbox (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                update_id INTEGER NOT NULL UNIQUE,
                chat_id INTEGER NOT NULL,
                text TEXT NOT NULL,
                message_date INTEGER NOT NULL,
                status TEXT NOT NULL DEFAULT 'queued',
                attempts INTEGER NOT NULL DEFAULT 0,
                locked_at INTEGER,
                error TEXT,
                created_at INTEGER NOT NULL DEFAULT (unixepoch()),
                updated_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE INDEX IF NOT EXISTS idx_inbox_status_id ON inbox(status, id);
            CREATE TABLE IF NOT EXISTS task_audit (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                task_id INTEGER NOT NULL,
                chat_id INTEGER NOT NULL,
                update_id INTEGER,
                phase TEXT NOT NULL,
                status TEXT NOT NULL,
                message TEXT,
                error TEXT,
                run_id TEXT NOT NULL,
                worker_instance_id TEXT NOT NULL,
                created_at INTEGER NOT NULL DEFAULT (unixepoch())
            );
            CREATE INDEX IF NOT EXISTS idx_task_audit_task_id ON task_audit(task_id, id);
            CREATE INDEX IF NOT EXISTS idx_task_audit_phase ON task_audit(phase, created_at);
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

    fn build_messages(&self, chat_id: i64, user_text: &str) -> Result<Vec<OpenAIMessage>> {
        let mut messages = Vec::new();
        messages.push(OpenAIMessage {
            role: "system".to_string(),
            content: self.config.system_prompt.clone(),
        });

        for (role, text) in self.recent_history(chat_id, self.config.history_window)? {
            let mapped = if role == "assistant" { "assistant" } else { "user" };
            messages.push(OpenAIMessage {
                role: mapped.to_string(),
                content: text,
            });
        }

        messages.push(OpenAIMessage {
            role: "user".to_string(),
            content: user_text.to_string(),
        });

        Ok(messages)
    }

    fn enqueue_message(&self, update_id: i64, chat_id: i64, text: &str, message_date: i64) -> Result<bool> {
        let inserted = self.db.execute(
            "INSERT OR IGNORE INTO inbox (update_id, chat_id, text, message_date, status, updated_at)
             VALUES (?1, ?2, ?3, ?4, 'queued', unixepoch())",
            params![update_id, chat_id, text, message_date],
        )?;
        Ok(inserted > 0)
    }

    fn get_task_id_by_update_id(&self, update_id: i64) -> Result<Option<i64>> {
        let id: Option<i64> = self
            .db
            .query_row(
                "SELECT id FROM inbox WHERE update_id = ?1",
                params![update_id],
                |r| r.get(0),
            )
            .optional()?;
        Ok(id)
    }

    fn log_task_event(
        &self,
        task_id: i64,
        chat_id: i64,
        update_id: Option<i64>,
        phase: &str,
        status: &str,
        message: Option<&str>,
        error: Option<&str>,
    ) -> Result<()> {
        let msg = message.map(|v| truncate_for_telegram(v, 1000));
        let err = error.map(|v| truncate_for_telegram(v, 1000));
        self.db.execute(
            "INSERT INTO task_audit
             (task_id, chat_id, update_id, phase, status, message, error, run_id, worker_instance_id)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
            params![
                task_id,
                chat_id,
                update_id,
                phase,
                status,
                msg,
                err,
                self.config.run_id,
                self.config.worker_instance_id
            ],
        )?;
        Ok(())
    }

    fn has_runnable_tasks(&self) -> Result<bool> {
        let exists: Option<i64> = self
            .db
            .query_row(
                "SELECT 1 FROM inbox WHERE status IN ('queued', 'failed') ORDER BY id LIMIT 1",
                [],
                |r| r.get(0),
            )
            .optional()?;
        Ok(exists.is_some())
    }

    fn claim_next_task(&mut self) -> Result<Option<QueueTask>> {
        let tx = self
            .db
            .transaction_with_behavior(TransactionBehavior::Immediate)?;

        let task: Option<QueueTask> = tx
            .query_row(
                "SELECT id, chat_id, update_id, text
                 FROM inbox
                 WHERE status IN ('queued', 'failed')
                 ORDER BY CASE status WHEN 'queued' THEN 0 ELSE 1 END, id
                 LIMIT 1",
                [],
                |r| {
                    Ok(QueueTask {
                        id: r.get(0)?,
                        chat_id: r.get(1)?,
                        update_id: r.get(2)?,
                        text: r.get(3)?,
                    })
                },
            )
            .optional()?;

        if let Some(t) = &task {
            tx.execute(
                "UPDATE inbox
                 SET status = 'in_progress',
                     attempts = attempts + 1,
                     locked_at = unixepoch(),
                     error = NULL,
                     updated_at = unixepoch()
                 WHERE id = ?1",
                params![t.id],
            )?;
        }

        tx.commit()?;
        Ok(task)
    }

    fn mark_task_done(&self, task_id: i64) -> Result<()> {
        self.db.execute(
            "UPDATE inbox
             SET status = 'done', updated_at = unixepoch(), error = NULL
             WHERE id = ?1",
            params![task_id],
        )?;
        Ok(())
    }

    fn mark_task_failed(&self, task_id: i64, error: &str) -> Result<()> {
        self.db.execute(
            "UPDATE inbox
             SET status = 'failed', updated_at = unixepoch(), error = ?2
             WHERE id = ?1",
            params![task_id, truncate_for_telegram(error, 1000)],
        )?;
        Ok(())
    }

    async fn bootstrap_offset(&self, pending_window_seconds: u64, pending_max_messages: usize) -> Result<i64> {
        let updates = self.get_updates(0, 0).await?;
        if updates.is_empty() {
            return Ok(0);
        }

        let now = current_unix_timestamp();
        let cutoff = now.saturating_sub(pending_window_seconds as i64);

        let mut in_window: Vec<&Update> = updates
            .iter()
            .filter(|u| u.message.as_ref().map(|m| m.date >= cutoff).unwrap_or(false))
            .collect();

        if in_window.is_empty() {
            return Ok(updates.last().map(|u| u.update_id + 1).unwrap_or(0));
        }

        if in_window.len() > pending_max_messages {
            let start = in_window.len() - pending_max_messages;
            in_window = in_window[start..].to_vec();
        }

        Ok(in_window.first().map(|u| u.update_id).unwrap_or(0))
    }

    async fn get_updates(&self, offset: i64, timeout: u64) -> Result<Vec<Update>> {
        let response = self
            .http
            .get(format!("{}/getUpdates", self.config.telegram_api_base))
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
            .post(format!("{}/sendMessage", self.config.telegram_api_base))
            .json(&req)
            .send()
            .await
            .context("telegram sendMessage request failed")?;
        Ok(())
    }

    async fn call_openai(&self, messages: Vec<OpenAIMessage>) -> Result<String> {
        let req = OpenAIChatCompletionsRequest {
            model: self.config.openai_model.clone(),
            messages,
            temperature: Some(0.2),
        };

        let resp = self
            .http
            .post(&self.config.openai_chat_completions_url)
            .bearer_auth(&self.config.openai_api_key)
            .json(&req)
            .send()
            .await
            .context("openai request failed")?;

        let status = resp.status();
        let body = resp.text().await.context("failed reading openai response")?;

        if !status.is_success() {
            bail!(
                "openai non-success status={} body={}",
                status,
                truncate_for_telegram(&body, 400)
            );
        }

        let parsed: OpenAIChatCompletionsResponse = serde_json::from_str(&body).with_context(|| {
            format!(
                "failed to parse openai response: {}",
                truncate_for_telegram(&body, 400)
            )
        })?;

        let content = parsed
            .choices
            .first()
            .map(|c| c.message.content.trim().to_string())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| "(empty model response)".to_string());

        Ok(content)
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

#[derive(Serialize)]
struct SendMessageRequest<'a> {
    chat_id: i64,
    text: &'a str,
}

#[derive(Debug, Serialize)]
struct OpenAIMessage {
    role: String,
    content: String,
}

#[derive(Debug, Serialize)]
struct OpenAIChatCompletionsRequest {
    model: String,
    messages: Vec<OpenAIMessage>,
    #[serde(skip_serializing_if = "Option::is_none")]
    temperature: Option<f32>,
}

#[derive(Debug, Deserialize)]
struct OpenAIChatCompletionsResponse {
    choices: Vec<OpenAIChoice>,
}

#[derive(Debug, Deserialize)]
struct OpenAIChoice {
    message: OpenAIMessageResponse,
}

#[derive(Debug, Deserialize)]
struct OpenAIMessageResponse {
    content: String,
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

    let mut app = App::new(config.clone(), db)?;
    app.init_db()?;

    let had_saved_offset = if let Some(saved) = app.load_offset()? {
        config.offset = saved;
        true
    } else {
        false
    };

    if config.drop_pending && !had_saved_offset {
        config.offset = app
            .bootstrap_offset(config.pending_window_seconds, config.pending_max_messages)
            .await?;
        app.save_offset(config.offset)?;
    }

    println!(
        "worker running id={} run_id={} model={} pending_window_seconds={} pending_max_messages={} had_saved_offset={}",
        config.worker_instance_id,
        config.run_id,
        config.openai_model,
        config.pending_window_seconds,
        config.pending_max_messages,
        had_saved_offset
    );

    let mut handled_count: u64 = 0;

    loop {
        let poll_timeout = if app.has_runnable_tasks()? {
            0
        } else {
            config.timeout
        };

        let updates = match app.get_updates(config.offset, poll_timeout).await {
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
            match app.enqueue_message(update.update_id, chat_id, &text, message.date) {
                Ok(inserted) => {
                    if inserted {
                        if let Ok(Some(task_id)) = app.get_task_id_by_update_id(update.update_id) {
                            let _ = app.log_task_event(
                                task_id,
                                chat_id,
                                Some(update.update_id),
                                "ingress",
                                "info",
                                Some(&text),
                                None,
                            );
                            let _ = app.log_task_event(
                                task_id,
                                chat_id,
                                Some(update.update_id),
                                "queued",
                                "ok",
                                None,
                                None,
                            );
                        }
                    }
                }
                Err(err) => {
                    eprintln!("enqueue error update_id={}: {err:#}", update.update_id);
                }
            }
        }

        let Some(task) = app.claim_next_task()? else {
            sleep(Duration::from_secs(config.sleep_seconds)).await;
            continue;
        };

        handled_count += 1;
        println!(
            "process task_id={} chat_id={} text={}",
            task.id,
            task.chat_id,
            truncate_for_telegram(&task.text, 200)
        );
        let _ = app.log_task_event(
            task.id,
            task.chat_id,
            Some(task.update_id),
            "claimed",
            "ok",
            None,
            None,
        );

        let result: Result<()> = async {
            let messages = app.build_messages(task.chat_id, &task.text)?;
            let _ = app.log_task_event(
                task.id,
                task.chat_id,
                Some(task.update_id),
                "model_request",
                "info",
                None,
                None,
            );
            let reply = app.call_openai(messages).await?;
            let _ = app.log_task_event(
                task.id,
                task.chat_id,
                Some(task.update_id),
                "model_response",
                "ok",
                Some(&reply),
                None,
            );
            app.send_message(task.chat_id, &reply).await?;
            let _ = app.log_task_event(
                task.id,
                task.chat_id,
                Some(task.update_id),
                "reply_sent",
                "ok",
                None,
                None,
            );
            app.append_history(task.chat_id, "user", &task.text)?;
            app.append_history(task.chat_id, "assistant", &reply)?;
            Ok(())
        }
        .await;

        match result {
            Ok(_) => {
                let _ = app.mark_task_done(task.id);
                let _ = app.log_task_event(
                    task.id,
                    task.chat_id,
                    Some(task.update_id),
                    "task_done",
                    "ok",
                    None,
                    None,
                );
            }
            Err(err) => {
                let msg = format!("{}", err);
                let _ = app.mark_task_failed(task.id, &msg);
                let _ = app.log_task_event(
                    task.id,
                    task.chat_id,
                    Some(task.update_id),
                    "task_failed",
                    "failed",
                    None,
                    Some(&msg),
                );
                let notify = format!("任务处理失败：{}", truncate_for_telegram(&msg, 600));
                if let Err(send_err) = app.send_message(task.chat_id, &notify).await {
                    eprintln!(
                        "task {} failed to notify chat_id={}: {}",
                        task.id, task.chat_id, send_err
                    );
                }
                eprintln!("task {} failed: {}", task.id, msg);
            }
        }

        if config.suicide_every > 0 && handled_count % config.suicide_every == 0 {
            eprintln!(
                "worker id={} handled {} messages; exiting intentionally",
                config.worker_instance_id, handled_count
            );
            std::process::exit(17);
        }
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
