# E2E 测试指南

## 前提

- `~/.env` 中包含 `TELEGRAM_BOT_TOKEN`、`OPENAI_API_KEY`、`TG_API_ID`、`TG_API_HASH`
- 首次运行测试脚本时需要交互式登录 Telegram（输入手机号和验证码），session 保存在 `~/.telethon_test_session`

## 步骤

### 1. 构建镜像并启动容器

```bash
docker build -t autonous-agent:dev .

docker run -d \
  --name autonous-agent \
  --restart unless-stopped \
  --env-file ~/.env \
  -v "$(pwd)":/workspace \
  autonous-agent:dev
```

注意：不要挂载 `/state`。数据库文件应仅存在于容器内（`/state/agent.db`）。

容器启动时 `startup.sh` 会自动编译 binary 并启动 Supervisor → Worker。

确认启动成功：

```bash
docker logs -f autonous-agent
```

应看到：

```
[supervisor] running worker=/workspace/bin/worker
[supervisor] starting worker instance W000001
worker running id=W000001 ...
```

### 2. 从宿主机发送测试消息

```bash
scripts/send_test_message.sh 'E2E-M3-T3-20260217-231629, extract timestamp from it and reply with parsed time'
```

注意：
- 不要只发送 `E2E-...` 这类纯标识字符串。
- 测试消息必须包含明确任务指令，便于验证回复是否“语义正确”。

### 3. 查看容器日志

```bash
docker logs --tail 20 autonous-agent
```

应看到：

```
process task_id=1 chat_id=... text=你好，请回复 OK
```

无 error 输出即表示 Telegram poll、OpenAI API 调用、回复发送均正常。

### 4. 在 Telegram 中验证

打开 Telegram，检查 `@autonous_bot` 的回复。
应验证回复内容与测试指令匹配（例如回复中包含正确解析出的时间戳），而不只是“任意文本回复”。

## Milestone 3 Dummy Failure Injection

用于验证控制平面失败处理（`retry/circuit/no-progress/max_wall_time`）：

```bash
scripts/e2e_m3_dummy.sh
```

该脚本会使用同一 `worker` binary，并通过 ENV 切换到 dummy 实现：
- `AUTONOUS_MODEL_PROVIDER=dummy`
- `AUTONOUS_COMMANDER=dummy`

脚本会分别构造失败场景并直接查询 SQLite 断言事件是否出现。

## 环境变量参考

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OPENAI_MODEL` | `gpt-4o-mini` | 使用的模型 |
| `WORKER_SUICIDE_EVERY` | `0` | Worker 每处理 N 条消息后自动退出（测试时可设为 `1`） |
| `TG_DROP_PENDING` | `true` | 启动时是否丢弃积压消息 |
| `TG_PENDING_WINDOW_SECONDS` | `600` | 保留多少秒内的积压消息（测试时建议设为 `10`） |
| `TG_TIMEOUT` | `30` | Telegram long poll 超时秒数 |
| `TG_HISTORY_WINDOW` | `12` | 对话上下文保留条数 |
