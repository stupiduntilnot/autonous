# event-tree CLI 工具设计

## 目标

提供一个命令行工具，从 `events` 表中读取事件并以树形结构打印，用于调试和审查系统运行状态。

## 事件树结构

系统中所有事件组成**一棵统一的树**。`agent.started` 的 `parent_id` 直接指向 worker 的 `process.started`，无需通过 payload 中的额外字段关联。

```
process.started (role=supervisor)                     depth=1
├── revision.promoted                                 depth=2
├── worker.spawned                                    depth=2
├── process.started (role=worker)                     depth=2
│   ├── agent.started (chat_id=..., task_id=...)      depth=3
│   │   ├── turn.started                              depth=4
│   │   ├── turn.completed                            depth=4
│   │   └── reply.sent                                depth=4
│   └── agent.completed (task_id=...)                 depth=3
├── worker.exited                                     depth=2
├── crash_loop.detected                               depth=2
└── rollback.attempted                                depth=2
```

关键关系：
- supervisor 的 `process.started` 是 root（`parent_id=NULL`）
- worker 的 `process.started` 通过 `parent_id` 指向 supervisor 的 `process.started`
- `agent.started` 通过 `parent_id` 指向 worker 的 `process.started`
- `agent.completed` / `agent.failed` 与 `agent.started` 同级，都是 worker `process.started` 的子事件
- turn 级事件（`turn.started`、`turn.completed`、`reply.sent`）通过 `parent_id` 指向 `agent.started`

失败时 `agent.completed` 替换为 `agent.failed`。

## 命令行接口

```
event-tree [flags]
```

### 模式

| flag | 说明 | 示例 |
|---|---|---|
| （无参数） | 最近 supervisor 的完整树 | `event-tree` |
| `--id <event_id>` | 显示指定 event 及其所有子事件的子树 | `event-tree --id 42` |
| `-L <depth>` | 限制显示深度（类似 `tree -L`） | `event-tree -L 2` |

`-L` 深度语义：
- `-L 1`：只显示 supervisor root
- `-L 2`：supervisor + 直接子事件（`revision.promoted`、`worker.spawned`、`process.started(worker)`、`worker.exited` 等）
- `-L 3`：展开到 agent 层（`agent.started`、`agent.completed`）
- `-L 4`：展开到 turn 层（`turn.started`、`turn.completed`、`reply.sent`）
- 无 `-L`：展开全部

`-L` 可与 `--id` 组合使用，例如 `event-tree --id 4 -L 2` 显示指定 event 往下两层。

### 通用选项

| flag | 说明 | 默认值 |
|---|---|---|
| `--db <path>` | SQLite 数据库路径 | `$AUTONOUS_DB_PATH` 或 `/state/agent.db` |
| `--json` | 输出 JSON 格式（而非 tree 格式） | false |
| `--no-payload` | 不展示 payload 详情 | false |

## 输出格式

### Tree 格式（默认）

默认输出最近 supervisor 的完整树：

```
[1] 2026-02-17 10:00:00  process.started  role=supervisor pid=100 version=abc123
├── [2] 2026-02-17 10:00:00  revision.promoted  revision=abc123
├── [3] 2026-02-17 10:00:01  worker.spawned  pid=101
├── [4] 2026-02-17 10:00:01  process.started  role=worker pid=101
│   ├── [42] 2026-02-17 10:30:01  agent.started  chat_id=123 task_id=5 text="你好"
│   │   ├── [43] 2026-02-17 10:30:01  turn.started  model_name=gpt-4o
│   │   ├── [44] 2026-02-17 10:30:03  turn.completed  latency_ms=1820 input_tokens=42 output_tokens=7
│   │   └── [45] 2026-02-17 10:30:03  reply.sent  chat_id=123
│   └── [46] 2026-02-17 10:30:03  agent.completed  task_id=5
├── [5] 2026-02-17 10:30:05  worker.exited  exit_code=17 uptime_seconds=1804
├── [6] 2026-02-17 10:30:07  worker.spawned  pid=102
└── [7] 2026-02-17 10:30:07  process.started  role=worker pid=102
    ├── [50] 2026-02-17 10:31:00  agent.started  chat_id=123 task_id=6 text="谢谢"
    │   ├── [51] 2026-02-17 10:31:00  turn.started  model_name=gpt-4o
    │   ├── [52] 2026-02-17 10:31:02  turn.completed  latency_ms=1500 input_tokens=85 output_tokens=12
    │   └── [53] 2026-02-17 10:31:02  reply.sent  chat_id=123
    └── [54] 2026-02-17 10:31:02  agent.completed  task_id=6
```

使用 `-L 2` 时只展开到 depth=2：

```
[1] 2026-02-17 10:00:00  process.started  role=supervisor pid=100 version=abc123
├── [2] 2026-02-17 10:00:00  revision.promoted  revision=abc123
├── [3] 2026-02-17 10:00:01  worker.spawned  pid=101
├── [4] 2026-02-17 10:00:01  process.started  role=worker pid=101 [...]
├── [5] 2026-02-17 10:30:05  worker.exited  exit_code=17 uptime_seconds=1804
├── [6] 2026-02-17 10:30:07  worker.spawned  pid=102
└── [7] 2026-02-17 10:30:07  process.started  role=worker pid=102 [...]
```

有被截断子事件的节点后面标注 `[...]`。

格式：`[id] timestamp  event_type  key=value key=value ...`

payload 中的每个 key-value 平铺显示。长文本值（如 `text`、`error`）截断到 80 字符。

### JSON 格式（`--json`）

```json
{
  "id": 1,
  "timestamp": 1739781000,
  "event_type": "process.started",
  "payload": {"role": "supervisor", "pid": 100},
  "children": [
    {"id": 2, "timestamp": 1739781000, "event_type": "revision.promoted", ...},
    {
      "id": 4,
      "timestamp": 1739781001,
      "event_type": "process.started",
      "payload": {"role": "worker", "pid": 101},
      "children": [
        {"id": 42, "event_type": "agent.started", "children": [...]},
        {"id": 46, "event_type": "agent.completed", "children": []}
      ]
    },
    ...
  ]
}
```

使用 `-L` 时，超出深度的子树不包含在 `children` 中。

## 数据库访问

工具直接打开 SQLite 文件（只读模式），不需要运行中的 supervisor/worker 进程。

### 开发期间的使用方式

1. **容器内执行**：编译后在 Docker 容器内运行，DB 路径为容器内路径。
2. **宿主机执行**（推荐）：SQLite DB 文件在挂载卷上，宿主机可以直接访问。用 `--db` 指定宿主机路径即可。

```bash
# 宿主机上直接使用（打印最近 supervisor 的完整树）
go run ./cmd/event-tree --db /path/to/mounted/autonous.db

# 只看 supervisor + worker 层级
go run ./cmd/event-tree --db /path/to/mounted/autonous.db -L 2

# 查看某个 worker 的子树
go run ./cmd/event-tree --db /path/to/mounted/autonous.db --id 4

# 容器内使用
event-tree
```

SQLite 支持多读单写，只读打开不会与运行中的 supervisor/worker 冲突。

## 实现

### 文件结构

```
cmd/event-tree/main.go    # 入口、flag 解析、输出格式化
```

逻辑简单（单次 SQL 查询 + 内存建树 + 打印），不需要拆分多个文件。

### 核心逻辑

1. **查询**：找到 supervisor root，用递归 CTE 获取整棵子树。
2. **建树**：内存中用 `map[int64][]*Event` 按 `parent_id` 分组，递归构建树。
3. **截断**：如果指定了 `-L`，在构建/打印时按深度截断。
4. **打印**：深度优先遍历，用 `├──` / `└──` 前缀渲染。

```go
// 默认模式: 找到最近的 supervisor root
//   SELECT id FROM events WHERE event_type = 'process.started'
//     AND json_extract(payload, '$.role') = 'supervisor'
//     ORDER BY id DESC LIMIT 1

// --id 模式: 直接使用指定的 event ID 作为 root

// 获取子树（递归 CTE）:
//   WITH RECURSIVE subtree(id) AS (
//     SELECT id FROM events WHERE id = ?
//     UNION ALL
//     SELECT e.id FROM events e JOIN subtree s ON e.parent_id = s.id
//   )
//   SELECT * FROM events WHERE id IN (SELECT id FROM subtree)
//   ORDER BY id ASC

// -L 截断: 在内存建树时，跳过 depth > L 的节点
```

### 只读打开

```go
db, err := sql.Open("sqlite3", path+"?mode=ro&_journal_mode=WAL")
```

## 任务分解

- [ ] `cmd/event-tree/main.go`：flag 解析（`--id`、`-L`、`--db`、`--json`、`--no-payload`）、DB 只读打开、递归 CTE 查询、内存建树、深度截断、tree/JSON 输出。
- [ ] 测试：用内存 SQLite 插入样例事件（统一树结构），验证树构建、深度截断和输出格式。
