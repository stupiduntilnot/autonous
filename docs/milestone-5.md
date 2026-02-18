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
  - 说明：M5 可支持“按钮触发的显式 approve”，但不允许无用户动作的自动批准

## 数据模型

### 事实层（已有）：`events`

- 继续复用现有 `events(id, timestamp, parent_id, event_type, payload)`
- 所有升级动作都必须写 `update.*` 事件
- `events` 作为审计真相（append-only），不直接承担 supervisor 在线决策

### 快照层（新增）：`artifacts`

一条记录表示一次升级事务的候选 artifact 当前态（M5 固定 `1 tx = 1 artifact`）。

建议字段：

- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `tx_id TEXT NOT NULL UNIQUE`（UUIDv4）
- `base_tx_id TEXT`（发起升级时的 active artifact 对应事务 ID）
- `bin_path TEXT NOT NULL`（例如 `/state/artifacts/<tx_id>/worker`）
- `sha256 TEXT`
- `git_revision TEXT`（构建时 HEAD，保留兼容追踪）
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
- `INDEX(status, updated_at)`
- `INDEX(base_tx_id)`

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

### 状态语义与执行者

- `created`（worker）: 事务已创建，尚未开始 build
- `building/testing/self_checking`（worker）: 正在执行对应阶段
- `staged`（worker）: 产物已落盘并通过本地门禁，等待批准
- `approved`（worker）: 已批准，等待 supervisor 在下个循环部署
- `deploying/deployed_unstable`（supervisor）: 正在/已完成切换，等待稳定性判定
- `promoted`（supervisor，终态）: 新版本确认稳定
- `*_failed/cancelled/rolled_back`（终态）: 失败或终止

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
- `deploying` 期间再次批准同一 `tx_id`

### 幂等规则

- `approve <tx_id>`：仅 `staged` 成功；其余状态返回 `already processed`
- supervisor deploy 抢占：
  - `UPDATE artifacts SET status='deploying' ... WHERE tx_id=? AND status='approved'`
- promote：仅 `deployed_unstable` 可进入 `promoted`

### 恢复规则（重启后，操作化）

- supervisor 启动时、拉起第一个 worker 之前执行一次进行态清理：
  - `building -> build_failed`
  - `testing -> test_failed`
  - `self_checking -> self_check_failed`
  - `deploying -> deploy_failed`
- 清理动作必须落对应 `update.*.failed` 事件
- `approved` 保留，等待下一次循环进入 deploy



## 部署触发模型（明确选型）

选用 Review 提议的 **方案 A**：

- worker 将 `staged -> approved` 后，回复用户并主动退出（建议退出码 `0`，后续如需可扩展专用码）
- supervisor 在每次 worker 退出后、下次拉起前，检查 `status='approved'` 并执行 deploy
- 不引入 supervisor 后台 goroutine，不引入 signal 协议

## Bootstrap 初始化

容器首次启动时执行一次 bootstrap：

1. `startup.sh` 构建初始 worker 到 `/state/artifacts/bootstrap/worker`
2. 创建 `/state/bin/worker.current` symlink 指向该文件
3. 若 `artifacts` 无 `promoted` 记录，写入一条基准记录：
   - `tx_id='bootstrap'`
   - `status='promoted'`
   - `base_tx_id=NULL`
4. supervisor `WORKER_BIN` 固定为 `/state/bin/worker.current`

## 升级流水线（基于 events + artifacts）

### 1) Generate Patch（worker）

- 生成候选改动
- 创建 `artifacts(status='created')`
- 写事件：`update.txn.created`

### 2) Build（worker）

- `created -> building`
- 执行：`go build -o /state/artifacts/<tx_id>/worker ./cmd/worker`
- 成功写 `sha256/git_revision` 并进入 `testing`；失败进入 `build_failed`
- 写事件：`update.build.started|completed|failed`

### 3) Test + Self-check（worker）

- 执行 `AUTONOUS_UPDATE_TEST_CMD`（默认 `go test ./...`）
- 执行 `AUTONOUS_UPDATE_SELF_CHECK_CMD`（可空）
- 成功进入 `staged`，失败进入 `test_failed/self_check_failed`
- 写事件：`update.test.*`、`update.self_check.*`、`update.artifact.staged`

### 4) Approve（worker，直接命令路径）

- 先做消息预处理：严格匹配 `^approve\\s+([a-f0-9-]{36})$`（UUIDv4）
- 命中则直接走 DB 状态机，不经过 LLM/tool loop
- `staged -> approved` 成功后回消息并主动退出 worker，触发 supervisor deploy 循环

### 4.1) Telegram 一键审批（Commander 交互）

- 通过 Telegram `InlineKeyboardMarkup` 渲染 `Yes/No`
- `callback_query` 映射为统一命令事件：
  - `Yes` -> `approve <tx_id>`
  - `No` -> `cancel <tx_id>`（`staged -> cancelled`）

边界约束：

- `telegram adapter` 只负责协议映射（按钮渲染、回调解析）
- `Commander` 暴露统一审批动作事件，不泄漏 Telegram payload
- `worker` 是唯一状态机执行者

### 5) Deploy（supervisor）

- 在 worker 退出后的 supervisor 循环中查询 `status='approved'`
- 条件更新到 `deploying` 后执行：
  - 复验 `sha256`（与 `artifacts.sha256` 必须一致）
  - 原子 symlink 切换到 candidate binary
- 成功：`deploying -> deployed_unstable`
- 失败：`deploying -> deploy_failed`
- 写事件：`update.deploy.started|completed|failed`

### 6) Promote / Rollback（supervisor）

- M5 采用**延迟判定语义**：在 worker 退出后，根据本次 uptime 与 crash 统计判定
- 当新版本达到稳定阈值：`deployed_unstable -> promoted`
- 若稳定窗口内触发 crash loop：`deployed_unstable -> rollback_pending -> rolled_back`
- 回滚目标为 `base_tx_id`（N-1）

## 原子 symlink 切换（实现约束）

```go
tmpLink := activeBin + ".tmp"
_ = os.Remove(tmpLink)
if err := os.Symlink(newBinPath, tmpLink); err != nil { return err }
if err := os.Rename(tmpLink, activeBin); err != nil { return err }
```

说明：`Rename` 替换的是 symlink 节点本身，满足 Unix 原子语义。

## 目录与版本追踪约定

- artifact root: `/state/artifacts`
- active worker: `/state/bin/worker.current`
- `revision.promoted` 在 M5 进入兼容模式：
  - artifact 成功 `promoted` 时可继续写 `revision.promoted`（payload 带 `git_revision`）
  - 新逻辑以 `artifacts` 为主，`CurrentGoodRev()` 后续迁移为 `CurrentGoodArtifact()`

## 与 M3 控制项的关系

- update pipeline（build/test/self-check/approve）属于 **direct command path**，不走常规 agent turn loop
- 因此不受 `AUTONOUS_CONTROL_MAX_WALL_TIME_SECONDS` 约束
- pipeline 单独使用 `AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS`（默认 `1800`）作为总超时

## 测试路径分层（开发 vs 探针）

- **开发路径（feature development）**：
  - 允许走正常 agent 路径，可调用 model（用于实现真实新功能）。
- **探针路径（deterministic probe）**：
  - 必须走 direct command path，不调用 LLM。
  - 用于验证“新 artifact 是否已部署并在运行”，保证结果可判定、可复现。

建议约定：

- 每次 task/fix 需要新建 artifact 时，新增一个探针命令（例如按字母递增）。
- 采用“随迭代增量增加 probe”的方式，而非预定义固定批次数量；这样更符合真实开发节奏，也能降低长期维护成本。
- 每个 probe 都应具备稳定输入输出（例如输入 `x` 返回 `X`），并写入对应 `events` 以便审计。

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
- `base_tx_id`
- `status_from`
- `status_to`
- `sha256`
- `duration_ms`
- `error`（失败时）

约束：

- 每次 `artifacts.status` 变更必须对应一条 `update.*` 事件
- 事件写入与 `artifacts` 更新放在同一 DB 事务

## 配置（新增 ENV，最小集）

- `AUTONOUS_UPDATE_ARTIFACT_ROOT`（默认 `/state/artifacts`）
- `AUTONOUS_UPDATE_ACTIVE_BIN`（默认 `/state/bin/worker.current`）
- `AUTONOUS_UPDATE_TEST_CMD`（默认 `go test ./...`）
- `AUTONOUS_UPDATE_SELF_CHECK_CMD`（默认空，空表示跳过）
- `AUTONOUS_UPDATE_PIPELINE_TIMEOUT_SECONDS`（默认 `1800`）
- `AUTONOUS_UPDATE_STABLE_WINDOW_SECONDS`（默认复用 `SUPERVISOR_STABLE_RUN_SECONDS`）

## 测试计划

### 单元测试

- 状态机合法/非法转移
- 幂等 approve/deploy（重复请求不重复执行）
- 进行态清理（startup cleanup）正确性
- deploy 前 SHA256 复验

### 集成测试

- bootstrap 初始化（首次 symlink + 基准 promoted 记录）
- worker：`created -> staged -> approved(并退出)`
- supervisor：`approved -> deploying -> deployed_unstable -> promoted`
- crash loop：`deployed_unstable -> rolled_back`

### E2E 测试

- 成功链路：build/test/self-check/approve/deploy/promote
- 失败链路：test failed 不可 deploy
- 回滚链路：部署后故障，自动回退到 `base_tx_id`
- 审计链路：`event-tree` 能按 `tx_id` 重建完整因果
- 探针链路：每次新 artifact 部署后，执行当轮新增 deterministic probe，验证无需 LLM 的确定性输出

## 任务分解

### 1. Schema 与仓储

- [DONE] 新增 `artifacts` schema 与索引
- [DONE] 实现 `artifacts` 状态转移仓储（含条件更新）
- [ ] 将状态更新与 `events` 写入封装到同一事务

### 2. Worker 流水线

- [DONE] 实现 build/test/self-check/stage
- [DONE] 实现 `approve <tx_id>` 直接命令路径（绕过 LLM）
- [DONE] 审批成功后主动退出 worker，触发 supervisor deploy 循环

### 3. Supervisor 部署与回滚

- [DONE] 启动阶段进行态清理
- [DONE] 实现 `approved` 事务消费与原子 deploy（含 SHA256 复验）
- [DONE] 实现 `deployed_unstable -> promoted` 延迟判定与自动回滚

### 4. 验收

- [DONE] `go build ./... && go test ./...` 全通过
- [DONE] 成功/失败/回滚三类 E2E 全通过
- [DONE] 文档与实现一致（`milestones.md`、`project.md` 索引可追踪）
