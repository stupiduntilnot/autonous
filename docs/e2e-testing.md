# E2E 测试指南

## 概述

本文档描述如何对 autonous Agent 进行端到端测试。核心原则：

1. **Docker 容器只启动一次**，所有开发测试在其中进行
2. 从宿主机通过 `scripts/send_test_message.sh` 发送 Telegram 消息来验证功能
3. 只有在万不得已时才销毁容器重建
4. 最终目标：Milestone 完成后，Agent 通过 Telegram 对话自我更新，不再依赖宿主机

## 环境变量

### 必需（存放在 `~/.env`）

| 变量 | 说明 |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Telegram Bot API token |
| `OPENAI_API_KEY` | OpenAI API key |
| `TG_API_ID` | Telegram User API ID（测试脚本用） |
| `TG_API_HASH` | Telegram User API Hash（测试脚本用） |

### 可选（容器启动时通过 `-e` 传入）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `OPENAI_MODEL` | `gpt-4o-mini` | 使用的模型 |
| `WORKER_SUICIDE_EVERY` | `0`（不退出） | Worker 每处理 N 条消息后自动退出，由 Supervisor 重启 |
| `TG_DROP_PENDING` | `true` | 启动时是否丢弃积压消息 |
| `TG_PENDING_WINDOW_SECONDS` | `600` | 保留多少秒内的积压消息 |
| `TG_TIMEOUT` | `30` | Telegram long poll 超时秒数 |
| `TG_HISTORY_WINDOW` | `12` | 对话上下文保留条数 |
| `WORKER_SYSTEM_PROMPT` | 内置默认 | Worker 的 system prompt |

## 操作流程

### 1. 构建 Docker image（仅在代码变更后执行）

```bash
docker build -t autonous-agent:dev .
```

### 2. 启动容器（只做一次）

```bash
docker run -d \
  --name autonous-agent \
  --restart unless-stopped \
  --env-file ~/.env \
  autonous-agent:dev
```

确认启动成功：

```bash
docker logs -f autonous-agent
```

应看到类似输出：

```
startup launching supervisor
[supervisor] running worker=/workspace/bin/worker
[supervisor] starting worker instance W000001
worker running id=W000001 ...
```

### 3. 发送测试消息

首次运行需要交互式登录 Telegram（输入手机号和验证码）：

```bash
scripts/send_test_message.sh '你好，请回复 OK'
```

Session 保存在 `~/.telethon_test_session`，后续无需再登录。

### 4. 查看 Agent 处理日志

```bash
docker logs --tail 20 autonous-agent
```

应看到：

```
process task_id=1 chat_id=... text=你好，请回复 OK
```

### 5. 验证回复

在 Telegram 中检查 `@autonous_bot` 的回复，或在日志中确认无 error。

## 日常开发循环

```
修改代码 → docker build → docker rm -f autonous-agent → docker run（同上） → 测试
```

**注意**：日常开发中应尽量避免重建容器。只有以下情况才需要：
- Go 代码有变更需要重新编译
- Dockerfile 本身有变更

## 容器管理

```bash
# 查看状态
docker ps --filter name=autonous-agent

# 查看日志（实时跟踪）
docker logs -f autonous-agent

# 进入容器调试
docker exec -it autonous-agent bash

# 万不得已：销毁重建
docker rm -f autonous-agent
```

## 未来愿景

当 Agent 具备自我更新能力后：
- 不再需要从宿主机修改代码
- 通过 Telegram 对话指示 Agent 实现新功能
- Agent 自行修改代码、替换 Worker binary、由 Supervisor 重启生效
