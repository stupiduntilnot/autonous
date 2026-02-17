# Milestone 0-3 验收记录（2026-02-17）

## 目标

从零重建环境后，验证 Milestone 0/1/2/3 的关键链路：

- M0: supervisor + worker + Telegram + OpenAI 主链路可用
- M1: 统一事件日志与层级关系完整
- M2: context pipeline 与 `context.assembled` 事件
- M3: failure control（retry/circuit/no-progress/max_wall_time）可验证

## 执行时间

- 本次回归执行时间：2026-02-17（本地）
- 关键 smoke 消息时间戳：`20260217-234119`

## 执行步骤与结果

### 1) 从零重建容器

命令：

```bash
scripts/redeploy_autonous.sh
```

结果：
- 容器 `autonous-agent` 成功重建并启动。
- 冷启动后 DB 初始状态：
  - `inbox=0`
  - `history=0`
  - `events=4`（supervisor/worker 启动相关）

### 2) M0~M2 真实链路 smoke（Telegram + OpenAI）

发送消息：

```bash
scripts/send_test_message.sh "ACPT-M0M3-20260217-234119, extract timestamp and answer in RFC3339 UTC"
```

数据库断言结果：
- `inbox` 最新任务：`status=done`，`attempts=1`
- `history` 有 user + assistant 两条
- 事件链完整：
  - `process.started`（supervisor）
  - `worker.spawned`
  - `process.started`（worker，payload 含 `provider=openai`、`source=telegram`）
  - `agent.started`
  - `context.assembled`
  - `turn.started`
  - `turn.completed`
  - `reply.sent`
  - `agent.completed`

### 3) M3 dummy failure-injection 回归

命令：

```bash
scripts/e2e_m3_dummy.sh
```

结果：全部场景通过

- `retry.exhausted` 场景：通过
- `max_wall_time -> control.limit_reached` 场景：通过
- `progress.stalled` 场景：通过
- `circuit.opened/half_open/closed` 场景：通过

## 结论

本次从零环境回归中，Milestone 0-3 关键验收路径均通过。
