# Milestone 5 计划: Self-Update Transaction

## 核心目标

在现有 `Supervisor + Worker + SQLite` 架构上实现可审计、可回滚的自更新事务流程，使系统可以安全地从“生成改动”走到“部署新 worker”。

Milestone 5 必须覆盖：

- 升级流水线：`generate patch -> build -> test/self-check -> stage artifact -> approve -> deploy`
- 产物管理：持久化 artifact 元数据（`sha256`、构建时间、测试结果、当前状态）
- 回滚机制：`supervisor` 保留 `N-1` last-known-good artifact，崩溃时自动回退

## 核心原则

- **Facts + Projection**: `events` 只追加事实；`artifacts` 保存运行时决策所需当前态。
- **State-machine driven**: 任何升级动作都必须满足状态机合法转移。
- **Supervisor neutrality**: `supervisor` 不理解 prompt，只依据 DB 状态执行 deploy/rollback。
- **Artifact immutability**: 已 `staged` 的二进制内容不可变；状态可推进，不可回写历史。
- **Crash-safe recovery**: 进程重启后仅通过 DB 恢复，不依赖内存上下文。

## 范围与非目标

### Milestone 5 范围

- 用现有 `events` 表记录完整升级事实链
- 新增单表 `artifacts` 作为当前态快照
- worker 完成 build/test/self-check/stage/approve
- supervisor 完成 deploy/promote/rollback

### 非目标（后续 milestone）

- 多环境发布（canary/blue-green）
- 远程 artifact 仓库
- 自动审批策略（本里程碑必须显式 approve）

## 数据模型

### 事实层（已有）：`events`

- 继续复用现有 `events(id, timestamp, parent_id, event_type, payload)`
- 所有升级动作都必须写 `update.*` 事件
- `events` 作为审计真相（append-only），不直接承担 supervisor 在线决策

### 快照层（新增）：`artifacts`

一条记录表示一次候选升级事务与其产物当前态（M5 约束：`1 tx -> 1 candidate artifact`）。

建议字段：

- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `tx_id TEXT NOT NULL UNIQUE`
- `artifact_id TEXT NOT NULL UNIQUE`
- `base_artifact_id TEXT`（发起升级时的 active artifact）
- `bin_path TEXT NOT NULL`（例如 `/state/artifacts/<artifact_id>/worker`）
- `sha256 TEXT`
- `build_started_at INTEGER`
- `build_finished_at INTEGER`
- `test_summary TEXT`（JSON）
- `self_check_summary TEXT`（JSON）
- `approval_chat_id INTEGER`
- `approval_message_id INTEGER`
- `deploy_started_at INTEGER`
- `deploy_finished_at INTEGER`
- `status TEXT NOT NULL`
- `last_error TEXT`
- `created_at INTEGER NOT NULL DEFAULT (unixepoch())`
- `updated_at INTEGER NOT NULL DEFAULT (unixepoch())`

建议索引：

- `UNIQUE(tx_id)`
- `UNIQUE(artifact_id)`
- `INDEX(status, updated_at)`
- `INDEX(base_artifact_id)`

`status` 枚举：

- `created`
- `building`
- `build_failed`
- `testing`
- `test_failed`
- `self_checking`
- `self_check_failed`
- `staged`
- `approved`
- `deploying`
- `deployed_unstable`
- `promoted`
- `rollback_pending`
- `rolled_back`
- `deploy_failed`
- `cancelled`

## 状态机（定义明确）

### 状态语义

- `created`: 事务已创建，尚未开始 build
- `building/testing/self_checking`: 正在执行对应阶段
- `staged`: 产物已落盘并通过本地门禁，等待用户批准
- `approved`: 用户已批准，可由 supervisor 部署
- `deploying`: supervisor 正在切换 active binary
- `deployed_unstable`: 已切换并启动新 worker，等待稳定窗口
- `promoted`: 稳定窗口通过，成为新的 good artifact（终态）
- `*_failed` / `cancelled` / `rolled_back`: 失败或终止终态

### 合法转移

1. `created -> building`
2. `building -> testing | build_failed`
3. `testing -> self_checking | test_failed`
4. `self_checking -> staged | self_check_failed`
5. `staged -> approved | cancelled`
6. `approved -> deploying`
7. `deploying -> deployed_unstable | deploy_failed`
8. `deployed_unstable -> promoted | rollback_pending`
9. `rollback_pending -> rolled_back`

### 非法转移（必须拒绝）

- 任意 `*_failed/promoted/cancelled/rolled_back` 终态再进入非终态
- 未经 `approved` 直接 `deploying`
- 未经 `staged` 直接 `approved`
- 在 `deploying` 期间再次 `approve` 同一 `tx_id`

### 幂等规则

- `approve <tx_id>`：仅 `staged` 可成功；对 `approved/deploying/...` 返回“already processed”
- supervisor deploy：仅消费 `status='approved'`，并通过条件更新抢占：
  - `UPDATE artifacts SET status='deploying' ... WHERE tx_id=? AND status='approved'`
- promote：仅 `deployed_unstable` 可进入 `promoted`

### 恢复规则（重启后）

- `building/testing/self_checking/deploying` 视为“中断中的进行态”：
  - worker/supervisor 重启后可按超时策略置为对应 `*_failed` 或继续执行
- `approved` 永不丢失：supervisor 重启后继续可见并可部署
- `deployed_unstable` 在稳定窗口未达标且发生 crash loop 时进入 `rollback_pending`

## 升级流水线（基于 events + artifacts）

### 1) Generate Patch（worker）

- 生成候选改动，创建 `artifacts(tx_id, artifact_id, status='created')`
- 写事件：`update.txn.created`

### 2) Build（worker）

- `created -> building`
- 执行：`go build -o /state/artifacts/<artifact_id>/worker ./cmd/worker`
- 成功写 `sha256`，进入 `testing`；失败进入 `build_failed`
- 写事件：`update.build.started|completed|failed`

### 3) Test + Self-check（worker）

- 执行 `go test ./...` 与自检命令
- 成功进入 `staged`，失败进入 `test_failed/self_check_failed`
- 写事件：`update.test.*`、`update.self_check.*`、`update.artifact.staged`

### 4) Approve（worker）

- 接收 `approve <tx_id>`
- 仅 `staged -> approved`
- 写入审批来源字段（chat/message）
- 写事件：`update.approved`

### 5) Deploy（supervisor）

- 查询 `status='approved'` 记录
- 条件更新到 `deploying` 后执行原子 symlink 切换到 candidate
- 成功：`deploying -> deployed_unstable`
- 失败：`deploying -> deploy_failed`
- 写事件：`update.deploy.started|completed|failed`

### 6) Promote / Rollback（supervisor）

- 新 worker 运行达到稳定阈值：
  - `deployed_unstable -> promoted`
  - 写 `update.promoted`
- 若稳定窗口内触发 crash loop：
  - `deployed_unstable -> rollback_pending -> rolled_back`
  - active binary 回退到 `base_artifact_id`（N-1）
  - 写 `rollback.attempted` + `update.rollback.completed|failed`

## 目录与部署约定

- artifact root: `/state/artifacts`
- active worker 入口：`/state/bin/worker.current`（symlink）
- supervisor 固定执行：`WORKER_BIN=/state/bin/worker.current`
- symlink 切换必须原子化（临时链接 + `rename`）

## 事件设计

新增事件类型建议：

- `update.txn.created`
- `update.build.started|completed|failed`
- `update.test.started|completed|failed`
- `update.self_check.started|completed|failed`
- `update.artifact.staged`
- `update.approved`
- `update.deploy.started|completed|failed`
- `update.promoted`
- `update.rollback.started|completed|failed`

关键 payload 字段：

- `tx_id`
- `artifact_id`
- `base_artifact_id`
- `status_from`
- `status_to`
- `sha256`
- `duration_ms`
- `error`（失败时）

要求：

- 每次 `artifacts.status` 变更必须对应一条 `update.*` 事件
- 事件写入与 `artifacts` 更新放在同一 DB 事务

## 配置（新增 ENV，最小集）

- `AUTONOUS_UPDATE_ARTIFACT_ROOT`（默认 `/state/artifacts`）
- `AUTONOUS_UPDATE_ACTIVE_BIN`（默认 `/state/bin/worker.current`）
- `AUTONOUS_UPDATE_TEST_CMD`（默认 `go test ./...`）
- `AUTONOUS_UPDATE_SELF_CHECK_CMD`（默认空，空表示跳过）
- `AUTONOUS_UPDATE_STABLE_WINDOW_SECONDS`（默认复用 supervisor stable run，必要时单独配置）

## 失败处理策略

- `build/test/self-check` 失败：进入对应 `*_failed` 终态，不允许 deploy
- deploy 失败：`deploy_failed`，保持当前 active artifact 不变
- promote 前 crash loop：执行 rollback 到 `base_artifact_id`，落 `rolled_back`
- 回滚失败：记录 `update.rollback.failed` 并保持 `rollback_pending` 供人工介入

## 测试计划

### 单元测试

- 状态机合法/非法转移
- 幂等 approve/deploy（重复请求不重复执行）
- `N-1` 选择与 rollback 目标解析

### 集成测试

- worker：`created -> staged`
- supervisor：`approved -> deployed_unstable -> promoted`
- crash loop：`deployed_unstable -> rolled_back`

### E2E 测试

- 成功链路：build/test/self-check/approve/deploy/promote
- 失败链路：test failed 不可 deploy
- 回滚链路：部署后故障，自动回退到 `base_artifact_id`
- 审计链路：`event-tree` 能按 `tx_id` 重建完整因果

## 任务分解

### 1. Schema 与仓储

- [ ] 新增 `artifacts` schema 与索引
- [ ] 实现 `artifacts` 状态转移仓储（含条件更新）
- [ ] 将状态更新与 `events` 写入封装到同一事务

### 2. Worker 流水线

- [ ] 实现 build/test/self-check/stage
- [ ] 实现 `approve <tx_id>`（仅 `staged` 可批准）
- [ ] 写入完整 `update.*` 事件

### 3. Supervisor 部署与回滚

- [ ] 实现 `approved` 事务消费与原子 deploy
- [ ] 实现 `deployed_unstable -> promoted` 稳定窗口判定
- [ ] 实现 crash loop 自动回滚到 `base_artifact_id`

### 4. 验收

- [ ] `go build ./... && go test ./...` 全通过
- [ ] 成功/失败/回滚三类 E2E 全通过
- [ ] 文档与实现一致（`milestones.md`、`project.md` 索引可追踪）
