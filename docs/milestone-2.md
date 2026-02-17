# Milestone 2 计划: Context Subsystem MVP

## 核心原则

- **三个独立接口**: Context Provider、Context Compressor、Prompt Assembler，各自可独立替换。
- **先 naive，再 smart**: MVP 用最简单的实现（滑动窗口 + 字符截断），接口设计为后续 LLM 摘要、语义检索预留空间。
- **Token 预算驱动**: 压缩决策基于 token 预算，而非固定消息条数。当前用 `chars/4` 估算，未来可接入精确 token 计数。
- **不引入新表**: 继续使用 `history` 表存储对话历史，不增加额外持久化。
- **可观测**: 压缩行为记录到 `events` 表，可追踪上下文是如何被裁剪的。

## 与 pi-mono 的对齐

pi-mono 的 context 管理分三层：

```
SessionManager.buildSessionContext()   <- 从持久化存储加载消息
  transformContext()                   <- 可选的中间处理（剪枝、注入）
    convertToLlm()                    <- 转换为 LLM 兼容格式
```

pi-mono 的 compaction 策略：
- 在 `contextTokens > contextWindow - reserveTokens` 时触发
- 保留最近 `keepRecentTokens` 的消息不动
- 对更早的消息调用 LLM 生成摘要
- 摘要作为 `compactionSummary` 消息插入上下文头部

本项目 MVP 不实现 LLM 摘要（那是 Milestone 3+ 的事），但接口设计兼容这一演进路径。

## 接口设计

### `internal/context/context.go`

```go
package context

import "github.com/autonous/autonous/internal/openai"

// ContextProvider retrieves conversation history.
type ContextProvider interface {
    GetHistory(chatID int64, limit int) ([]openai.Message, error)
}

// ContextCompressor reduces context size to fit within token budget.
type ContextCompressor interface {
    Compress(messages []openai.Message, budget TokenBudget) []openai.Message
}

// PromptAssembler builds the final message list for the LLM.
type PromptAssembler interface {
    Assemble(system string, history []openai.Message, userMsg string) []openai.Message
}

// TokenBudget defines constraints for context compression.
type TokenBudget struct {
    MaxTokens int // Maximum total tokens allowed (estimated)
    MaxChars  int // Maximum total characters (fallback when no token estimate)
}
```

设计要点：
- 接口参数是值类型（`[]openai.Message`），不依赖数据库或 HTTP client。
- `Compressor` 是纯函数——输入消息列表和预算，输出裁剪后的消息列表。
- `Assembler` 是纯函数——拼接 system + history + user message。
- 只有 `Provider` 涉及 I/O（读数据库）。

### Token 估算

与 pi-mono 一致，使用 `chars / 4` 作为保守估算：

```go
// EstimateTokens returns a conservative token estimate for a message.
func EstimateTokens(msg openai.Message) int {
    return (len([]rune(msg.Content)) + 3) / 4 // +3 for role overhead
}
```

不使用 tokenizer 库，不调用外部 API。真实 token 数从 `CompletionResponse.InputTokens` 获取（Milestone 1 已实现），用于可观测性，不用于压缩决策。

## Naive 实现

### SQLiteProvider

从 `history` 表读取最近 N 条消息（移植现有 `recentHistory` 函数）：

```go
type SQLiteProvider struct {
    DB *sql.DB
}

func (p *SQLiteProvider) GetHistory(chatID int64, limit int) ([]openai.Message, error) {
    // SELECT role, text FROM history WHERE chat_id = ? ORDER BY id DESC LIMIT ?
    // Reverse to chronological order
}
```

### SimpleCompressor

两步压缩：

1. **Token 预算裁剪**: 从最新消息向前累加 token 估算，超出预算时丢弃更早的消息。
2. **单条消息截断**: 任何单条消息超过 `MaxCharsPerMessage` 时截断。

```go
type SimpleCompressor struct {
    MaxCharsPerMessage int // Default: 4000
}

func (c *SimpleCompressor) Compress(messages []openai.Message, budget TokenBudget) []openai.Message {
    // 1. Truncate individual messages exceeding MaxCharsPerMessage
    // 2. Walk backwards from newest, accumulate estimated tokens
    // 3. Drop messages that exceed budget
}
```

裁剪策略和 pi-mono 的 `findCutPoint` 类似——从尾部向前保留，但不生成摘要（MVP 直接丢弃）。

### StandardAssembler

简单拼接：

```go
type StandardAssembler struct{}

func (a *StandardAssembler) Assemble(system string, history []openai.Message, userMsg string) []openai.Message {
    msgs := make([]openai.Message, 0, len(history)+2)
    msgs = append(msgs, openai.Message{Role: "system", Content: system})
    msgs = append(msgs, history...)
    msgs = append(msgs, openai.Message{Role: "user", Content: userMsg})
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
compressed := compressor.Compress(history, ctx.TokenBudget{MaxTokens: cfg.MaxContextTokens})
messages := assembler.Assemble(cfg.SystemPrompt, compressed, task.Text)
```

三个组件在 worker 启动时初始化，通过参数传入 `processTask`。

## 压缩事件

压缩行为记录到 `events` 表：

| event_type | 层级 | payload |
|---|---|---|
| `context.compressed` | Turn | `original_count`, `compressed_count`, `estimated_tokens`, `budget_tokens` |

在 `turn.started` 之后、LLM 调用之前记录，挂在 agent event 下。

## 配置

新增环境变量：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MAX_CONTEXT_TOKENS` | `8000` | 上下文 token 预算（估算值） |
| `MAX_CHARS_PER_MESSAGE` | `4000` | 单条消息最大字符数 |

## 任务分解

### 1. 定义接口和 naive 实现

- [ ] 创建 `internal/context/context.go`：定义 `ContextProvider`、`ContextCompressor`、`PromptAssembler` 接口 + `TokenBudget` struct + `EstimateTokens` 函数。
- [ ] 创建 `internal/context/provider.go`：实现 `SQLiteProvider`。
- [ ] 创建 `internal/context/compressor.go`：实现 `SimpleCompressor`。
- [ ] 创建 `internal/context/assembler.go`：实现 `StandardAssembler`。

### 2. 测试

- [ ] `internal/context/compressor_test.go`：测试 token 预算裁剪、单条消息截断、空消息、全部超预算。
- [ ] `internal/context/assembler_test.go`：测试拼接顺序。
- [ ] `internal/context/provider_test.go`：测试 SQLite 读取 + 排序。

### 3. Worker 集成

- [ ] 在 `cmd/worker/main.go` 中初始化三个组件。
- [ ] 替换 `buildMessages` 为 provider -> compressor -> assembler 流水线。
- [ ] 删除 `recentHistory`、`buildMessages` 旧函数。
- [ ] 添加 `context.compressed` 事件记录。

### 4. 配置

- [ ] 在 `internal/config/config.go` 中添加 `MaxContextTokens`、`MaxCharsPerMessage` 字段。

## 未来演进（不在 MVP 范围内）

- **LLM 摘要压缩** (Milestone 3+): `Compressor` 实现可调用 LLM 对旧消息生成摘要，与 pi-mono 的 `compaction` 对齐。
- **Tool output 处理** (Milestone 4+): `Compressor` 可识别 tool call 结果并单独截断/跳过，与 pi-mono 的 `toolResult` 消息类型对齐。
- **多轮 context 缓存**: 利用 Anthropic 的 cache_read/cache_write 减少重复 token 开销。
- **语义检索**: Provider 实现可基于向量相似度检索相关历史，而非简单时间窗口。
