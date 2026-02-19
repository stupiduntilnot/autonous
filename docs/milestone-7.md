# Milestone 7 计划: Bootstrap Workflow (Telegram-first, Extensible Skills)

## 核心目标

完成 bootstrap 闭环：用户只通过 Telegram 提需求与确认，Agent 在容器内完成设计收敛、实现、测试、提交与发布。

关键约束：

- **核心最小化**：系统核心不内置 GitHub 特定逻辑。
- **可扩展集成**：GitHub 等外部系统通过 skill 接入。
- **发布唯一性**：上线必须走 M5 的 `update stage -> approve -> deploy -> promote/rollback`。

**里程碑完成判定**：Milestone 7 验收通过后，项目视为“bootstrap 完成（bootstrap completed）”。

## 完成后的治理约束

- 自 Milestone 7 起，后续实现默认走 Telegram 驱动的标准 workflow。
- 禁止“未进入标准 workflow 的 ad-hoc 本地改动”作为常规开发方式。
- 外部平台（GitHub/GitLab/Jira）集成必须通过 skill，不得成为核心强依赖。

## 范围与非目标

### M7 范围

- Telegram 单聊输入与 `/dev` 单条激活
- Adapter 将消息统一映射为内部 `WorkItem`
- 队列入库后按 `mode` dispatch 到 `ChatWorkflow` / `DevWorkflow`
- Telegram 需求入口与多轮设计收敛
- 设计冻结后自动进入实现阶段
- 容器内 build/test 通过后 `commit + push`
- 触发并完成 M5 发布审批链路
- 定义并支持可选 skill 集成点（例如 GitHub skill）

### 非目标

- 核心内置 GitHub REST API adapter
- 自动合并 PR
- 多仓库联动发布

## 端到端行为定义（与验收一一对应）

1. 用户在 Telegram 提出新 feature 请求  
2. Agent 生成设计草案并回传给用户（可选：若启用 GitHub skill，则同步创建/更新 issue 并回传 issue URL）  
3. 用户与 Agent 多轮讨论，Agent 持续更新设计版本  
4. 用户确认后，任务进入 `design fixed`  
5. Agent 在 Docker 内调用 model + tools 实现功能  
6. 本地测试通过后，Agent 执行 `commit + push`  
7. Agent 触发新版本发布；Telegram 出现审批按钮；用户点击后收到最终状态报告

## 系统边界与职责

- `Supervisor`：只负责进程与部署状态，不解析需求语义
- `Worker`：负责消息 dispatch、设计收敛、实现/测试、提交推送、触发发布
- `Commander(Telegram)`：负责 Telegram 协议解析与命令/模式归一化
- `Skill Runtime`：加载并执行可选扩展（如 GitHub skill）

## Telegram Wrapper 设计（M7 关键）

单聊场景采用“默认 `chat` + `/dev` 单条激活”：

- 普通消息（如 `1+1=?`）默认走 `chat` 模式
- 仅当消息以 `/dev` 开头时（如 `/dev 实现...`），该条消息走 `dev` 模式
- `/dev` 为 one-shot，不改变后续消息默认模式

示例：

- A: `1+1=?` -> `chat workflow`
- B: `/dev 请实现...` -> `dev workflow`
- C: `2+2=?` -> `chat workflow`

## 当前实现现状与重构目标（M7 关键）

当前代码仍属于“单主流程 + direct command 特判”：

- 主流程：`processTask(...)`（普通消息 + model/tool loop）
- 特判分支：`processDirectCommand(...)`（`update stage` / `approve` / `cancel` / `rollback`）

这还不是清晰的双 Pipeline 架构。  
M7 必须完成重构为：

- `ChatWorkflow`：仅负责普通问答
- `DevWorkflow`：负责设计、实现、验证、发布编排
- `Dispatcher`：基于 `WorkItem.Mode` 进行统一路由

目标是“流程决策归程序编排，LLM 只负责实现内容”。

## Adapter 统一接口（M7 必做）

`Commander` 输出统一内部消息类型，不泄漏 Telegram 细节：

```go
type WorkMode string

const (
    WorkModeChat WorkMode = "chat"
    WorkModeDev  WorkMode = "dev"
)

type WorkItem struct {
    Mode       WorkMode
    Text       string
    ChatID     int64
    MessageID  int64
    ReceivedAt int64
    TraceID    string
}
```

Adapter 职责边界：

- 负责解析 Telegram update、识别 `/dev`、填充 `WorkItem`
- 不做 workflow 业务决策，不直接操作发布状态机

## 消息 Dispatch 与流转（M7 关键）

`WorkItem` 入队后由 dispatcher 路由：

- `mode=chat` -> `ChatWorkflow`（普通问答）
- `mode=dev` -> `DevWorkflow`（设计->实现->验证->发布）

约束：

- `DevWorkflow` 的 release 阶段必须由程序编排触发，不依赖 LLM 自主决定
- `local_validating` 通过后自动触发 M5 staging（等价内部调用 `update stage <tx_id>`）

## DevWorkflow 原子动作拆分（M7 必做）

为替代当前 direct command 散落逻辑，M7 需要抽象原子动作并在 `DevWorkflow` 编排：

- `ActionStageArtifact`
- `ActionRequestApproval`
- `ActionApprove`
- `ActionCancel`
- `ActionRollbackRequest`
- `ActionReleaseReport`

说明：

- `ActionApprove/ActionCancel` 由 Telegram callback 或命令触发
- `ActionStageArtifact` 由 `local_validating` 成功后自动触发，不依赖 LLM 决策
- `Supervisor` 继续负责 deploy/promote/rollback 执行，`DevWorkflow` 负责请求与状态推进

## 统一状态机（M7 workflow）

状态定义（单个 feature transaction）：

- `requested`
- `design_in_discussion`
- `design_fixed`
- `implementing`
- `local_validating`
- `pushed`
- `release_staged`
- `awaiting_approval`
- `released`（terminal state）
- `release_failed`（terminal state）
- `cancelled`（terminal state）

合法转移：

1. `requested -> design_in_discussion`
2. `design_in_discussion -> design_fixed | cancelled`
3. `design_fixed -> implementing`
4. `implementing -> local_validating`
5. `local_validating -> pushed | cancelled`
6. `pushed -> release_staged`（调用 M5 `update stage <tx_id>`）
7. `release_staged -> awaiting_approval`
8. `awaiting_approval -> released | release_failed | cancelled`

约束：

- 未 `design_fixed` 不得进入 `implementing`
- 未 `local_validating` 成功不得 `push`
- 未 `pushed` 不得进入发布阶段

## 数据与可观测性

延续 `events` + `artifacts`：

- `events` 记录 workflow 事实（append-only）
- `artifacts` 继续只负责发布产物状态（M5）

建议新增事件：

- `workflow.requested`
- `workflow.design.updated`
- `workflow.design.fixed`
- `workflow.implementation.started|completed|failed`
- `workflow.validation.started|completed|failed`
- `workflow.git.commit.created`
- `workflow.git.push.completed|failed`
- `workflow.release.triggered`
- `workflow.released|failed|cancelled`

可选 skill 事件（启用扩展时）：

- `workflow.skill.github.issue.created`
- `workflow.skill.github.issue.updated`
- `workflow.skill.github.issue.link_notified`

关键 payload：

- `feature_id`
- `design_version`
- `branch`
- `commit_sha`
- `tx_id`
- `error`
- `skill`（可选）
- `external_ref`（可选，如 `issue_url`）

## GitHub 作为可选 Skill（非核心）

设计原则：

- GitHub 能力不进入核心状态机，不影响核心 workflow 的可用性。
- GitHub skill 优先使用官方 `gh` CLI，不在核心中实现 GitHub REST API adapter。
- 未安装/未配置 `gh` 时，核心 workflow 仍可执行。

建议行为（启用 GitHub skill 时）：

- 在 `design_in_discussion` 阶段创建或更新 issue
- 在 Telegram 回传 issue URL
- 在 commit message 关联 issue（例如 `feat: xxx (#123)`）

## Telegram 交互设计

- 需求输入：自然语言 + 明确任务目标
- 模式规则：默认 `chat`，`/dev` 仅激活当前消息的 `dev` 模式
- 设计冻结命令：`design fix`（可带 `feature_id`）
- 发布审批：继续使用 M5 的 `Approve/Cancel` 按钮
- 结果回报：必须发最终状态消息（`released` / `release_failed` / `rolled_back`）

## 配置（新增 ENV，最小集）

核心：

- `AUTONOUS_WORKFLOW_REQUIRE_DESIGN_FIXED`（默认 `1`）

可选 GitHub skill（仅扩展使用）：

- `AUTONOUS_SKILL_GITHUB_ENABLED`（默认 `0`）
- `AUTONOUS_SKILL_GITHUB_REPO`
- `AUTONOUS_SKILL_GITHUB_DEFAULT_BRANCH`（默认 `main`）

说明：`gh` 登录凭据由运行环境提供（例如 `GH_TOKEN`），不作为核心强依赖。

## 失败处理

- 设计阶段失败：保持 `design_in_discussion`，允许重试或取消
- build/test 失败：进入 `workflow.validation.failed` 并附日志摘要
- push 失败：进入 `workflow.git.push.failed`，不得触发发布
- 发布失败：沿用 M5 失败与 rollback 机制
- skill 失败：记录 skill 失败事件，不阻断核心 workflow（除非配置为强制）

## 测试计划

### 单元测试

- workflow 状态机合法/非法转移
- `/dev` one-shot 解析（A/B/C 三消息序列）
- `Commander -> WorkItem` 映射正确性
- dispatcher 路由正确性（`chat` vs `dev`）
- `design fix` 命令解析与状态推进
- skill 开关启用/禁用行为

### 集成测试

- 从 `requested` 到 `pushed` 的核心全链路（无 GitHub skill）
- `pushed -> release_staged -> awaiting_approval -> released`
- push 失败与发布失败分支

### E2E

- 核心 E2E：纯 Telegram 驱动，不依赖 GitHub skill
  - A:`1+1=?` 走 chat
  - B:`/dev ...` 走 dev 并进入发布审批
  - C:`2+2=?` 自动回 chat
- 扩展 E2E：启用 GitHub skill 后，验证 `gh` 路径与 issue URL 回传

## 任务分解

### 1. Telegram + Adapter + Dispatch（M7 主线）

- [ ] 实现 Telegram `/dev` one-shot wrapper（默认 chat）
- [ ] 定义并落地 `WorkItem` 统一接口
- [ ] 实现队列后 dispatcher（`chat` / `dev` 路由）
- [ ] 增加 `workflow.dispatch.*` 与 `workflow.mode.*` 事件
- [ ] 将 `processTask + processDirectCommand` 重构为 `ChatWorkflow + DevWorkflow`

### 2. Workflow 基础

- [ ] 新增 workflow 状态机与持久化记录
- [ ] 定义 direct command：`design fix`
- [ ] 增加 `workflow.*` 事件

### 3. Worker 编排

- [ ] 需求触发设计草案生成与多轮更新
- [ ] 设计冻结后自动进入实现/测试
- [ ] 测试通过后执行 `commit + push`
- [ ] 成功后触发 M5 发布事务
- [ ] 将 `update stage/approve/cancel/rollback` 收敛为 `DevWorkflow` 原子动作

### 4. Skill 扩展点

- [ ] 定义 skill 接口与加载机制（核心无 GitHub 依赖）
- [ ] 实现可选 GitHub skill（基于 `gh` CLI）
- [ ] 失败降级策略：skill 异常不阻断核心 workflow

### 5. 可延后项（下个 Milestone 可接管）

- [ ] GitHub issue 模板细化与复杂同步策略
- [ ] 多前端接入（Slack/REST）具体 adapter 实现
- [ ] 高级并发调度（优先级/抢占/多任务编排）

### 6. 验收

- [ ] 单测与集成测试通过
- [ ] 核心 E2E 跑通你定义的 1~7 行为
- [ ] 扩展 E2E 验证 GitHub skill（可选）
- [ ] 文档与实现一致（`project.md` / `milestones.md` / `milestone-7.md`）
