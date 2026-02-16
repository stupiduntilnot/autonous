# `finder` Sub-agent

## **1. 概述 (Overview)**

`finder` 是一个为 `pi-mono` agent 设计的专用子代理（**sub-agent**）或工具，其核心职责是**在文件系统中高效地查找文件**。它扮演一个只读的“仓库侦察兵”角色，根据指定的模式（**pattern**）快速定位相关文件、目录或代码片段。

这个工具对于 Coding Agent 至关重要，因为它解决了在大型代码库中如何精确、高效地定位上下文信息的难题。Agent 不需要一次性读取大量不相关的文件，而是可以调用 `finder` 来获取一个精准的文件列表，从而将有限的 **context window** 资源集中在最相关的代码上。

## **2. 实现原理 (Implementation Principle)**

`finder` 的实现原理可以分解为以下几个关键点：

*   **底层依赖 (Underlying Dependency)**：`finder` 的核心功能是委托给一个用 Rust 编写的高性能命令行工具 `fd` (在某些系统上也称为 `fd-find`)。`fd` 是一个专门为速度优化的 `find` 命令替代品，它天生支持 `.gitignore` 规则、正则表达式，并且默认进行并行搜索。通过 `child_process` 执行 `fd` 命令，`finder` 继承了其全部优点。

*   **参数化与封装 (Parameterization and Encapsulation)**：`finder.ts` 文件将 `fd` 的命令行参数封装成一个结构化的 `tool` 定义。它定义了 Agent 可以使用的参数，主要包括：
    *   `pattern`: 一个 `glob` 模式，用于匹配文件名或路径（例如 `src/**/*.ts`, `*.md`）。这个参数会直接传递给 `fd`。
    *   `cwd` (Current Working Directory): 指定搜索的起始目录。Agent 可以指定在项目的某个子目录中进行搜索，默认为项目根目录。
    *   `limit`: 限制返回结果的数量，防止返回过多文件撑爆 `context`。

*   **忽略规则 (Ignore Rules)**：`fd` 默认会自动读取并应用 `.gitignore` 文件中的规则。这意味着 `finder` 在搜索时会智能地跳过 `node_modules`、`build`、`dist` 等被忽略的文件和目录，这对于在典型的软件项目中进行搜索至关重要。

*   **输出处理 (Output Processing)**：`finder` 执行 `fd` 命令后，会捕获其标准输出 (`stdout`)。`fd` 的输出是每行一个文件路径。`finder` 的代码会解析这个输出，将其转换成一个字符串数组（`string[]`），其中每个字符串就是一个文件的相对路径或绝对路径。这个数组最终作为工具的执行结果返回给 Agent。

*   **异步执行 (Asynchronous Execution)**：由于文件搜索可能是一个耗时操作，`finder` 的实现是异步的 (`async`)，它返回一个 `Promise`。这确保了在等待文件系统 I/O 时不会阻塞 Agent 的主进程。

## **3. 与 `pi-mono` 框架的交互 (Interaction with the `pi-mono` Framework)**

1.  **Tool 调用**: 当 `pi-mono` agent 需要在代码库中查找文件时，它会生成一个 `tool_call`，指定工具名称为 `find`，并提供相应的参数，例如：
    ```json
    {
      "tool_call": "find",
      "parameters": {
        "pattern": "**/*service.ts",
        "cwd": "packages/server/src"
      }
    }
    ```

2.  **执行与返回**: `pi-mono` 的 `Tool` 执行器会接收到这个调用，并执行 `finder` 的逻辑。`finder` 随即调用 `fd` 命令行工具。执行完毕后，它会将查找到的文件路径列表（字符串数组）作为结果返回给 agent。

3.  **上下文构建**: Agent 接收到文件列表后，可以根据任务需要，决定下一步是读取这些文件的全部内容、部分内容，还是基于这些文件路径继续提出问题。这样，`finder` 就成为了 agent 理解代码库结构、构建精准上下文的第一步。

## **4. 源代码位置 (Source Code Location)**

`finder` 的主要实现可以在 `pi-mono` 的主仓库中找到：

*   [https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/src/core/tools/find.ts](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/src/core/tools/find.ts)
