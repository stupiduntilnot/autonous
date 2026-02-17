# Milestone 4 计划: Tool Subsystem

## 核心目标

在现有 `Supervisor + Worker + SQLite` 架构上引入最小可用的 Tool Subsystem，使 Agent 具备受控的本地操作能力（读写文件、检索、命令执行），并保持：

- 安全边界明确（可拒绝高风险输入）
- 执行结果可审计（事件可追踪）
- 失败可恢复（不因单次 tool 失败导致 worker 崩溃）

## 核心原则

- **工具原子化**: 每个 tool call 是一次完整原子操作，不使用两阶段写入（NO two-phase writes）。
- **默认拒绝**: 未注册 tool、超出路径边界、超出风险策略的请求默认拒绝。
- **统一协议**: 所有工具输入输出走统一 envelope，避免每个 tool 各自定义返回格式。
- **可观测优先**: tool 生命周期、截断、超时、失败都必须落 `events`。
- **与 M3 兼容**: 继续复用 `Commander` / `ModelProvider` 抽象与 control policy。
- **对齐 pi-mono 但不照搬**: 以 `~/code/pi-mono` 的 tool 分层与执行约束为 blueprint，对齐行为与边界，不直接复制实现细节。

## 范围与非目标

### Milestone 4 范围

- Tool Registry（工具注册与调度）
  - strict input schema（typed struct / JSON schema）
  - strict output envelope（`stdout/stderr`、`exit_code`、`truncated_*`）
  - timeout、输出上限、分页能力
- Tool Safety Policy
  - allowlist
  - NO two-phase writes
- 首批 atomic tools 路线图（无需人工审批）
  - 第 1 阶段：仅 `ls`（最小可运行）
  - 第 2 阶段：`find`, `grep`, `read`, `write`, `edit`, `bash`（在机制跑通后逐个补齐）
- Worker 内最小 tool loop（单 task 内允许多次 tool round，受 M3 限制保护）

### 非目标（后续 milestone）

- 多 worker 并行 tool 调度
- 细粒度 RBAC（按用户/会话权限矩阵）
- 跨容器远程执行器
- 复杂 artifact 生命周期管理（归 Milestone 5）

## 总体架构

```
Telegram(Commander) -> inbox -> Worker
  -> ModelProvider (请求下一步)
    -> ToolPlanner (解析 tool request)
      -> ToolRegistry -> ToolRunner -> ToolResult
    -> ModelProvider (携带 tool result 再推理)
  -> Commander.SendMessage
```

关键点：

1. Worker 仍是单消费者串行处理 task。
2. Tool execution 只发生在 Worker 内，不引入新进程角色。
3. 每次 tool call 都落 `tool_call.*` 事件，保持和 Milestone 1 的事件模型一致。

## 抽象设计

### 1) Tool 接口

`internal/tool/tool.go`（建议）：

```go
type Tool interface {
    Name() string
    Validate(raw json.RawMessage) error
    Execute(ctx context.Context, raw json.RawMessage) (Result, error)
}
```

### 2) Registry

`internal/tool/registry.go`：

- `Register(tool Tool) error`
- `Get(name string) (Tool, bool)`
- `MustList() []ToolMeta`

约束：
- 名称唯一；重复注册直接报错。
- 未注册 tool 统一返回 `validation` 类错误。

### 3) 统一输出 Envelope

`internal/tool/result.go`：

```go
type Result struct {
    OK             bool              `json:"ok"`
    ExitCode       int               `json:"exit_code"`
    Stdout         string            `json:"stdout"`
    Stderr         string            `json:"stderr"`
    TruncatedLines bool              `json:"truncated_lines"`
    TruncatedBytes bool              `json:"truncated_bytes"`
    NextPageCursor string            `json:"next_page_cursor,omitempty"`
    Meta           map[string]any    `json:"meta,omitempty"`
}
```

说明：
- 所有工具都返回 `Result`，即使是纯文件操作也保持一致结构。
- 非 shell tool 的 `ExitCode` 约定：成功为 `0`，失败为非 0。

### 4) Tool 请求协议（Model -> Worker）

为避免与 provider-specific function calling 耦合，M4 采用统一 JSON 协议：

```json
{
  "tool_calls": [
    {"name":"read","arguments":{"path":"internal/db/db.go"}}
  ],
  "final_answer":""
}
```

规则：
- 若 `tool_calls` 非空，worker 执行工具并把结果作为下一轮上下文输入模型。
- 若 `tool_calls` 为空且 `final_answer` 非空，worker 发送最终回复并结束 task。
- 若两者都为空，判为 `validation` 错误并失败。

## Safety Policy

### 1) 路径策略

- allowlist 完全由单一配置 `AUTONOUS_TOOL_ALLOWED_ROOTS` 决定（逗号分隔绝对路径）。
- 拒绝：
  - 跳出 allowlist 的真实路径（含符号链接逃逸）
  - 相对路径中的 `..` 越界

配置示例：

```bash
AUTONOUS_TOOL_ALLOWED_ROOTS=/workspace,/state
```

### 2) 命令与工具策略

- 仅允许注册在 Registry 内的工具名称。
- `bash` 增加 denylist（至少阻断明显破坏性命令）。
- 其余工具按参数校验 + 路径 allowlist 执行，不引入分层策略字段。

### 3) 执行限制

- 每个 tool call 强制 timeout（例如 30s）
- 输出双限额：
  - 最大行数（如 2000）
  - 最大字节（如 50KB）
- 超限时截断并设置 `truncated_*` 标记
- 对将被记录到事件或回传给模型/用户的文本执行 `secret redaction`
  - 至少覆盖：API key、Bearer token、常见 `*_TOKEN/*_SECRET/*_PASSWORD` 键值
  - 命中后替换为 `***REDACTED***`

### 4) NO two-phase writes

`write/edit` 仅支持“一次调用即完成”，不提供 `prepare/commit` 分离语义。

## Worker 集成

### 1) 处理流程（单 task）

1. `agent.started`
2. 检查 M3 控制策略（turn/wall_time/retry/circuit）
3. 调用模型，解析 tool request
4. 对每个 tool call：
   - 写 `tool_call.started`
   - 执行 tool
   - 写 `tool_call.completed` 或 `tool_call.failed`
5. 将 tool result 追加到上下文，再次调用模型
6. 直到得到 `final_answer`，发送消息并 `agent.completed`

### 2) 与 M3 的关系

- tool loop 每轮都计入 `max_turns`
- 所有 tool round 时间累计到 `max_wall_time`
- model token 继续累计用于 `max_tokens`
- 无进展检测可复用（例如多轮重复同一 tool call）

## 事件设计

沿用 Milestone 1 既有事件并扩展 payload 约定：

- `tool_call.started`
  - `tool_name`
  - `arguments`（必要时截断）
- `tool_call.completed`
  - `tool_name`
  - `latency_ms`
  - `exit_code`
  - `truncated_lines`
  - `truncated_bytes`
- `tool_call.failed`
  - `tool_name`
  - `error`
  - `error_class`（`validation/tool_exec/policy/timeout/unknown`）
  - `redacted`（是否发生脱敏，`true/false`）

## 配置（新增 ENV）

- `AUTONOUS_TOOL_TIMEOUT_SECONDS`（默认 `30`）
- `AUTONOUS_TOOL_MAX_OUTPUT_LINES`（默认 `2000`）
- `AUTONOUS_TOOL_MAX_OUTPUT_BYTES`（默认 `51200`）
- `AUTONOUS_TOOL_BASH_DENYLIST`（可选，逗号分隔）
- `AUTONOUS_TOOL_ALLOWED_ROOTS`（必填，逗号分隔绝对路径）

说明：
- 所有新增变量使用 `AUTONOUS_` 前缀。
- 不增加全局 `tool enable` 开关；若需要禁用某能力，使用 policy/allowlist 控制并落事件。
- `AUTONOUS_TOOL_ALLOWED_ROOTS` 的设置时机是容器启动时（部署层注入 env），由 `supervisor` 进程继承给 `worker`。
- 当前阶段不做运行时动态审批；allowlist 变更通过重新部署生效。
- 启动期校验要求：
  - 至少包含一个 root（为空则启动失败）
  - 每个 root 必须是绝对路径
  - `Clean` 后去重

## 首批工具规格（MVP）

实施顺序约束（M4）：
- 先只实现一个最简单工具 `ls`，用于打通完整机制：registry/policy/runner/model-tool loop/events/tests/e2e。
- 在 `ls` 链路完成并通过 E2E 后，再实现其余工具。
- 其余工具必须按“单工具单任务”推进：每次只新增一个工具，配套单测与 E2E，再进入下一工具。

实现决策：
- M4 的 atomic tools **不在 Go 中重写业务逻辑**，统一基于成熟 CLI 工具封装（固定参数模板 + 输入白名单校验 + 超时 + 截断）。
- 命令选型原则：
  - 优先使用广泛采用且行为稳定的工具；
  - 若存在成熟的现代实现（尤其 Rust 生态），优先于传统内置工具（例如 `rg` 优先于 `grep`，`fd` 优先于 `find`）。
- Docker 镜像构建时必须预装 tool runtime 依赖，避免运行期缺命令。

M4 默认命令映射：
- `ls` -> `ls`（coreutils）
- `find` -> `fd`（不可用时可回退 `find`，但默认应安装并使用 `fd`）
- `grep` -> `rg`
- `read` -> `cat`/`sed`/`head`（按 offset/limit 选择）
- `write` -> `tee`（overwrite/append）
- `edit` -> `sed`（必要时配合 `perl`，仍按 CLI 路径）
- `bash` -> `bash`

关于 `sed` 与 `awk` 的选择：
- `edit` 的核心语义是“查找替换”，`sed` 更直接且更易标准化为固定模板。
- `awk` 在字段处理/文本提取场景更强，作为补充工具保留，不作为 M4 的默认 edit 实现。

容器依赖要求（M4）：
- 必装：`bash`, `coreutils`, `sed`, `cat`, `head`, `tee`, `rg`, `fd`
- 可选增强：`perl`（复杂替换场景更稳）
- 以上依赖缺失应在启动自检时报错并拒绝进入 tool mode。

### `ls`
- 入参：`path`, `recursive`, `limit`
- 输出：目录项文本（可分页）

### `find`
- 入参：`path`, `name_pattern`, `max_depth`, `limit`
- 输出：匹配路径列表

### `grep`
- 入参：`path`, `pattern`, `glob`, `limit`
- 输出：匹配行（`file:line:text`）

### `read`
- 入参：`path`, `offset`, `limit_bytes`
- 输出：文件片段

### `write`
- 入参：`path`, `content`, `mode(append|overwrite)`
- 输出：写入字节数

### `edit`
- 入参：`path`, `find`, `replace`, `all`
- 输出：替换次数

### `bash`
- 入参：`cmd`, `workdir`, `timeout_seconds`
- 输出：`stdout/stderr/exit_code`

## 测试计划

### 单元测试

- `internal/tool/registry_test.go`
  - 注册冲突、未注册工具、元数据查询
- `internal/tool/policy_test.go`
  - allowlist、symlink escape、防越界
- `internal/tool/output_test.go`
  - 行/字节截断、分页游标
- 各工具测试：
  - 参数校验
  - 成功/失败路径
  - 超时与输出上限

### 集成测试（worker）

- 模型返回单次 tool call，验证 `tool_call.started/completed`
- 模型返回连续 tool call，验证 loop + `max_turns` 限制
- 工具失败时验证 `tool_call.failed` + retry/circuit 联动

### E2E 测试

- Telegram 下发明确指令（例如“读取某文件并总结”）验证真实链路
- dummy provider 注入坏 tool request（非法参数、未知 tool）验证防护
- 验证 `event-tree` 可重建 agent->turn->tool_call 层级
- Tool-by-tool E2E 覆盖矩阵：
  - `ls`: 列目录 + 分页/截断
  - `find`: 模式匹配 + depth/limit
  - `grep`: 命中/未命中 + 大输出截断
  - `read`: offset/limit + 越界路径拒绝
  - `write`: overwrite/append + 写后读回校验
  - `edit`: 单次替换/全量替换 + 不匹配场景
  - `bash`: 正常命令/超时/denylist 拒绝
- 执行策略：
  - PR gate: 至少 1 个真实链路 smoke + 关键高风险工具（`write/edit/bash`）E2E
  - Nightly: 全工具矩阵全量执行

## 任务分解

### 1. 抽象与基础设施

- [DONE] 新建 `internal/tool` 包：`Tool` 接口、`Registry`、`Result`。
- [DONE] 实现统一输入校验辅助（typed struct / JSON decode + validate）。
- [DONE] 实现输出截断与分页基础组件。

### 2. 安全策略

- [DONE] 实现路径 allowlist 与真实路径校验（防 symlink/`..` 逃逸）。
- [DONE] 实现工具策略校验（registered-only + `bash` denylist）。
- [DONE] 实现 timeout 与统一错误分类。

### 3. 工具实现（MVP）

- [DONE] 第一步仅实现 `ls`，用于验证工具子系统全链路。
- [DONE] 为 `ls` 补齐单测（成功/失败/边界）与对应 E2E。

### 4. Worker 集成

- [DONE] 在 worker 注入 `ToolRegistry` 与 `ToolRunner`。
- [DONE] 接入模型 tool request 协议并实现最小 tool loop。
- [DONE] 将 tool 事件接入 `events`（started/completed/failed）。

### 5. 配置

- [DONE] 在 `internal/config/config.go` 增加 M4 所需 ENV。
- [DONE] 为默认值和非法配置增加启动期校验。
- [DONE] 解析并校验 `AUTONOUS_TOOL_ALLOWED_ROOTS`（非空、绝对路径、去重）。
- [DONE] 更新 `Dockerfile` 预装 M4 所需 CLI 依赖（含 `rg`、`fd`），并在启动期增加命令可用性自检。

### 6. 工具实现：`find`

- [DONE] 实现 `find`。
- [DONE] 补齐 `find` 单测（成功/失败/边界）与对应 E2E。

### 7. 工具实现：`grep`

- [ ] 实现 `grep`。
- [ ] 补齐 `grep` 单测（成功/失败/边界）与对应 E2E。

### 8. 工具实现：`read`

- [ ] 实现 `read`。
- [ ] 补齐 `read` 单测（成功/失败/边界）与对应 E2E。

### 9. 工具实现：`write`

- [ ] 实现 `write`。
- [ ] 补齐 `write` 单测（成功/失败/边界）与对应 E2E。

### 10. 工具实现：`edit`

- [ ] 实现 `edit`。
- [ ] 补齐 `edit` 单测（成功/失败/边界）与对应 E2E。

### 11. 工具实现：`bash`

- [ ] 实现 `bash`。
- [ ] 补齐 `bash` 单测（成功/失败/边界）与对应 E2E。

### 12. 验收

- [DONE] `go build ./... && go test ./...` 全通过。
- [DONE] `ls` 的真实 Telegram E2E（含明确 tool 任务指令）通过。
- [ ] 其余工具按“单工具单任务”逐个完成各自 E2E。
- [ ] dummy failure-injection 用例通过。
