# M4 综合 E2E 设计: Multi-Tool Workflow

## 目标

验证一个真实、可重跑的端到端场景：Agent 在单个任务中协同使用多个 tools，完成“扫描目录中的数字并求和写回文件”。

本用例同时覆盖：

- 业务正确性（sum 结果正确）
- 工具链路正确性（tool calls 出现且可审计）
- 可重复执行（每次运行隔离，不受上次残留影响）

## 场景定义

### 输入数据集

每次测试创建唯一目录（随机后缀）：

- 根目录：`/workspace/e2e/multi-tools/<random>/`
- 文件示例：
  - `a.txt`: `hello 12 world`
  - `b.txt`: `foo 8 bar 30`
  - `c.txt`: `only letters here`
  - `nested/d.md`: `7 and 3`

预期总和：`12 + 8 + 30 + 7 + 3 = 60`

### Telegram 指令

本用例要求消息里显式包含输入目录与输出文件的绝对路径（路径由下面 shell 命令生成并打印）。

## 两轮执行策略（单进程）

- 仅使用同一个正在运行的 agent 进程，不重启容器、不切换 env。
- Round 1 在 prompt 中明确要求 `do not use bash tool`。
- Round 2 在 prompt 中明确要求 `you must use bash tool`，并且输入包含 Round 1 的输出文件。
- 不断言严格 tool 顺序，只断言关键工具覆盖和最终结果正确。
- 对齐 pi-mono：若中途 tool 失败，系统应把错误作为 tool result 回传模型并继续 loop，而不是直接任务失败。

## 断言策略

### 1) 业务断言

- Round 1:
  - 输出文件存在（`<root>/round1/sum.txt`）。
  - 输出内容为 `60`（允许末尾换行）。
  - assistant 最终回复仅为 `60`。
- Round 2:
  - round2 输入额外包含 round1 的输出文件。
  - 输出文件存在（`<root>/round2/sum.txt`）。
  - 输出内容为 `66`（`60 + 1 + 2 + 3`，见下文数据集）。
  - assistant 最终回复仅为 `66`。

### 2) 行为断言（events）

按本次测试目录（`ROOT/R1/R2`）对应任务，分两轮校验：

- Round 1:
  - 至少出现一次 `tool_call.started` / `tool_call.completed`。
  - 必需工具集合至少覆盖：`find`、`read`、`write`。
  - 若出现 `tool_call.failed`，后续仍应继续出现新的 `tool_call.started`（证明没有“失败即终止”）。
  - `bash` 若被调用，记录告警（不作为硬失败，避免模型非确定性导致 flaky）。
- Round 2:
  - 必须出现至少一次 `bash` 的 `tool_call.completed`。
  - 允许额外工具调用（`ls`/`find`/`grep`/`read`/`write`/`edit`），但不能影响最终正确性。

### 3) 失败时输出

脚本失败需打印：

- 本次 `ROOT` 路径
- 关键 DB 查询结果（`inbox/history/events`）
- 输出文件实际内容
- worker/supervisor 日志片段

## 可重跑设计

- 每次在容器内创建新的随机目录（`mktemp -d`）。
- 不依赖固定 DB 自增 ID，只用消息内容或时间窗筛选本次任务。
- 脚本退出时仅清理本次临时目录，不影响其他运行。

## 自动化入口

- 一键脚本：`scripts/e2e_m4_multi_tool_telegram.sh`
- 该脚本按本设计执行两轮测试并完成断言：
  - Round 1：禁用 `bash`（prompt 约束）
  - Round 2：强制 `bash`（prompt 约束）且输入包含 Round 1 输出

## Host 手工执行步骤（不写自动化脚本）

以下命令均在 host machine 执行。

### 0) 准备变量

按顺序逐条执行以下命令（每条都可直接 copy/paste）：

```bash
export DB_IN_CONTAINER="/state/agent.db"
```

```bash
export CONTAINER="autonous-agent"
```

```bash
export ROOT="$(docker exec "$CONTAINER" sh -lc 'mkdir -p /workspace/e2e/multi-tools && mktemp -d /workspace/e2e/multi-tools/mt-XXXXXX')"
```

```bash
export R1="${ROOT}/round1"
```

```bash
export R2="${ROOT}/round2"
```

```bash
echo "ROOT=$ROOT"
```

```bash
echo "R1=$R1"
```

```bash
echo "R2=$R2"
```

### 1) 准备 Round 1 数据

```bash
docker exec "$CONTAINER" mkdir -p "$R1/nested"
docker exec "$CONTAINER" sh -lc "cat > '$R1/a.txt' <<'EOF'
hello 12 world
EOF"
docker exec "$CONTAINER" sh -lc "cat > '$R1/b.txt' <<'EOF'
foo 8 bar 30
EOF"
docker exec "$CONTAINER" sh -lc "cat > '$R1/c.txt' <<'EOF'
only letters here
EOF"
docker exec "$CONTAINER" sh -lc "cat > '$R1/nested/d.md' <<'EOF'
7 and 3
EOF"
```

### 2) 发送 Round 1 Telegram 指令（禁用 bash）

```bash
MSG1="E2E-M4-MULTI-R1: scan ${R1}, find all numbers in files, sum them, write result to ${R1}/sum.txt, do not use bash tool, reply only the number."
echo "$MSG1"
scripts/send_test_message.sh "$MSG1"
```

### 3) 等待 Round 1 完成并校验

```bash
sleep 10
docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" \
  "SELECT id,status,substr(text,1,200) FROM inbox WHERE text LIKE '%${R1}%' ORDER BY id DESC LIMIT 1;"

docker exec "$CONTAINER" cat "${R1}/sum.txt"
docker exec "$CONTAINER" sh -lc "tr -d '\n\r ' < '${R1}/sum.txt'"
docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" \
  "SELECT id,role,substr(text,1,120) FROM history ORDER BY id DESC LIMIT 10;"

# Round1 tool calls（按需人工检查包含 find/read/write；bash 若出现记告警）
docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" \
  "SELECT id,event_type,substr(payload,1,220) FROM events WHERE event_type LIKE 'tool_call.%' ORDER BY id DESC LIMIT 80;"
```

Round 1 预期：

- `${R1}/sum.txt` 内容为 `60`
- 最终 assistant 回复为 `60`

### 4) 准备 Round 2 数据（包含 Round 1 输出）

```bash
docker exec "$CONTAINER" mkdir -p "$R2"
docker exec "$CONTAINER" sh -lc "cat > '$R2/e.txt' <<'EOF'
extra numbers: 1 2 3
EOF"
```

### 5) 发送 Round 2 Telegram 指令（强制 bash）

```bash
MSG2="E2E-M4-MULTI-R2: use ${R2}/e.txt and ${R1}/sum.txt, you must use bash tool to compute total sum, write result to ${R2}/sum.txt, and reply only the number."
echo "$MSG2"
scripts/send_test_message.sh "$MSG2"
```

### 6) 等待 Round 2 完成并校验

```bash
sleep 10
docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" \
  "SELECT id,status,substr(text,1,200) FROM inbox WHERE text LIKE '%${R2}%' ORDER BY id DESC LIMIT 1;"

docker exec "$CONTAINER" cat "${R2}/sum.txt"
docker exec "$CONTAINER" sh -lc "tr -d '\n\r ' < '${R2}/sum.txt'"
docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" \
  "SELECT id,role,substr(text,1,120) FROM history ORDER BY id DESC LIMIT 10;"

# Round2 tool calls（必须看到 bash 的 tool_call.completed）
docker exec "$CONTAINER" sqlite3 "$DB_IN_CONTAINER" \
  "SELECT id,event_type,substr(payload,1,220) FROM events WHERE event_type LIKE 'tool_call.%' ORDER BY id DESC LIMIT 120;"
```

Round 2 预期：

- `${R2}/sum.txt` 内容为 `66`
- 最终 assistant 回复为 `66`
- `events` 中出现 `bash` 的 `tool_call.completed`

### 7) （可选）清理本次测试目录

```bash
docker exec "$CONTAINER" rm -rf "$ROOT"
```
