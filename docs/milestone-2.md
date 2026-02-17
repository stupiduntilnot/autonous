# Milestone 2 计划: Context Subsystem MVP

## 核心原则

- **三个独立接口**: Context Provider、Context Compressor、Prompt Assembler，各自可独立替换。
- **先 naive，再 smart**: MVP 用最简单的实现（固定条数 + 字符截断），接口设计为后续 LLM 摘要、语义检索预留空间。
- **通用消息模型**: Context 层使用自己的 `Message` 类型，不依赖任何 LLM provider 的类型。OpenAI 等 provider 特定的转换由各自的 Adapter 负责。
- **不引入新表**: 继续使用 `history` 表存储对话历史，不增加额外持久化。
- **可观测**: 压缩行为记录到 `events` 表，可追踪上下文是如何被裁剪的。

## 与 pi-mono 的关系

参考 pi-mono 的 context 管理架构作为设计灵感，但用 Go 自身的风格和惯例实现，不照搬其 TypeScript 代码模式。

pi-mono 的三层 context 流水线：

```
buildSessionContext()    <- 从持久化存储加载消息 (= Provider)
  transformContext()     <- 剪枝、注入             (= Compressor)
    convertToLlm()       <- 转换为 LLM 格式        (= Assembler + Adapter)
```

本项目 MVP 对齐这一分层，但压缩策略更简单：固定消息条数 + 字符截断（pi-mono 用 token 预算 + LLM 摘要，那是 Milestone 3+ 的事）。

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

各 LLM Adapter 负责双向转换：
- `context.Message` -> `openai.Message`（发送给 LLM 时）
- LLM response -> `context.Message`（存入 history 时）

当前只有 OpenAI adapter，转换是 trivial 的（字段名相同）。但当引入 Anthropic、Gemini 等 provider 时，各自的消息格式差异由 adapter 封装，context 层不受影响。

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
- `Compressor` 是纯函数——输入消息列表，输出裁剪后的消息列表。压缩策略（条数上限、字符截断）由实现的 struct 字段配置。
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

两步压缩，对应 milestones.md 中的 deliverables：

1. **Max N messages**: 保留最近 N 条消息，丢弃更早的。
2. **Max string characters trim**: 任何单条消息超过字符上限时截断。

```go
// internal/context/compressor.go

type SimpleCompressor struct {
    MaxMessages        int // Maximum number of messages to keep
    MaxCharsPerMessage int // Truncate individual messages exceeding this
}

func (c *SimpleCompressor) Compress(messages []Message) []Message {
    // 1. Keep only the last MaxMessages messages
    // 2. Truncate individual messages exceeding MaxCharsPerMessage
}
```

Tool output skip + truncate 在 milestones.md 中提及，但当前无 tool call（Milestone 4），因此 MVP 不实现。接口层面已兼容——未来 `Compressor` 实现可检查消息来源并特殊处理 tool output。

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
// Convert context.Message -> openai.Message before LLM call
llmMessages := toOpenAIMessages(ctxMessages)
```

三个组件在 worker 启动时初始化，通过参数传入 `processTask`。

`toOpenAIMessages` 是一个简单的转换函数，将 `context.Message` 映射到 `openai.Message`。未来引入其他 provider 时，各 adapter 提供自己的转换。

## 压缩事件

压缩行为记录到 `events` 表：

| event_type | 层级 | payload |
|---|---|---|
| `context.compressed` | Turn | `original_count`, `compressed_count`, `max_messages`, `max_chars_per_message` |

仅在实际发生裁剪时记录（`compressed_count < original_count` 或有消息被截断）。在 `turn.started` 之后、LLM 调用之前记录，挂在 agent event 下。

## 配置

新增环境变量：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MAX_CHARS_PER_MESSAGE` | `4000` | 单条消息最大字符数 |

`TG_HISTORY_WINDOW`（已有，默认 12）控制消息条数上限，复用现有配置。

## 任务分解

### 1. 通用消息模型 + 接口

- [ ] 创建 `internal/context/message.go`：定义 `Message` struct。
- [ ] 创建 `internal/context/context.go`：定义 `Provider`、`Compressor`、`Assembler` 接口。

### 2. Naive 实现

- [ ] 创建 `internal/context/provider.go`：实现 `SQLiteProvider`。
- [ ] 创建 `internal/context/compressor.go`：实现 `SimpleCompressor`。
- [ ] 创建 `internal/context/assembler.go`：实现 `StandardAssembler`。

### 3. 测试

- [ ] `internal/context/compressor_test.go`：测试消息条数裁剪、单条消息截断、空输入、无需裁剪。
- [ ] `internal/context/assembler_test.go`：测试拼接顺序。
- [ ] `internal/context/provider_test.go`：测试 SQLite 读取 + 排序。

### 4. Worker 集成

- [ ] 添加 `toOpenAIMessages` 转换函数。
- [ ] 在 `cmd/worker/main.go` 中初始化三个组件。
- [ ] 替换 `buildMessages` 为 provider -> compressor -> assembler 流水线。
- [ ] 删除 `recentHistory`、`buildMessages` 旧函数。
- [ ] 添加 `context.compressed` 事件记录。

### 5. 配置

- [ ] 在 `internal/config/config.go` 中添加 `MaxCharsPerMessage` 字段。

## 未来演进（不在 MVP 范围内）

- **Token 预算压缩** (Milestone 3): `Compressor` 基于 `chars/4` 估算做 token 级裁剪，与 pi-mono 的 `findCutPoint` 对齐。
- **LLM 摘要压缩** (Milestone 3+): `Compressor` 实现可调用 LLM 对旧消息生成摘要，与 pi-mono 的 `compaction` 对齐。
- **Tool output 处理** (Milestone 4): `Compressor` 识别 tool call 结果并单独截断/跳过。
- **多 provider 支持**: 各 LLM provider 实现自己的 `context.Message` <-> provider message 转换。
- **语义检索**: Provider 实现可基于向量相似度检索相关历史，而非简单时间窗口。
