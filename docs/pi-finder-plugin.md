# pi-mono Sub-agent: `finder` 深度解析

## 1. 概述 (Overview)

`finder` 是一个为 `pi-mono` agent 设计的专用 **sub-agent** 或工具，其核心职责是**在本地文件系统中高效地查找文件**。它扮演一个只读的“仓库侦察兵”角色，根据指定的模式（**pattern**）快速定位相关文件、目录或代码片段。

这个工具对于 Coding Agent 至关重要，因为它解决了在大型代码库中如何精确、高效地定位上下文信息的难题。Agent 不需要一次性读取大量不相关的文件，而是可以调用 `finder` 来获取一个精准的文件列表，从而将有限的 **context window** 资源集中在最相关的代码上。

## 2. 实现原理 (Implementation Principle)

`finder` 的实现原理可以分解为以下几个关键点：

*   **底层依赖 (Underlying Dependency)**：`finder` 的核心功能是委托给一个用 Rust 编写的高性能命令行工具 `fd` (在某些系统上也称为 `fd-find`)。`fd` 是一个专门为速度优化的 `find` 命令替代品，它天生支持 `.gitignore` 规则、正则表达式，并且默认进行并行搜索。`finder` 通过 `child_process` 执行 `fd` 命令，捕获其标准输出，从而继承了 `fd` 的全部优点。

*   **参数化与封装 (Parameterization and Encapsulation)**：`finder` 工具将 `fd` 的命令行参数封装成一个结构化的 `tool` 定义，供 Agent 调用。它定义了 Agent 可以使用的参数，主要包括：
    *   `pattern`: 一个 `glob` 模式或正则表达式，用于匹配文件名或路径（例如 `src/**/*.ts`, `*.md`）。这个参数会直接传递给 `fd` 进行搜索。
    *   `cwd` (Current Working Directory): 指定搜索的起始目录。Agent 可以指定在项目的某个子目录中进行搜索，默认为当前工作目录。
    *   `limit`: 限制 `fd` 命令返回结果的最大数量，防止返回过多文件占用过多 `context`。

*   **忽略规则 (Ignore Rules)**：`fd` 默认会自动读取并应用搜索目录及其父目录中的 `.gitignore` 文件规则。这意味着 `finder` 在搜索时会智能地跳过 `node_modules`、`build`、`dist` 等通常不希望被搜索到的文件和目录，这对于在典型的软件项目中进行搜索至关重要，能大大减少噪音。

*   **输出处理 (Output Processing)**：`finder` 执行 `fd` 命令后，会捕获其标准输出 (`stdout`)。`fd` 的输出格式通常是每行一个匹配的文件路径。`finder` 的代码会解析这个输出，将其分割成一个字符串数组（`string[]`），其中每个字符串就是一个文件的相对路径或绝对路径。这个数组最终作为工具的执行结果返回给 Agent。

*   **异步执行 (Asynchronous Execution)**：由于文件系统 I/O 操作可能耗时，`finder` 的执行是异步的。它通常会返回一个 `Promise`，确保在等待文件搜索结果时不会阻塞 Agent 的主进程，从而保持 Agent 的响应性。

## 3. 与 `pi-mono` 框架的交互 (Interaction with the `pi-mono` Framework)

`finder` 与 `pi-mono` 框架的交互遵循 **Tool Use** 模式：

1.  **Tool 调用 (Tool Call)**: 当 `pi-mono` agent 在执行任务时需要查找文件，它会生成一个 `tool_call` 请求。这个请求会指定工具名称为 `find`，并提供所需的参数，例如：
    ```json
    {
      "tool_call": "find",
      "parameters": {
        "pattern": "**/*service.ts",
        "cwd": "packages/server/src",
        "limit": 10
      }
    }
    ```
2.  **执行与结果返回 (Execution and Result Return)**: `pi-mono` 框架的 **Tool Executor** 会接收到这个 `tool_call`，并执行 `finder` sub-agent 内部的逻辑（即调用 `fd` 命令行工具）。`finder` 执行完毕后，会将查找到的文件路径列表（字符串数组）作为 `tool_result` 返回给 `pi-mono` agent。
3.  **上下文构建与下一步决策 (Context Building and Next Step Decision)**: Agent 接收到文件列表后，会将这些文件路径纳入其 **context window**。基于这些路径，Agent 可以决定下一步操作，例如使用 `read_file` tool 读取这些文件的内容，或者进一步细化搜索条件。这样，`finder` 就成为了 Agent 理解代码库结构、构建精准上下文、以及做出后续决策的关键辅助工具。

## 4. 源代码位置 (Source Code Location)

`finder` sub-agent 的源代码可以在以下 GitHub 仓库中找到：

*   **GitHub 仓库**: [https://github.com/default-anton/pi-finder](https://github.com/default-anton/pi-finder)
*   在该仓库中，你可以找到其 `TypeScript` 实现和 `package.json` 等配置信息。
