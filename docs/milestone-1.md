# Milestone 1 计划: 可观测性 (精简版 MVP)

本文档列出了完成 Milestone 1 的具体任务，重点是为一个统一的、但仍支持层级追踪的事件日志系统实现最简单的设计。

## 核心原则 (Core Principles)

- **统一事件日志 (Unified Event Log)**: 所有的系统事件都将被记录在单一的 `events` 表中。
- **层级化运行 ID (Hierarchical Run IDs)**: 使用一个通用的 `parent_run_id` 关系来追踪进程的层级结构。
- **极简 Schema (Minimalist Schema)**: `events` 表的 schema 将只包含追踪所需的最少列。更丰富的、非索引的元数据将被存储在 `payload` 中。

## 任务分解 (Task Breakdown)

### 1. 实现统一的 `events` 表
- [ ] **创建 Schema (Create Schema)**: 在一个共享的数据库模块中，使用最精简的 schema 来定义 `events` 表。这个 schema 将取代旧的 `task_audit` 表。
  ```sql
  CREATE TABLE events (
      id INTEGER PRIMARY KEY,
      timestamp INTEGER NOT NULL,
      run_id INTEGER NOT NULL,
      parent_run_id INTEGER, -- 根进程 (supervisor) 的此字段为 NULL
      event_type TEXT NOT NULL,
      payload TEXT -- 包含所有其他事件特定细节的 JSON 对象
  );
  ```
- [ ] **创建索引 (Create Indexes)**: 在 `timestamp`, `run_id`, `parent_run_id` 和 `event_type` 上添加索引以保证查询性能。

### 2. 实现层级化 ID 生成
- [ ] **进程启动逻辑 (Process Startup Logic)**:
    - **记录启动事件 (Log Startup Event)**: 任何进程（Supervisor 或 Worker）启动时的第一个动作，就是记录一个 `process.started` 事件。这个事件的 `payload` 至关重要，应包含其身份元数据，例如它的概念性 `role` (角色，如 "supervisor", "worker")、一个长生命周期的 `instance_id` (实例ID，如果适用)、它的 `pid` 和代码 `version`。
    - **获取 `run_id` (Adopt `run_id`)**: 从这个启动事件中检索数据库自动生成的 `id`，并将其作为该进程自身的 `run_id` 保存在内存中。
- [ ] **传递逻辑 (Propagation Logic)**:
    - 当一个父进程启动一个子进程时，它必须通过环境变量（例如 `PARENT_RUN_ID`）将自身的 `run_id` 传递给子进程。
    - 子进程将读取这个值，并在记录自己的启动事件时，将其用作 `parent_run_id` 的值。

### 3. 重构日志记录以使用新 Schema
- [ ] **创建统一的 Logger (Create Unified Logger)**: 创建一个 `log_event` 函数，它接受 `run_id`, `parent_run_id`, `event_type` 和一个可序列化的 `payload` 作为参数，然后向 `events` 表中插入一条新记录。
- [ ] **记录所有事件 (Log All Events)**: 重构 supervisor 和 worker，使用新的 `log_event` 函数来记录所有重要事件（`worker.spawned`, `task.claimed`, `llm_call.completed` 等）。
- [ ] **记录模型调用指标 (Log Model Call Metrics)**: 对于 `llm_call.completed` 事件，其 `payload` 必须包含 `model_name`, `latency_ms`, `input_tokens` 和 `output_tokens`。这需要集成一个计时器和一个 `tokenizer`。

### 4. 从事件中派生状态
- [ ] **更新状态逻辑 (Update State Logic)**: 修改 supervisor 的逻辑，通过查询 `events` 表来派生状态，而不是依赖一个独立的键值状态表。
    - **`worker_instance_seq`**: 这个序列号现在将通过 `COUNT` 计算 `parent_run_id` 为当前 supervisor `run_id` 的 `worker.started` 事件的数量来得出。
    - **`current_good_rev`**: 这个值将通过查找最近的 `revision.promoted` 事件的 `payload` 中的 `revision` 来得出。
