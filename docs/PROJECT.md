# Autonous 项目文档

更新时间：2026-02-15

## 1. 项目定位
`autonous` 是一个通过 Telegram 远程驱动的 autonomous coding system。

当前阶段目标：
1. 先把 runtime 做稳（Supervisor + Worker）。
2. 保证状态可恢复（SQLite）。
3. 在最小复杂度下持续演进能力（incremental evolution）。

## 2. 从起点到现在的关键决策

## 2.1 Bootstrap 策略
决策：从最小可运行环路开始，不做大而全设计。
理由：先验证闭环，再迭代扩展，排障成本最低。

## 2.2 Telegram 作为第一控制面
决策：先打通 Telegram Bot API，再容器化。
理由：把 API 问题和容器问题分离，验证链路更快。

## 2.3 Secret 管理
决策：Secret 放在 host 环境（如 `~/.env`），运行时注入 container，不进 git。
理由：降低泄漏风险，便于后续新增 keys。

## 2.4 Runtime 选择
决策：容器 runtime 采用 Docker-compatible 方案（OrbStack 可用）；base image 用 `debian:bookworm-slim`。
理由：工具链稳定、兼容性好、维护成本低。

## 2.5 语言选择
决策：核心 runtime 先用 Rust。
理由：强类型 + 编译反馈 + 可交付单一 binary，适合自演化系统的稳定内核。

## 2.6 持久化选择
决策：SQLite 作为 runtime source of truth。
理由：本地单文件、零运维、事务可靠，适合 MVP 到中期扩展。

## 2.7 架构切分
决策：拆成 `autonous-supervisor` 和 `autonous-worker`。
理由：把 lifecycle control 和 task execution 解耦。

## 2.8 Supervisor 硬边界
决策：Supervisor 不接触 prompt/message 内容。
理由：控制平面必须保持中立、稳定、可验证。

## 2.9 先做 Dummy Worker 再做真实 Worker
决策：先用 deterministic crash-test（每 2 条消息自杀）验证重启链路。
理由：先证明 lifecycle 可靠，再接入真实模型调用。

## 2.10 Deployment 稳定化
决策：增加 `scripts/redeploy_autonous.sh`，统一“删旧容器 -> build -> 启动 -> 清理”。
理由：避免新旧 image/container 混用导致的调试噪声。

## 2.11 从 local Codex CLI 转向 OpenAI API
决策：Worker 不依赖本地 Codex CLI，直接调用 ChatGPT/OpenAI API。
理由：降低 sandbox/CLI 进程耦合，行为更可控。

## 2.12 Queue-first 执行模型
决策：采用 SQLite inbox queue + single consumer 串行执行。
理由：有序、可恢复、可审计。

## 2.13 Pending 策略
决策：cold start 才应用 pending filter；restart 依赖 offset/queue 续跑。
理由：兼顾防洪峰和不丢消息。

## 3. 当前架构（Current Baseline）

## 3.1 组件
1. `autonous-supervisor`
- 负责 process lifecycle：start/restart/crash-loop detection/rollback hook。

2. `autonous-worker`
- 负责 Telegram ingress、queue processing、OpenAI API 调用、结果回发。

## 3.2 责任边界
1. Telegram 仅与 Worker 交互。
2. Supervisor 不读取、不解析、不存储 prompt/message。
3. Worker 承担全部业务语义与模型调用。

## 3.3 数据模型（SQLite）
1. `kv`
- 系统游标与配置（如 `telegram_offset`）。

2. `history`
- 对话历史（`chat_id`, `role`, `text`, `created_at`）。

3. `inbox`
- 任务队列（`update_id unique`, `status`, `attempts`, `error` 等）。

4. `supervisor_state` / `supervisor_revisions`
- 生命周期与 revision 状态。

## 3.4 执行流
1. Worker 拉取 Telegram updates。
2. 写入 `inbox`（去重）。
3. 串行 claim 一条任务（single consumer）。
4. 构造上下文并调用 OpenAI API。
5. 发送 Telegram 回复。
6. `inbox` 标记 done/failed，并记录错误。

## 4. 与 `pi-mono` 思路对齐点
1. 事件/消息采用 queue 驱动，避免并发乱序。
2. source-of-truth 必须持久化，可重放。
3. 运行时 context 可裁剪，历史记录可追溯。

当前差异：
- `autonous` 先聚焦 Telegram + SQLite + Rust minimal runtime，不引入更重的多模块体系。

## 5. 配置约定（关键环境变量）
1. Telegram
- `TELEGRAM_BOT_TOKEN`
- `TG_TIMEOUT`
- `TG_SLEEP_SECONDS`
- `TG_DROP_PENDING`
- `TG_PENDING_WINDOW_SECONDS`
- `TG_PENDING_MAX_MESSAGES`

2. OpenAI
- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `OPENAI_CHAT_COMPLETIONS_URL`
- `WORKER_SYSTEM_PROMPT`

3. Runtime
- `TG_DB_PATH`（默认 `/state/agent.db`）
- `WORKER_SUICIDE_EVERY`（默认 0）

## 6. 当前阶段结论
1. Supervisor/Worker 分层已成立并验证。
2. Worker 已切换到 OpenAI API 执行路径。
3. 串行 queue 模型已落地（SQLite inbox）。
4. 系统进入“可持续扩展”的下一阶段。

## 7. 下一阶段建议
1. 引入 retry policy（指数退避 + 最大重试）。
2. 给 `inbox` 增加 dead-letter 语义（长期失败隔离）。
3. 增加 queue/latency/error 的可观测指标与运维命令。
