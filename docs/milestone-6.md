# Milestone 6 计划: 配置目录与系统提示符

## 核心目标

建立标准化的用户配置目录，并将系统提示符（system prompt）外部化为可读写的文件，使 Agent 能够在运行时加载自定义指令，并通过 M4 工具自我修改行为。

## 配置目录

遵循 [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html)（XDG = X Desktop Group，即 freedesktop.org 标准）：

```
$XDG_CONFIG_HOME/autonous/
```

若 `$XDG_CONFIG_HOME` 未设置，默认为：

```
$HOME/.config/autonous/
```

支持 `AUTONOUS_CONFIG_DIR` 环境变量显式覆盖，优先级：

```
AUTONOUS_CONFIG_DIR > $XDG_CONFIG_HOME/autonous > $HOME/.config/autonous
```

Worker 启动时，若配置目录不存在，**不自动创建**（避免意外写入用户 HOME）。若 `AUTONOUS_CONFIG_DIR` 被显式设置，则在启动时创建。

## 系统提示符文件

### 路径

```
$AUTONOUS_CONFIG_DIR/AUTONOUS.md
```

### 加载逻辑

Worker 启动时：

1. 若 `AUTONOUS.md` 存在，读取其内容作为 system prompt
2. 若文件不存在，使用内置默认 system prompt（行为与当前相同）
3. 若文件存在但读取失败，记录警告事件并 fallback 到内置默认值（不中断启动）

加载结果写入 `events` 表：

- 事件类型：`system_prompt.loaded`
- payload：`source`（`file` | `env` | `builtin`）、`config_dir`、`size_bytes`

### Agent 自我修改

Agent 可通过 M4 的 `read`/`write`/`edit` 工具直接操作 `AUTONOUS.md`，修改自身的核心指令。改动立即落盘，下次 Worker 启动时生效。

System prompt 中需包含以下元信息，让 Agent 知道文件位置：

```
配置目录: {AUTONOUS_CONFIG_DIR}
系统提示符文件: {AUTONOUS_CONFIG_DIR}/AUTONOUS.md
你可以通过 read/write/edit 工具读取和修改该文件，修改在下次会话启动时生效。
```

Worker 构建 system prompt 时，将 `{AUTONOUS_CONFIG_DIR}` 替换为实际路径。

## 配置（新增 ENV）

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `AUTONOUS_CONFIG_DIR` | `$XDG_CONFIG_HOME/autonous` 或 `$HOME/.config/autonous` | 用户配置目录 |

### 与现有 `WORKER_SYSTEM_PROMPT` 的关系

当前代码中已有 `WORKER_SYSTEM_PROMPT` 环境变量，M6 引入文件方式后，优先级为：

```
AUTONOUS.md 文件 > WORKER_SYSTEM_PROMPT env var > 内置默认值
```

`WORKER_SYSTEM_PROMPT` 保留作为向后兼容的回退，文件方式优先。

## 实现要点

### internal/config/config.go

新增字段：

```go
// ConfigDir is the user config directory.
// Resolved from AUTONOUS_CONFIG_DIR > $XDG_CONFIG_HOME/autonous > $HOME/.config/autonous.
ConfigDir string

// SystemPromptFile is the path to the user-defined system prompt.
// Defaults to ConfigDir + "/AUTONOUS.md".
SystemPromptFile string
```

配置目录解析：

```go
func resolveConfigDir() string {
    if v := os.Getenv("AUTONOUS_CONFIG_DIR"); v != "" {
        return v
    }
    if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
        return filepath.Join(xdg, "autonous")
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".config", "autonous")
}
```

### cmd/worker/main.go

System prompt 加载优先级：文件 > `WORKER_SYSTEM_PROMPT` env var > 内置默认值：

```go
// loadSystemPrompt returns the system prompt and its source.
func loadSystemPrompt(cfg *config.WorkerConfig) (prompt string, source string) {
    data, err := os.ReadFile(cfg.SystemPromptFile)
    if err == nil {
        return injectConfigMeta(string(data), cfg.ConfigDir), "file"
    }
    if v := os.Getenv("WORKER_SYSTEM_PROMPT"); v != "" {
        return injectConfigMeta(v, cfg.ConfigDir), "env"
    }
    return injectConfigMeta(builtinSystemPrompt(), cfg.ConfigDir), "builtin"
}
```

`source` 字段枚举：`file` | `env` | `builtin`，写入 `system_prompt.loaded` 事件。

## 测试计划

### 单元测试

- `resolveConfigDir`：各环境变量组合的路径解析（三种优先级）
- `loadSystemPrompt`：文件存在 / 不存在 / 读取失败三种情况

### 集成测试

- Worker 启动时 `system_prompt.loaded` 事件写入正确，`source` 字段准确
- Agent 通过 `edit` 工具修改 `AUTONOUS.md` 后，下次启动加载新内容

## 任务分解

- [ ] `resolveConfigDir` 函数（含 XDG 支持与环境变量优先级）
- [ ] `WorkerConfig` 新增 `ConfigDir`、`SystemPromptFile` 字段
- [ ] `loadSystemPrompt` 函数（含三路 fallback 与 `system_prompt.loaded` 事件）
- [ ] System prompt 模板注入 `{AUTONOUS_CONFIG_DIR}` 元信息
- [ ] `go build ./... && go test ./...` 全通过
- [ ] 文档与实现一致（`milestones.md` 更新）
