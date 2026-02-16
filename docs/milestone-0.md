# Milestone 0: 基础结构

## 目标 (Goal)
实现一个最小化的 `MVP`: `supervisor` + `worker` + `telegram` + `OpenAI API`。

## 可交付成果 (Deliverables)
- **Supervisor 功能**:
  - 生成 `worker` (Spawn worker)
  - 监控 `worker` (Monitor worker)
  - 重启 `worker` (Restart worker)
  - 记录 `worker` 的版本/构建 `ID` (Record worker version/build id)
- **Worker 功能**:
  - 拉取 `Telegram` 消息 (Pull Telegram messages)
  - 调用 `OpenAI API` (Call OpenAI API)
  - 将模型的响应发送回 `Telegram` (Send Model response to Telegram)
- **安全要求 (Security Requirements)**:
  - 密钥（例如, `API tokens`）必须通过**环境变量 (`ENVIRONMENT VARIABLES`)**注入。
  - 在开发过程中，密钥应在运行时注入到 `Docker` 容器中，而不是硬编码在 `Dockerfile` 或任何启动脚本里。
