# Final Plan for Milestone 1: Observability (v3 - Hierarchical)

This document lists the concrete tasks for Milestone 1, updated with a future-proof design for hierarchical workers.

## Core Principles

- **Unified Event Log**: All events for auditing, debugging, tracing, and statistics will be recorded as rows in a single `events` table.
- **Hierarchical Run IDs**: A generic `parent_run_id` relationship will be used to model the process hierarchy, allowing for arbitrary nesting of workers/supervisors in the future. A `run_id` will be a short, sequential integer for token efficiency.
- **Event Naming Convention**: Event types will use dot-notation for namespacing (e.g., `llm_call.started`) and underscores for multi-word components (`llm_call`).

## Task Breakdown

### 1. Implement Unified `events` Table
- [ ] **Create Schema**: In a shared database module, define the `events` table schema using the generic parent-child relationship.
  ```sql
  CREATE TABLE events (
      id INTEGER PRIMARY KEY,        -- Unique ID for the event itself
      timestamp INTEGER NOT NULL,

      -- IDs for tracing process hierarchy
      run_id INTEGER NOT NULL,   -- The run_id of the process that EMITTED this event
      parent_run_id INTEGER,     -- The run_id of the process that SPAWNED the emitter (NULL for root)

      -- Metadata about the emitter
      instance_id TEXT NOT NULL, -- The long-lived name, e.g., "supervisor-main", "W00001"
      role TEXT NOT NULL,        -- The type of actor, e.g., "orchestrator", "supervisor", "worker"

      -- Event details
      event_type TEXT NOT NULL,  -- e.g., "process.started", "llm_call.completed", "revision.promoted"
      payload TEXT               -- JSON object with event-specific details
  );
  ```
- [ ] **Create Indexes**: Add indexes on `timestamp`, `run_id`, `parent_run_id`, and `event_type` to ensure query performance.

### 2. Implement Hierarchical ID Generation and Propagation
- [ ] **Process Startup Logic**:
    - **Abstract Startup**: Create a common startup sequence for any process (Supervisor, Worker).
    - **Log Startup Event**: On startup, the first action is to log a `process.started` event. The `payload` should contain its `version`, `pid`, and any other static info.
    - **Adopt `run_id`**: Retrieve the auto-generated `id` from this startup event and hold it in memory as the process's own `run_id`.
- [ ] **Propagation Logic**:
    - When a parent process (e.g., Supervisor) spawns a child (e.g., Worker), it must pass its own `run_id` to the child via an environment variable (e.g., `PARENT_RUN_ID`).
    - The child process will read this `PARENT_RUN_ID` and use it as the `parent_run_id` value when logging its own `process.started` event.
    - **Design Note**: Using environment variables is preferred over command-line arguments for passing this kind of data. It aligns with the [Twelve-Factor App](https://12factor.net/config) methodology, which treats configuration (like a parent's ID) as separate from the invocation command. This makes services more portable and easier to manage in different deployment environments.
- [ ] **Instance & Role IDs**:
    - Each process must have an `instance_id` (a long-lived name, e.g., from an env var `INSTANCE_ID=supervisor-main` or generated sequentially like `W00001`) and a `role` (a hardcoded string like `"supervisor"` or `"worker"`). These must be logged with every event.

### 3. Refactor Logging to Use New Schema
- [ ] **Create Unified Logger**: The `log_event` function will now take `run_id`, `parent_run_id`, `instance_id`, `role`, `event_type`, and a `payload`.
- [ ] **Log Worker Events**: Refactor the worker to use the new logger for all events (e.g., `task.claimed`, `task.done`).
- [ ] **Log Model Call Events**: Instrument `call_openai` to log an `llm_call.completed` event. The `payload` must include `model_name`, `latency_ms`, `input_tokens`, and `output_tokens`.
- [ ] **Log Supervisor Events**: The supervisor will use the same `log_event` function to log its actions (e.g., `worker.spawned`, `worker.crashed`). A `revision.promoted` event's `payload` will contain the `revision` SHA to link to the `supervisor_revisions` table.

### 4. Context Accounting
- [ ] **Log Context Payload**: As part of an `llm_call.started` event, use a tokenizer to calculate and log the token breakdown of the context in the `payload` JSON.
