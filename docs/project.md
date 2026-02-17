# Autonous 项目文档

最后更新: 2026-02-17

## 1. 项目定位
`autonous` 是一个通过 `Telegram` 远程驱动的自主编程系统（autonomous coding system），目前正在用 `Golang` 进行重写。

项目的核心目标是实现一个稳定、可观测、并且能够增量演进的自主系统。

## 2. 核心架构原则

### 2.1. 系统构成
- 系统被划分为 `supervisor` 和 `worker` 两个进程，以解耦进程生命周期管理和任务执行。
- 核心持久化层是一个单独的 `SQLite` 数据库。

### 2.2. Supervisor-Worker 关系
- **`Supervisor`** 是系统的稳定内核，仅负责 `worker` 的进程生命周期（启动、监控、重启）。
- **`Supervisor`** 必须保持中立和可验证。它**不会**读取、解析或存储任何来自用户或 `LLM` 的 `prompt` 或消息内容。
- **`Worker`** 负责所有的业务逻辑，包括 `Telegram` 通信、任务执行以及与外部 `API` 的交互。

### 2.3. 执行与状态
- **队列驱动执行 (Queue-Driven Execution)**: 所有传入的任务都被放入一个持久化的队列（`inbox` 表），并由一个单一消费者串行处理，以保证顺序性、可恢复性和可审计性。
- **持久化的事实来源 (Persistent Source of Truth)**: 所有关键状态（例如，对话历史、任务队列、事件日志）都必须持久化到 `SQLite` 数据库中，以确保系统能从崩溃中恢复。
- **可恢复的状态 (Recoverable State)**: Agent 的状态主要从持久化的数据库中派生，而不是从进程内存中。
- **可裁剪的上下文 (Trimable Context)**: 提供给 `LLM` 的context必须被主动管理，以保持在 `token` 限制之内。

### 2.4. 可观测性 (Observability)
- **统一事件日志 (Unified Event Log)**: 所有用于审计、调试和追踪的事件都将被记录在一个统一的 `events` 表中。
- **层级化运行 ID (Hierarchical Run IDs)**: 使用一个通用的 `parent_run_id` 关系来为进程层级建模，从而实现因果链的追踪。
- **事件命名约定 (Event Naming)**: 事件类型使用点 (`.`) 来为命名空间分层（例如, `llm_call.started`）。

### 2.5. Adapter 边界
- 所有 LLM provider 特定的类型、协议和行为必须封装在各自的 adapter 内部。
- 系统的其余部分（`worker`、`context` 层等）只使用通用抽象类型（如 `context.Message`、`CompletionResponse`），不直接依赖任何 provider 的类型。
- 每个 adapter 负责：通用类型到 provider 请求格式的转换、provider 响应到通用类型的转换、provider 特定的错误处理和重试逻辑。
- 新增 LLM provider 时，只需新增一个 adapter，不应修改 `context` 层或 `worker` 的代码。

### 2.6. 分层 Package 设计
参考 pi-mono 的分层架构，系统按职责分为独立的层级，每层可独立使用：

| 层级 | 职责 | 独立性 |
|---|---|---|
| `ai` | LLM 通信：adapter、通用消息类型、`CompletionResponse` | 可独立使用，不依赖 agent 或上层逻辑 |
| `agent` | Agent 循环：context 管理、turn 执行、事件记录 | 依赖 `ai`，可独立启动一个 agent loop |
| `coding-agent` | 领域特定：tool 子系统、代码操作、自我更新 | 依赖 `ai` + `agent` |

各 milestone 与层级的对应关系：
- `ai` 层: Milestone 0 (OpenAI adapter)、Milestone 2 (context 通用消息类型)
- `agent` 层: Milestone 1 (可观测性)、Milestone 2 (context 管理)、Milestone 3 (控制平面)
- `coding-agent` 层: Milestone 4 (tool 子系统)、Milestone 5 (自我更新)

上层依赖下层，下层不感知上层的存在。每层定义自己的接口，通过依赖注入组合。

## 3. 设计文档
本文档提供一个宏观的概览。关于具体的实现计划，请参考各个 `milestone` 的文档：

- **[Milestones 路线图](./milestones.md)**: 所有 `milestone` 的目标和交付物概览。
- **[Milestone 0: 基础结构](./milestone-0.md)**: 定义了已完成的 `MVP`。
- **[Milestone 1: 可观测性计划](./milestone-1.md)**: 详细说明了统一事件日志系统的实现计划。
- **[Milestone 2: Context Subsystem MVP](./milestone-2.md)**: Context Provider / Compressor / Assembler 三接口设计。
- **[Milestone 4: Tool Subsystem](./milestone-4.md)**: 工具子系统的详细设计与实施计划。

*一份用于指导开发辅助 `LLM` 的原则性文档位于 `AGENTS.md`。*

### 3.1. Milestone 文档治理

- 每个里程碑只保留一个权威设计文档：`docs/milestone-N.md`。
- `docs/milestone-N.md` 必须足够完整，使任意 coding agent 仅通过该文档即可重新实现该 milestone。
- 不维护并行的 `plan-milestoneN.md` 长期文档；若出现临时计划稿，最终必须合并回 `docs/milestone-N.md` 并删除计划稿，避免双源漂移。

## 4. 运行模型

Docker 容器是 Agent 的服务器，**只启动一次**，持续运行。容器内是一对 Supervisor + Worker 进程。对 Agent 而言，不存在"宿主机"与"容器"的区别——它只知道自己是一对进程。

### 4.1. Bootstrap 阶段（当前）

Agent 尚不具备自我更新能力。代码修改由宿主机上的开发者（通过 Claude Code 等工具）完成：

1. 宿主机修改代码（仓库通过 `-v` 挂载到容器内 `/workspace`）
2. 进入容器编译新 binary
3. Worker 退出后，Supervisor 自动启动新版本

### 4.2. 自主阶段（目标）

Agent 具备自我更新能力后，宿主机不再参与开发：

1. 用户通过 Telegram 对话指示 Agent 实现功能
2. Worker 修改代码 → 编译新 binary → kill 自己
3. Supervisor 启动新版 Worker

### 4.3. 容器管理原则

- 容器使用 `--restart unless-stopped`，确保意外退出后自动恢复
- 只有万不得已（如 Dockerfile 变更、基础镜像升级）才销毁重建
- Bootstrap 阶段仅允许挂载源码目录到 `/workspace`，用于容器内编译
- `SQLite` 数据库文件位于容器内 `/state/agent.db`，禁止从宿主机挂载 `/state`
- 在当前策略下，销毁并重建容器会清空数据库状态（如需保留需显式备份/导出）

## 5. 关键历史决策
本部分作为项目生命周期中关键决策的存档。

- **启动策略 (Bootstrap Strategy)**: 从最小可验证的闭环开始，然后再进行扩展。
- **控制平面 (Control Plane)**: 使用 `Telegram` 作为初始控制平面。
- **密钥管理 (Secret Management)**: 在运行时通过环境变量注入密钥。
- **运行时 (Runtime)**: 使用与 `Docker` 兼容的容器和 `debian:bookworm-slim` 基础镜像。
- **语言选择 (Language Choice)**: 项目最初使用 `Rust` 编写，现在正在用 **`Golang`** 重写。
- **持久化 (Persistence)**: 使用 `SQLite` 作为单一事实来源。
- **执行模型 (Execution Model)**: 使用队列优先、单一消费者的模型来保证任务处理的可靠性。
- **部署 (Deployment)**: 通过 `redeploy_autonous.sh` 脚本来标准化部署流程。

## 6. 配置
所有配置都通过环境变量进行管理。关键变量包括：
- `TELEGRAM_BOT_TOKEN`
- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `AUTONOUS_DB_PATH`
- `INSTANCE_ID` (用于 `supervisor`)
- `PARENT_RUN_ID` (用于 `worker`)

## 7. E2E 测试消息规范

为确保端到端链路验证具备可判定性，项目所有 milestone 的 E2E 测试统一遵循以下规则：

- 发送给 Agent 的测试消息必须是“可执行指令”，而非仅随机标识字符串。
- 推荐格式：`<trace_id>, <explicit task instruction>`，例如：
  - `E2E-M3-T3-20260217-231629, extract timestamp from it and reply with parsed time`
- 验证标准不只看“有回复”，还应校验回复内容与指令语义一致（例如确实提取并解析了时间戳）。
