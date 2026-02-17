# Milestone 2 计划: Context Subsystem MVP

## 核心原则

- **三个独立接口**: Context Provider、Context Compressor、Prompt Assembler，各自可独立替换。
- **先 naive，再 smart**: MVP 只用 max N messages，接口设计为后续字符截断、token 预算、LLM 摘要预留空间。
- **通用消息模型**: Context 层使用自己的 `Message` 类型，不依赖任何 LLM provider 的类型。OpenAI 等 provider 特定的转换由各自的 Adapter 负责。
- **不引入新表**: 继续使用 `history` 表存储对话历史，不增加额外持久化。
- **可观测**: 压缩行为记录到 `events` 表，可追踪上下文是如何被裁剪的。

## 与 pi-mono 的关系

参考 pi-mono 的 context 管理架构作为设计灵感，但用 Go 自身的风格和惯例实现，不照搬其 TypeScript 代码模式。

pi-mono 的三层 context 流水线：

```
buildSessionContext()    <- 从持久化存储加载消息 (= Provider)
  transformContext()     <- 剪枝、注入         (= Compressor)
    convertToLlm()       <- 转换为 LLM 格式    (= Assembler + Adapter)
```

本项目 MVP 对齐这一分层，但压缩策略最简：仅 max N messages（pi-mono 用 token 预算 + LLM 摘要，那是后续 milestone 的事）。

## 通用消息模型

Context 层定义自己的 `Message` 类型，独立于任何 LLM provider：

```go
// internal/context/message.go

// Message is the provider-agnostic message type used throughout the context subsystem.
type Message struct {
    Role    string // "system", "user", "assistant"
    Content string
}
```

各 LLM Adapter 在内部完成双向转换：
- `context.Message` -> provider 特定格式（在 `ChatCompletion` 方法内部，调用者不感知）
- LLM response -> `CompletionResponse`（已有通用返回类型）

当前只有 OpenAI adapter，转换是 trivial 的（字段名相同）。但当引入 Anthropic、Gemini 等 provider 时，各自的消息格式差异由 adapter 内部封装，worker 和 context 层不受影响。

## 接口设计

### `internal/context/context.go`

```go
package context

// Provider retrieves conversation history.
type Provider interface {
    GetHistory(chatID int64, limit int) ([]Message, error)
}

// Compressor reduces context size.
type Compressor interface {
    Compress(messages []Message) []Message
}

// Assembler builds the final message list for the LLM.
type Assembler interface {
    Assemble(system string, history []Message, userMsg string) []Message
}
```

设计要点：
- 接口参数使用 `context.Message`，不依赖 `openai.Message`。
- `Compressor` 是纯函数——输入消息列表，输出裁剪后的消息列表。压缩策略由实现的 struct 字段配置。
- `Assembler` 是纯函数——拼接 system + history + user message。
- 只有 `Provider` 涉及 I/O（读数据库）。

## Naive 实现

### SQLiteProvider

从 `history` 表读取最近 N 条消息（移植现有 `recentHistory` 函数）：

```go
// internal/context/provider.go

type SQLiteProvider struct {
    DB *sql.DB
}

func (p *SQLiteProvider) GetHistory(chatID int64, limit int) ([]Message, error) {
    // SELECT role, text FROM history WHERE chat_id = ? ORDER BY id DESC LIMIT ?
    // Reverse to chronological order
}
```

### SimpleCompressor

MVP 只做一件事：保留最近 N 条消息，丢弃更早的。

```go
// internal/context/compressor.go

type SimpleCompressor struct {
    MaxMessages int // Maximum number of messages to keep
}

func (c *SimpleCompressor) Compress(messages []Message) []Message {
    // Keep only the last MaxMessages messages
}
```

### StandardAssembler

简单拼接：

```go
// internal/context/assembler.go

type StandardAssembler struct{}

func (a *StandardAssembler) Assemble(system string, history []Message, userMsg string) []Message {
    msgs := make([]Message, 0, len(history)+2)
    msgs = append(msgs, Message{Role: "system", Content: system})
    msgs = append(msgs, history...)
    msgs = append(msgs, Message{Role: "user", Content: userMsg})
    return msgs
}
```

## Worker 集成

当前 `processTask` 中的 `buildMessages` 函数将被替换：

```go
// Before (Milestone 1):
messages, err := buildMessages(database, cfg.SystemPrompt, task.ChatID, cfg.HistoryWindow, task.Text)

// After (Milestone 2):
history, err := provider.GetHistory(task.ChatID, cfg.HistoryWindow)
compressed := compressor.Compress(history)
ctxMessages := assembler.Assemble(cfg.SystemPrompt, compressed, task.Text)
resp, err := ai.ChatCompletion(ctxMessages)
```

三个组件在 worker 启动时初始化，通过参数传入 `processTask`。

`context.Message` -> `openai.Message` 的转换由 OpenAI adapter 内部完成：`ChatCompletion` 接受 `[]context.Message`，在方法内部转换为 `openai` 的请求格式。Worker 全程只使用 `context.Message`，不接触任何 provider 特定类型。未来引入其他 LLM provider 时，各 adapter 同样在内部完成转换。

## 压缩事件

context 组装结果记录到 `events` 表：

| event_type | 层级 | payload |
|---|---|---|
| `context.assembled` | Turn | `original_count`, `compressed_count`, `max_messages`, `system_tokens`, `history_tokens`, `user_tokens` |

每次 LLM 调用前都记录，挂在 agent event 下。payload 包含：
- `original_count` / `compressed_count` / `max_messages`：消息裁剪情况
- `system_tokens` / `history_tokens` / `user_tokens`：各组件的估算 token 数

Token 估算使用 `ceil(chars / 4)` 启发式方法（与 pi-mono 的 `estimateTokens` 一致）。LLM API 只返回聚合的 `prompt_tokens`，不提供 system/history/user 的分项，因此本地估算是获取分项 token 成本的唯一途径。

## 配置

无新增环境变量。`TG_HISTORY_WINDOW`（已有，默认 12）控制消息条数上限，复用现有配置。

## 任务分解

### 1. 通用消息模型 + 接口

- [ ] 创建 `internal/context/message.go`：定义 `Message` struct。
- [ ] 创建 `internal/context/context.go`：定义 `Provider`、`Compressor`、`Assembler` 接口。

### 2. Naive 实现

- [ ] 创建 `internal/context/provider.go`：实现 `SQLiteProvider`。
- [ ] 创建 `internal/context/compressor.go`：实现 `SimpleCompressor`。
- [ ] 创建 `internal/context/assembler.go`：实现 `StandardAssembler`。

### 3. 测试

- [ ] `internal/context/compressor_test.go`：测试消息条数裁剪、空输入、无需裁剪。
- [ ] `internal/context/assembler_test.go`：测试拼接顺序。
- [ ] `internal/context/provider_test.go`：测试 SQLite 读取 + 排序。

### 4. Worker 集成

- [ ] 修改 `openai.ChatCompletion` 接受 `[]context.Message`，内部转换为 `openai.Message`。
- [ ] 在 `cmd/worker/main.go` 中初始化三个组件。
- [ ] 替换 `buildMessages` 为 provider -> compressor -> assembler 流水线。
- [ ] 删除 `recentHistory`、`buildMessages` 旧函数。
- [ ] 添加 `context.assembled` 事件记录（含各组件字符数）。
