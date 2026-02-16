# `librarian` 概念

## **1. 概述 (Overview)**

与 `finder` 不同，`librarian` 在 `pi-mono` 框架中**不是一个单一、独立的工具或 sub-agent**，而是一个**核心概念**或**一组功能的集合**。它代表了框架中负责**会话管理 (Session Management)、上下文维护 (Context Maintenance) 和历史记录分析 (History Analysis)** 的一系列机制。

`librarian` 的核心目标是赋予 Agent “记忆”，使其能够：
*   在多次交互中保持对话的连续性。
*   管理和压缩日益增长的对话历史，以适应模型的 **context window** 限制。
*   支持对过去的对话进行回顾、分析，甚至从中“学习”。

## **2. 实现原理 (Implementation Principle)**

`librarian` 的功能主要通过以下几个核心机制实现：

*   **会话状态对象 (`Context` Object)**：`pi-mono` 框架的核心是 `Context` 对象。这个对象不仅仅包含当前的对话消息，还封装了整个会话的所有状态，包括：
    *   `messages`: 一个 `AgentMessage[]` 数组，存储了从对话开始到现在的每一条消息（包括 `user`, `assistant`, 和 `tool` 的消息）。这是 `librarian` 的核心数据结构。
    *   `system_prompt`: 系统级指令。
    *   `tools`: 当前会话可用的工具列表。
    *   其他元数据。

*   **历史记录的持久化 (Persistence of History)**：为了实现跨会话的记忆，`librarian` 的功能依赖于 `Context` 对象的序列化和反序列化。
    *   **序列化 (`Serialization`)**: 在一次会话结束或在关键步骤后，整个 `Context` 对象（或至少是 `messages` 数组）可以被序列化成一种持久化格式（如 JSON）。
    *   **存储 (`Storage`)**: 序列化后的数据可以被存储在本地文件系统、数据库或云存储中。这就是 Agent “记忆”的物理载体。
    *   **反序列化 (`Deserialization`)**: 当用户开始一个新的会话或希望继续之前的对话时，框架会从存储中读取相应的历史记录文件，将其反序列化回内存中的 `Context` 对象，从而恢复之前的对话状态。

*   **上下文压缩 (`Context Compaction`)**: 当对话历史变得很长，接近 `LLM` 的 **context window** 限制时，`librarian` 的一个关键高级功能被激活：上下文压缩。
    *   **触发机制**: 框架会监控 `messages` 数组的总 `token` 数量。
    *   **压缩策略**: 当 `token` 数量达到一个阈值（例如 **context window** 的 75%），框架会启动一个“压缩”过程。它会调用一个 `LLM`（可能是另一个专用的、低成本的模型），让其对对话历史的早期部分进行**总结 (Summarization)**。
    *   **替换**: 总结生成后，框架会用这个精简的总结替换掉多条原始的早期消息，从而有效缩短上下文长度，为新的对话腾出空间。

*   **对话分析与检索 (Conversation Analysis and Retrieval)**：虽然 `pi-mono` 目前的实现更侧重于管理，但 `librarian` 概念也为更高级的分析功能打下了基础。通过访问完整的 `messages` 历史，可以实现：
    *   **错误检测**: 分析 `tool` 调用的失败记录和 `assistant` 的修正行为，以识别 Agent 的常见错误模式。
    *   **模式识别**: 寻找用户与 Agent 交互的特定模式。
    *   **知识提取**: 从长对话中提取关键信息、决策和代码片段，形成一个浓缩的知识库。

## **3. 与 `pi-mono` 框架的交互 (Interaction with the `pi-mono` Framework)**

`librarian` 的功能是无缝集成在 `pi-mono` 的核心 `Agent` 工作流中的，它不是一个被显式调用的 `tool`。

1.  **消息追加**: 每次用户或 Agent 生成一条新消息，这条消息都会被追加到 `Context` 对象的 `messages` 数组中。
2.  **上下文构建**: 在每次调用 `LLM` 之前，框架会使用当前的 `Context.messages` 数组来构建发送给模型的 `prompt`。
3.  **自动压缩**: 在构建 `prompt` 的过程中，框架会检查 `token` 数量，并按需自动触发上面描述的上下文压缩逻辑。
4.  **持久化钩子 (`Hook`)**: 框架可能在会话的不同生命周期点（如会话结束、Agent 关键决策后）触发持久化 `hook`，将 `Context` 写入磁盘。

## **4. 源代码位置 (Source Code Location)**

`librarian` 的功能分散在 `pi-mono` 仓库的多个部分，因为它是一个核心概念。以下是一些关键的切入点：

*   **核心 Context 定义**:
    [https://github.com/badlogic/pi-mono/blob/main/packages/pi-ai/src/index.ts](https://github.com/badlogic/pi-mono/blob/main/packages/pi-ai/src/index.ts) (查找 `AgentContext` 和 `AgentMessage` 的定义)
*   **CLI 中会话管理**:
    [https://github.com/badlogic/pi-mono/tree/main/packages/pi-cli](https://github.com/badlogic/pi-mono/tree/main/packages/pi-cli) (该包负责处理用户交互、加载和保存会话状态)
*   **上下文压缩逻辑**:
    这部分逻辑可能在 `Agent` 的核心实现中，需要深入研究 `Agent` 如何准备 `prompt` 并调用 `LLM` 的代码。一个好的起点是研究 `pi-ai` 或 `coding-agent` 包中处理 `LLM` 调用的部分。
