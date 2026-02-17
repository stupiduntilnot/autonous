# Milestone 3 计划: Basic Control Plane

## 当前实现状态（2026-02-17）

### 已完成

- Run limits：`max_turns`、`max_wall_time`、`max_tokens`（`max_tokens` 当前为内置默认值）
- Error policy：bounded retries + exponential backoff + circuit breaker
- Progress checks：`no-progress` 检测与 `progress.stalled` 事件
- 抽象边界：
  - `Commander`：`telegram` + `dummy`
  - `ModelProvider`：`openai` + `dummy`
- 同一 binary 运行时选择：
  - `AUTONOUS_MODEL_PROVIDER=openai|dummy`
  - `AUTONOUS_COMMANDER=telegram|dummy`
- 可观测性：
  - worker `process.started` payload 记录 `provider/source`
  - 关键控制事件已落库：`control.limit_reached`、`retry.*`、`circuit.*`、`progress.stalled`
- 测试：
  - `internal/control` 单元测试（含 circuit 状态转换）
  - `cmd/worker` 集成测试（limit/retry/no-progress）
  - `scripts/e2e_m3_dummy.sh` 覆盖 failure injection 场景

### 剩余 Gap

- `max_tokens`、`circuit_threshold/cooldown`、`no_progress_k` 仍为内置值，暂未开放 ENV 配置。
- 当前仍以单轮 turn 为主路径；多轮 loop 下的 `max_turns` 行为需在后续 milestone 持续验证。

## 核心目标

在不引入复杂编排系统的前提下，为 Agent 增加最小可用的控制平面能力，避免：

- run-away 执行（无限尝试/无限循环）
- budget 失控（wall time、token 成本不可控）
- 无进展重试（同一错误反复发生却持续消耗资源）

## 核心原则

- **先 hard limits，再智能策略**: 先用明确阈值（`max_turns`、`max_wall_time`、`max_tokens`）兜底，再逐步演进。
- **不引入新表**: 继续只使用 `events`、`inbox`、`history` 三表；控制状态优先通过现有字段与事件派生。
- **失败可解释**: 每一次被限流、重试、熔断、停止都必须落事件，便于事后审计。
- **与当前架构兼容**: 保持 Supervisor/Worker 分层，Control Plane 在 Worker 内执行，不让 Supervisor 感知 prompt 细节。

## 范围与非目标

### Milestone 3 范围

- Run limits：`max_turns`、`max_wall_time`、`max_tokens`（或 `max_cost` 二选一，MVP 优先 tokens）
- Error policy：有限重试 + exponential backoff + circuit breaker
- Progress checks：`K` 次迭代无状态变化则停止
- 指令来源抽象：定义 `Commander` 接口；本 milestone 仅落地 `Telegram` 实现
- Model provider 抽象：定义 `ModelProvider` 接口；本 milestone 仅落地 `OpenAI` 实现
- 提供可注入失败的测试实现：`DummyCommander`、`DummyProvider`

### 非目标（留到后续 milestone）

- 分布式调度、跨 worker 全局配额
- 精确计费（provider 实时价格表驱动的 `max_cost`）
- 复杂策略学习（如动态阈值、自动 policy 优化）
- 新增第二个真实控制入口（如 Slack、HTTP API）或第二个真实 model provider

## 抽象边界（本 milestone 新增）

### 1) 指令来源抽象

定义统一接口 `Commander`，Worker 只依赖该接口，不直接依赖 Telegram client 具体类型。

MVP 要求：
- 生产实现：`TelegramCommander`
- 测试实现：`DummyCommander`（支持脚本化注入 poll/send 失败）

### 2) Model Provider 抽象

定义统一接口 `ModelProvider`，以 `context.Message` 和 `CompletionResponse` 作为边界模型。

MVP 要求：
- 生产实现：`OpenAIProvider`
- 测试实现：`DummyProvider`（支持脚本化注入 timeout/429/5xx/empty response 等）

约束：
- `Dummy*` 仅用于测试，不作为默认 runtime provider。
- 使用同一 binary，运行时通过环境变量选择实现：
  - `AUTONOUS_MODEL_PROVIDER=openai|dummy`
  - `AUTONOUS_COMMANDER=telegram|dummy`
- 默认值应为生产实现（`openai + telegram`），`dummy` 仅在测试场景显式启用。

## 控制模型

### 1) Run Limits

每个 task（Agent run）维护一个运行预算 `RunBudget`：

- `max_turns`: 单任务最多允许的迭代次数
- `max_wall_time`: 从 `agent.started` 到当前时刻的最大时长
- `max_tokens`: 单任务累计 token 上限（累计 `turn.completed.input_tokens + output_tokens`）

术语对齐（与 pi-mono）：
- 本文中的 `turn` 对齐为 pi-mono 的 `turn_start` / `turn_end`。
- 本文中的“单任务”对齐为一次 `agent run`（从 `agent.started` 到 `agent.completed|agent.failed` 的完整生命周期）。
- 本文统一使用 `max_turns`；旧命名 `max_steps` 仅作为兼容别名保留一段时间。

触发任一上限即停止当前 run，标记失败并记录事件。

说明：
- 当前系统以单轮处理为主，`max_turns` 在 MVP 中默认可为 `1`；接口仍按多轮迭代设计，便于 Milestone 4/5 扩展。
- `max_cost` 暂不作为 MVP 主路径；后续在价格模型稳定后可替换或并行于 `max_tokens`。

### 2) Error Policy

#### 2.1 Bounded Retries + Exponential Backoff

- 每个 task 最大重试次数 `max_retries`。
- 第 `n` 次失败后的回退时间：
  - `backoff = min(base * 2^(n-1), max_backoff)`
- 在 backoff 窗口内，task 不应被再次 claim。

MVP 实现约束：
- 复用 `inbox.attempts`、`inbox.updated_at`、`inbox.status`，不新增表。
- `claimNextTask` 对 `failed` 任务按 backoff 条件过滤。

#### 2.2 Circuit Breaker

- 若连续出现同类错误达到阈值 `circuit_threshold`，打开熔断器。
- 熔断打开期间暂停处理新任务 `circuit_cooldown_seconds`，并记录事件。
- 冷却结束后自动 half-open，允许单个任务探测；成功则关闭熔断，失败则重新打开。

错误“同类”定义（MVP）：
- 按错误分类（例如 `telegram_api`, `openai_api`, `db`, `tool_exec`, `unknown`）而非完整 error string，避免文本细节导致无法聚合。

### 3) Progress Checks

定义 `no-progress`：连续 `K` 次迭代，状态指纹未变化。

状态指纹（MVP）建议包含：
- `history_count_before_turn`
- `compressed_count`
- `last_error_class`（若失败）
- `last_reply_hash`（若成功生成回复）

`last_error_class` 指错误分类（不是错误原文）。MVP 枚举建议：
- `command_source_api`（拉取/发送指令失败，如 Telegram API 错误）
- `provider_api`（模型 provider API 错误，如 429/5xx/timeout）
- `db`
- `tool_exec`
- `validation`
- `unknown`

若连续 `K` 次指纹不变，判定为 `progress.stalled`，停止 run 并记录事件。

## 事件设计（新增）

以下事件继续写入 `events` 表：

| event_type | 层级 | payload |
|---|---|---|
| `control.limit_reached` | Agent | `task_id`, `limit_type`, `value`, `threshold` |
| `retry.scheduled` | Agent | `task_id`, `attempt`, `backoff_seconds`, `error_class` |
| `retry.exhausted` | Agent | `task_id`, `attempts`, `last_error_class` |
| `circuit.opened` | Process/Worker | `error_class`, `threshold`, `cooldown_seconds` |
| `circuit.half_open` | Process/Worker | `error_class` |
| `circuit.closed` | Process/Worker | `recovered` |
| `progress.stalled` | Agent | `task_id`, `k`, `state_fingerprint` |

命名保持点分层风格，与 Milestone 1 事件体系一致。

另外，worker 的 `process.started` payload 必须记录当前实现选择：
- `provider`（`openai` 或 `dummy`）
- `source`（`telegram` 或 `dummy`）

## 配置（新增 ENV）

建议新增以下 Worker 配置项（均提供默认值）：

- `AUTONOUS_CONTROL_MAX_TURNS`（默认 `1`）
- `AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS`（默认 `120`）
- `AUTONOUS_CONTROL_MAX_RETRIES`（默认 `3`）
- `AUTONOUS_MODEL_PROVIDER`（默认 `openai`）
- `AUTONOUS_COMMANDER`（默认 `telegram`）

命名策略：
- 新增控制平面配置统一使用 `AUTONOUS_` 前缀（例如 `AUTONOUS_CONTROL_MAX_TURNS`）。
- 历史变量（`TG_*`、`OPENAI_*`、`WORKER_*`）在 Milestone 3 保持兼容，不在本 milestone 强制重命名。
- 控制平面变量采用双读迁移：优先读新变量（例如 `AUTONOUS_CONTROL_MAX_TURNS`），回退旧变量（例如 `AUTONOUS_CONTROL_MAX_STEPS`）。
- provider/source 选择变量也使用 `AUTONOUS_` 前缀：`AUTONOUS_MODEL_PROVIDER`、`AUTONOUS_COMMANDER`。
- 其余控制项（`MAX_TOKENS`、`RETRY_BASE/MAX`、`CIRCUIT_*`、`NO_PROGRESS_K`）在本 milestone 先使用内置默认值，不暴露为 env。

## Worker 集成点

### 1) 入口

在 `cmd/worker/main.go` 初始化 `ControlPolicy` 与 `Controller`，并通过依赖注入传入：

- `Commander`（当前注入 `TelegramCommander`）
- `ModelProvider`（当前注入 `OpenAIProvider`）

Go 采用显式依赖注入（constructor/parameter injection），不依赖 Spring 风格容器。示例：

```go
type WorkerDeps struct {
    Commander Commander
    Provider      ModelProvider
    Controller    *control.Controller
}

func NewWorker(deps WorkerDeps) *Worker { ... }

// production wiring
worker := NewWorker(WorkerDeps{
    Commander: telegram.NewCommander(...),
    Provider:      openai.NewProvider(...),
    Controller:    control.NewController(policy),
})

// test wiring
worker := NewWorker(WorkerDeps{
    Commander: dummy.NewCommander(script),
    Provider:      dummy.NewProvider(script),
    Controller:    control.NewController(testPolicy),
})
```

### 2) 处理流程

`processTask` 内增加控制循环（即使 MVP 仅 1 step 也保留结构）：

1. 检查 circuit 状态（是否允许执行）
2. 检查 run limits（turns / wall time / tokens）
3. 执行 turn（LLM + reply）
4. 评估 progress
5. 成功则完成；失败则根据 retry policy 决定重试或终止

失败分类（用于 retry/circuit）应来自统一错误分类器，不直接依赖 provider/IM 原始错误字符串。

### 3) 队列 claim

调整 `claimNextTask`：`failed` 任务只有在 backoff 到期后可再次 claim。

## Failure Injection 设计

为确保三类失败控制都可稳定测试（`max_turns`、`max_wall_time`、`max_retries`），`DummyCommander` 与 `DummyProvider` 采用“脚本驱动（scripted）”设计。

### 1) 核心机制

- 两个 dummy 都维护一个按调用顺序消费的 action 列表（queue）。
- 每次方法调用消费一个 action；若脚本耗尽，走默认 action（通常为 success）。
- action 支持最小集合：
  - `ok`
  - `err:<class>`（例如 `err:provider_api`、`err:command_source_api`）
  - `sleep:<ms>`（用于触发 wall time）

### 2) 覆盖三类控制的测试映射

- `AUTONOUS_CONTROL_MAX_TURNS`
  - 让 `DummyProvider` 连续返回 `ok` 且包含“需要继续”的回复（或触发多轮路径），直到命中 `max_turns`。
- `AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS`
  - 在 `DummyProvider` 或 `DummyCommander` 动作中注入 `sleep:<ms>`，超过 wall-time 阈值。
- `AUTONOUS_CONTROL_MAX_RETRIES`
  - 注入连续 `err:*`（如 `err:provider_api`），验证重试计数达到上限后停止并写 `retry.exhausted`。

### 3) Unit Test 注入方式

- 不依赖 env，直接在测试里构造 dummy 并传入脚本：
  - `dummy.NewProvider([]Action{...})`
  - `dummy.NewCommander([]Action{...})`
- 重点验证纯逻辑与状态迁移（limit/retry/circuit/no-progress）。

### 4) E2E 注入方式

- 通过 env 选择 dummy 实现：
  - `AUTONOUS_MODEL_PROVIDER=dummy`
  - `AUTONOUS_COMMANDER=dummy`
- 再通过测试专用脚本 env 注入动作序列（仅 dummy 读取）：
  - `AUTONOUS_DUMMY_PROVIDER_SCRIPT=err:provider_api,err:provider_api,ok`
  - `AUTONOUS_DUMMY_COMMANDER_SCRIPT=ok,sleep:2000,ok`
- 脚本格式建议使用逗号分隔，解析失败时应在启动阶段报错并退出（避免假通过）。

## 测试计划

### 单元测试

- `internal/control/policy_test.go`
  - limit 判定（turns/wall_time/tokens）
  - backoff 计算
  - no-progress 判定
- `internal/control/circuit_test.go`
  - closed -> open -> half-open -> closed 转换

### 集成测试

- `cmd/worker` 级别：
  - 达到 `max_retries` 后任务停止且写 `retry.exhausted`
  - 命中 `max_tokens`/`max_wall_time` 后写 `control.limit_reached`
  - 熔断开启期间不 claim 新任务
  - 使用 `DummyCommander` 注入 poll/send 失败，验证 error class、retry 与 circuit 行为
  - 使用 `DummyProvider` 注入 429/timeout/5xx，验证 error class、retry 与 circuit 行为
  - 使用脚本化 `sleep` + 连续错误，分别覆盖 `max_wall_time` 与 `max_retries`

### E2E 测试

- 构造 OpenAI/Telegram 失败场景，验证 backoff 与 circuit 行为
- 构造重复无变化场景，验证 `progress.stalled`

## 任务分解

### 1. 控制策略与数据结构

- [DONE] 新建 `internal/control/` 包，提供 `Policy`、`CircuitBreaker` 等控制原语。
- [DONE] 实现 limit/backoff/no-progress 的纯逻辑函数。

### 2. 抽象与适配器

- [DONE] 定义 `Commander` 接口，并让 Telegram 实现该接口。
- [DONE] 定义 `ModelProvider` 接口，并让 OpenAI 实现该接口。
- [DONE] 提供 `DummyCommander`（failure injectable）用于测试。
- [DONE] 提供 `DummyProvider`（failure injectable）用于测试。
- [DONE] 使用同一 binary，按 `AUTONOUS_MODEL_PROVIDER/AUTONOUS_COMMANDER` 在运行时选择实现。

### 3. Worker 集成

- [DONE] 在 `cmd/worker/main.go` 注入控制策略 + `Commander` + `ModelProvider`。
- [DONE] 在 `processTask` 加入 run limits 检查与 no-progress 检测。
- [DONE] 调整 `claimNextTask` 支持 failed task backoff 过滤。

### 4. 事件与可观测性

- [DONE] 在 `internal/db/db.go` 增加新增事件常量。
- [DONE] 在关键路径写入 `control.limit_reached`、`retry.scheduled`、`retry.exhausted`、`circuit.*`、`progress.stalled`。
- [DONE] 在 worker 的 `process.started` payload 中记录当前 `provider/source`。

### 5. 配置

- [DONE] 在 `internal/config/config.go` 增加最小配置读取：`AUTONOUS_CONTROL_MAX_TURNS`、`AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS`、`AUTONOUS_CONTROL_MAX_RETRIES`、`AUTONOUS_MODEL_PROVIDER`、`AUTONOUS_COMMANDER`。
- [PARTIAL] 其余控制参数（如 `max_tokens`、circuit 阈值、no-progress `K`）仍使用内置默认值。

### 6. 测试

- [DONE] 新增 `internal/control/*_test.go`（含 `circuit_test.go`）。
- [DONE] 补充 worker 集成测试覆盖 limit/retry/no-progress。
- [DONE] 新增 dummy failure-injection E2E 脚本：`scripts/e2e_m3_dummy.sh`。
