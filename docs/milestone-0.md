# [已完成] Milestone 0: 基础结构

## 目标 (Goal)
实现一个最小化的 `MVP`: `supervisor` + `worker` + `telegram` + `OpenAI API`。

## 可交付成果 (Deliverables)
- [已完成] **Supervisor 功能**:
  - [已完成] 生成 `worker` (Spawn worker)
  - [已完成] 监控 `worker` (Monitor worker)
  - [已完成] 重启 `worker` (Restart worker)
  - [已完成] 记录 `worker` 的版本/构建 `ID` (Record worker version/build id)
- [已完成] **Worker 功能**:
  - [已完成] 拉取 `Telegram` 消息 (Pull Telegram messages)
  - [已完成] 调用 `OpenAI API` (Call OpenAI API)
  - [已完成] 将模型的响应发送回 `Telegram` (Send Model response to Telegram)
- [已完成] **安全要求 (Security Requirements)**:
  - [已完成] 密钥（例如, `API tokens`）必须通过**环境变量 (`ENVIRONMENT VARIABLES`)**注入。
  - [已完成] 在开发过程中，密钥应在运行时注入到 `Docker` 容器中，而不是硬编码在 `Dockerfile` 或任何启动脚本里。
