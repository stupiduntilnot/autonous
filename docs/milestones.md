# Milestones

## [DONE] Milestone 0 — Basic Structure

### Goal
实现最小可用的 MVP：`supervisor + worker + Telegram + OpenAI API`

### Deliverables
- [DONE] `Supervisor` 功能：
  - [DONE] 启动 `worker`
  - [DONE] 监控 `worker`
  - [DONE] 重启 `worker`
  - [DONE] 记录 `worker version/build id`
- [DONE] `Worker` 功能：
  - [DONE] 拉取 Telegram 消息
  - [DONE] 调用 OpenAI API
  - [DONE] 向 Telegram 发送 Model 回复
- [DONE] 安全要求：
  - [DONE] Secrets（如 API tokens）必须通过 ENVIRONMENT VARIABLES 注入
  - [DONE] 开发阶段 secrets 仅注入 Docker 容器，禁止硬编码到 Dockerfile 或启动脚本

## [DONE] Milestone 1 — Observability

### Goal
确保每次运行都可追踪、可审阅、可解释。

### Deliverables
- [DONE] 统一事件日志（`events` table）：
  - [DONE] 扁平存储 + 基于 `parent_id` 的层级重建
  - [DONE] 基础设施事件：process lifecycle、worker spawn/exit、crash loop、rollback
  - [DONE] Agent 执行事件（对齐 pi-mono 的 3 层模型）：Agent -> Turn -> ToolCall
- [DONE] `Model Adapter` 接口：
  - [DONE] 统一 `CompletionResponse`（含 `input_tokens`、`output_tokens`）
  - [DONE] token usage 来自 provider API 响应（而非 tokenizer 估算）
- [DONE] 基于现有数据的状态派生：
  - [DONE] `telegram_offset` 来自 `inbox` table
  - [DONE] `current_good_rev` 来自 `revision.promoted` event
  - [DONE] `worker_instance_seq` 来自 `worker.spawned` event 计数

详细设计见 [milestone-1.md](./milestone-1.md)。

## Milestone 2 — Context Subsystem MVP

### Goal
实现最小可用的 `Context Subsystem MVP`，并保持清晰的 interfaces abstraction。

### Deliverables
- 三个独立接口：`ContextProvider`、`ContextCompressor`、`PromptAssembler`
- Provider-agnostic 的 `context.Message` 类型（不依赖任何 LLM provider 类型）
- Naive 实现：
  - `provider`: 从 SQLite 读取最近 N 条消息
  - `compressor`: 保留最多 N 条消息
  - `assembler`: `system + history + user message`
- 压缩行为写入 `events` table 以保证 observability

详细设计见 [milestone-2.md](./milestone-2.md)。

## [DONE] Milestone 3 — Basic Control Plane

### Goal
防止 runaway、预算失控和无限循环。

### Deliverables
- [DONE] Run limits: `max_turns`, `max_wall_time`, `max_tokens`（当前 `max_tokens` 使用内置默认值）
- [DONE] Error policy: bounded retries + exponential backoff + circuit breaker
- [DONE] Progress checks: no-progress detection + `progress.stalled` event
- [DONE] Instruction source abstraction: `Commander`（`telegram` + `dummy`）
- [DONE] Model provider abstraction: `ModelProvider`（`openai` + `dummy`）
- [DONE] 同一 binary 运行时选择：`AUTONOUS_MODEL_PROVIDER` / `AUTONOUS_COMMANDER`
- [DONE] 可观测性：worker `process.started` payload 记录 `provider/source`
- [DONE] Failure-injection tests:
  - [DONE] unit/integration tests（`internal/control` + `cmd/worker`）
  - [DONE] dummy E2E script（`scripts/e2e_m3_dummy.sh`）

详细设计见 [milestone-3.md](./milestone-3.md)。

## Milestone 4 — Tool Subsystem

### Goal
建立最小、正交且支持 bootstrapping 的 toolset。

### Deliverables
- `Tool registry`，包含：
  - 严格输入 schema（typed structs / JSON schema）
  - 严格输出 envelope（stdout/stderr、truncated flags、exit code）
  - timeout、output size cap、必要时分页
- `Tool safety policy`：tool allowlist；禁止 two-phase writes
- 初始 atomic tools（无需用户审批）：
  - phase 1：仅 `ls`（先打通机制与 E2E）
  - phase 2：`find`、`grep`、`read`、`write`、`edit`、`bash`（逐个工具、逐个任务推进）

## [DONE] Milestone 5 — Self-Update Transaction

### Goal
实现安全的 self-updates。

### Deliverables
- 升级流水线：`generate patch -> build -> test/self-check -> stage artifact -> approve -> deploy`
- `Artifact management`：存储 build artifacts 与 metadata（SHA、build time、tests passed）
- `Rollback`：`supervisor` 保留 last-known-good worker（N-1）并在失败时自动回退
- 手动回滚命令：`rollback <tx_id>`（direct command path，不走 LLM）

详细设计见 [milestone-5.md](./milestone-5.md)。

## [DONE] Milestone 6 — 配置目录与系统提示符

### Goal
建立标准化用户配置目录（XDG-compliant），将 system prompt 外部化为可读写的 `AUTONOUS.md` 文件，使 Agent 能够在运行时加载自定义指令并自我修改行为。

### Deliverables
- 用户配置目录：`$HOME/.config/autonous`（XDG 标准，支持 `$XDG_CONFIG_HOME` 与 `AUTONOUS_CONFIG_DIR` 覆盖）
- System prompt 外部化：Worker 启动时加载 `$AUTONOUS_CONFIG_DIR/AUTONOUS.md`，不存在则 fallback 到内置默认值
- Agent 自我修改：Agent 可通过 M4 `read`/`write`/`edit` 工具修改 `AUTONOUS.md`，下次启动生效

详细设计见 [milestone-6.md](./milestone-6.md)。

## Milestone 7 — Bootstrap Workflow (Telegram-first, Extensible Skills)

### Goal
完成 bootstrap：Agent 仅通过 Telegram 即可完成端到端 feature 交付，并通过 M5 update pipeline 发布；外部系统集成（如 GitHub）通过可选 skill 扩展。

Milestone 7 完成后，bootstrap 视为完成。

### Deliverables
- Telegram 需求入口：用户在 Telegram 提交 feature request
- 设计收敛 workflow（核心能力）：
  - Agent 在会话与事件中维护设计版本与决策记录
  - 多轮讨论后进入 design fixed
- 容器内实现 workflow：
  - Agent 通过 model API + tools 实现功能
  - 本地 build/test 通过 gate
  - commit 并 push 到远端仓库
- 发布 workflow：
  - Agent 为新 artifact 触发 update transaction
  - Telegram 展示带按钮的 approval message
  - approve/reject 后，Agent 报告最终状态（promoted/failed/rolled_back）
- 治理规则（M7 生效）：
  - 后续实现流程必须走 Telegram 驱动的标准 workflow（不再使用 ad-hoc 本地改动）
  - GitHub/其他平台集成必须以 skill 形式接入，不得成为核心强依赖

详细设计见 [milestone-7.md](./milestone-7.md)。

---

## TODO

待所有已定义 milestone 完成后，整理为新的 milestone：

- 单条消息字符截断：`Compressor` 对超长单条消息截断
- Token 预算压缩：`Compressor` 基于 `chars/4` 估算做 token 级裁剪
- LLM 摘要压缩：`Compressor` 调用 LLM 对旧消息生成摘要
- Tool output 处理：`Compressor` 识别 tool call 结果并单独截断/跳过
- 多 provider 支持：各 LLM provider 实现自己的 adapter
- 语义检索：Provider 基于向量相似度检索相关历史，而非简单时间窗口
- Milestone 3 后续可配置化（当前先使用内置默认值）：
  - `AUTONOUS_CONTROL_MAX_TOKENS`
  - `AUTONOUS_CONTROL_RETRY_BASE_SECONDS`
  - `AUTONOUS_CONTROL_RETRY_MAX_SECONDS`
  - `AUTONOUS_CONTROL_CIRCUIT_THRESHOLD`
  - `AUTONOUS_CONTROL_CIRCUIT_COOLDOWN_SECONDS`
  - `AUTONOUS_CONTROL_NO_PROGRESS_K`
- Milestone 3 后续验证（在多轮 agent loop 落地后）：
  - 多轮场景下 `max_turns` 语义与事件完整性回归
- Tool allowlist 后续能力：
  - 支持将用户批准的路径动态加入 `AUTONOUS_TOOL_ALLOWED_ROOTS`（含审计与过期策略）
- Tool safety 后续能力：
  - 引入 `risk_tier` 分层策略（Read/Write/Exec/Network）并接入审计与策略控制
- Tool reliability 后续能力：
  - 引入 dead-letter 机制（达到重试上限后进入隔离队列并支持人工/自动重放）
- Tool native implementation
  - 有些tool用Golang原生实现比调用cli工具好。应该替换现在的实现。
