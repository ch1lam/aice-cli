# AICE 架构纠偏计划

## 目标

AICE 不是 Pi 的 Go 目录翻译版，而是遵循同一组产品与运行时不变量的纯 Go 实现：

- 开箱即用、自带电池，默认能力足以覆盖大多数编码任务。
- provider、工具、TUI、持久化、执行环境及未来扩展通过明确边界可替换。
- Agent 内核保持小、显式、可测试，不引入框架式抽象。
- 默认使用启动 AICE 进程的宿主权限；权限策略和真正隔离由容器、沙箱或扩展负责。
- Session 保存无损事实，发送给模型的 context 和显示给用户的 viewport 都只是派生视图。

本计划只纠正已经出现的实质偏差，并为尚未实现的能力规定顺序。未实现 Session、沙箱或扩展不等于已经偏离。

## 对照基线

- AICE 分支：`refactor/pi-go`
- AICE 计划前基线：`baff151`
- Pi 源码：`/Users/chilam/code/pi`
- Pi 对照提交：`9b3a2059171bcc74ad9d2cadeea6d186776cf2db`

Pi 是语义和设计哲学参考，不是代码或目录模板。若后续复制 Pi 代码，必须另行审查许可证、记录来源提交并保留必要版权声明；本计划不复制 Pi 源码。

## 纠偏结论

| 领域 | 当前状态 | 判断 |
| --- | --- | --- |
| 单模块、单进程、显式组装 | `cmd/aice` 极薄，`internal/app` 完成依赖组装 | 与 Pi 的小型 coding harness 哲学一致 |
| CLI / TUI | Cobra 与 Bubble Tea 分层明确，TUI 只消费 Agent 事件 | 语言生态造成的表面变化，实质一致 |
| provider / protocol | `llm -> provider/deepseek -> api/anthropic`，SDK 类型未进入 Agent Loop | 实质一致 |
| 消息模型 | 完整消息被 `.Message()` 压缩成只有 role/content/timestamp 的 `llm.Message` | 实质偏离；历史丢失 assistant 元数据 |
| Agent 事件 | 生命周期大体对应，但使用 `Event`、`RunStart/RunEnd` 和多组专用消息字段 | 主要是表面差异，但会妨碍统一 transcript/session 消费 |
| 长度截断 | `StopReasonLength` 下仍可能执行已经形成合法 JSON 的不完整工具参数 | 实质偏离；违反 Pi 的工具调用安全不变量 |
| 权限所有权 | `Approver` 默认拒绝、`os.OpenRoot` 封闭目录、bash 清洗宿主环境 | 实质偏离；AICE 在进程内建立了 Pi 明确不拥有的权限模型 |
| 资源安全 | 参数验证、输出和时间限制、取消、进程树终止、原子写入 | 应保留；这些不是权限系统 |
| Session / compaction | 尚未实现 | 未完成，不是偏离 |
| 自扩展 | 尚未实现 | 未完成，不是偏离；核心契约稳定前不建插件框架 |

因此，目录外形与 Go 工程分层的变化不大，绝大多数属于语言和库生态差异；真正改变产品哲学的是消息有损、截断调用可执行，以及内建权限策略这三点。

## 不变量

后续每个提交都必须维护以下不变量：

1. `Message` 表示 LLM 可理解的标准消息联合：`UserMessage`、`AssistantMessage`、`ToolResultMessage`。
2. `AgentMessage` 表示完整 transcript。当前没有自定义消息时可以与 `Message` 等价，但 Agent API 必须使用正确的领域名称和边界。
3. 历史和 Session 保存完整具体消息；不再存在把 `AssistantMessage` 降格为 role/content envelope 的路径。
4. `[]AgentMessage -> []Message` 只发生在 LLM 请求边界，`[]Message -> provider SDK` 只发生在 protocol adapter。
5. 一个 assistant 工具调用必须恰好得到一个 tool result，包括未知工具、参数错误、取消、执行失败和长度截断。
6. 长度截断响应中的所有工具调用都执行零次，并各自产生可恢复的错误结果。
7. Agent Loop 不拥有审批、授权、文件系统白名单、网络策略或凭据策略。
8. 默认工具使用宿主进程权限；工作目录只定义相对路径起点，不冒充安全边界。
9. 移除权限策略时，参数验证、边界化输出、超时、取消、退出状态、进程树清理和原子写入不得随之移除。
10. Session 是 append-only 原始事实；模型 context 和 TUI viewport 不反写或覆盖原始记录。

## Go 表达决策

第一轮消息纠偏采用最小且可演进的 Go 表达：

- 把 `llm.Message` 改为由三个具体消息类型实现的 sealed interface。
- 删除贫血 `Message struct` 以及三个有损 `.Message()` 转换方法，不保留兼容别名或过渡 shim。
- `llm.Request.Messages` 保存 `[]llm.Message`，adapter 通过 type switch 映射具体类型。
- Agent 侧公开使用 `AgentMessage` 这个领域名称。由于当前还没有 custom message，第一版使用 `type AgentMessage = Message`，不提前伪造扩展 API。
- 当第一个真实的自定义 transcript 消息需求出现时，再把 `AgentMessage` 扩宽为开放联合，并在同一改动中定义验证、持久化和 `AgentMessage -> Message` 转换规则。
- `ContentPart` 本轮继续保留带 discriminator 的具体 struct。消息纠偏不同时重写 content 建模。
- 保持现有 JSON round-trip 能力；使用 `role` discriminator 恢复具体消息，不能用删除测试规避 interface 解码问题。

这保留了 Pi 的两个层次和具体消息语义，同时避免为了尚不存在的扩展系统引入注册表、反射或 `any`。

## 分阶段提交清单

每一项完成后独立提交。结构调整和行为调整不混入同一提交，除非数据结构本身就是造成行为损失的原因。

### 0. 固化原则与计划

状态：

- [x] `📝 docs: 纠正 AICE 架构原则`
- [x] `📝 docs: 制定 AICE 架构纠偏计划`

验证：

- `git diff --check`

### 1. 恢复 Pi 语义的消息与事件内核

#### 1.1 完整消息联合

类型：结构 + 必要的无损行为修复，二者不可安全拆开。

预计文件：

- `internal/llm/contracts.go`
- `internal/llm/contracts_test.go`
- `internal/agent/contracts.go`
- `internal/agent/loop.go`
- `internal/agent/loop_test.go`
- `internal/api/anthropic/adapter.go`
- `internal/api/anthropic/adapter_test.go`
- `internal/provider/deepseek/deepseek.go`
- `internal/provider/deepseek/deepseek_test.go`
- `internal/app/app.go`
- `internal/app/app_test.go`

改动：

- 引入 sealed `Message` 联合和当前等价的 `AgentMessage`。
- 删除贫血 `Message struct` 与 `.Message()`。
- Agent history、run result 和 interactive history 直接保存完整具体消息。
- adapter/provider 在边界 type switch，不让 SDK 类型向内泄漏。
- 保留 requested model；provider 返回不同实际模型时写入独立 response model 字段，不覆盖请求身份。
- 为联合消息补充基于 `role` 的 JSON 编解码和无损 round-trip 测试。

验收：

- assistant 的 API、provider、requested model、response model、response ID、usage、stop reason、error、content、timestamp 经一轮工具调用和下一轮 request 后保持不变。
- user、assistant、tool result 均能 JSON round-trip 成原具体类型。
- 不存在 `.Message()` 或薄 `Message{Role, Content}` 构造。

建议提交：

- `♻️ refactor: 恢复完整消息联合`

#### 1.2 对齐 AgentEvent 生命周期

类型：结构。

预计文件：

- `internal/agent/contracts.go`
- `internal/agent/loop.go`
- `internal/agent/loop_test.go`
- `internal/app/app.go`
- `internal/app/app_test.go`
- `internal/tui/model.go`
- `internal/tui/model_test.go`
- `internal/tui/run.go`
- `internal/tui/run_test.go`

改动：

- `Event` 更名为 `AgentEvent`，不保留旧名称兼容别名。
- `RunStart/RunEnd` 对齐为 `AgentStart/AgentEnd`。
- `message_start/message_end` 携带统一的 `AgentMessage`，stream update 仍携带 provider-neutral delta。
- `agent_end` 携带本次新增消息，便于 app/session 在一个稳定终点消费。
- TUI 仍只维护 presentation state，不成为 transcript 真相。

验收：

- 文本轮、工具轮、失败轮和取消轮均具有确定事件顺序。
- TUI 和非交互输出只依赖 `AgentEvent`，Agent 不依赖 Bubble Tea。

建议提交：

- `♻️ refactor: 对齐 AgentEvent 生命周期`

### 2. 阻止长度截断工具调用

类型：行为。

预计文件：

- `internal/agent/loop.go`
- `internal/agent/loop_test.go`

改动：

- 在任何具体工具查找、参数校验或执行之前检查 terminal assistant 的 `StopReasonLength`。
- 对该消息中的每个 tool call 生成 `IsError=true` 的配对结果。
- 保持调用顺序和事件顺序，随后允许模型基于错误结果恢复。

验收：

- 即使截断参数是合法 JSON，fake tool 的执行计数也必须为零。
- 多个截断调用各有一个对应结果，ID 和 name 不错配。
- 下一轮模型能看到 assistant 原消息和全部错误结果。

建议提交：

- `🐛 fix: 阻止执行长度截断的工具调用`

### 3. 纠正权限与执行环境所有权

#### 3.1 移除内建审批

类型：行为。

预计文件：

- 删除 `internal/tool/approval.go`
- `internal/tool/write.go`
- `internal/tool/edit.go`
- `internal/tool/bash.go`
- 对应测试和构造调用

改动：

- 删除 `Approver`、`ApprovalRequest` 和 `ErrApprovalRequired`。
- write、edit、bash 构造函数不再接受审批器。
- 工具被 Agent 调用时直接按当前执行环境运行。

验收：

- 不存在 Agent/tool 内建审批 API。
- write、edit、bash 无审批依赖即可执行。
- 参数错误仍在产生副作用前失败。

建议提交：

- `♻️ refactor: 移除工具内建审批`

#### 3.2 把 Workspace 还原为工作目录，而非沙箱

类型：行为。

预计文件：

- `internal/tool/workspace.go`
- `internal/tool/common.go`
- `internal/tool/mutation.go`
- 七个内建工具及其测试
- `internal/app/app.go`
- `internal/cli/root.go`
- 相关测试

改动：

- `Workspace` 只保存并规范化默认工作目录。
- 相对路径从工作目录解析；绝对路径和 `..` 按宿主 OS 权限正常工作。
- 文件工具改用标准 `os` 操作，不再通过 `os.OpenRoot` 构造进程内文件系统边界。
- bash 默认继承宿主环境并以 workspace 为 cwd；AICE 专用 session 元数据后续只做增量注入。
- CLI 文案明确 `--workspace` 是工作目录，不是访问白名单。

必须保留：

- 空路径、NUL 和 malformed input 拒绝。
- read/search/output 大小限制。
- 写入的临时文件、原子替换与 mode 保留。
- bash timeout、合并输出限制、退出码、context 取消和完整进程树终止。
- 敏感值不进入日志。

验收：

- 相对路径仍从 workspace 工作。
- 测试临时目录中的绝对路径和 `../` 可按宿主权限读写。
- bash 能看到测试注入的宿主环境变量，但日志和结果不会自动打印整个环境。
- 资源限制和取消测试继续通过。

建议提交：

- `♻️ refactor: 使用宿主执行环境`

这一步不创建抽象 `ExecutionEnvironment` 框架。当前可替换边界是 `agent.Tool` 和 composition root；等选定第一个真实沙箱后，再从两个具体实现中提取最小共同接口。

### 4. 建立 append-only Session 真相

#### 4.1 JSONL 存储与恢复

类型：新增行为。

预计文件：

- 新增 `internal/session` 包及测试
- `internal/llm` 的消息 codec 测试

第一版范围：

- 标准库实现 append-only JSONL。
- 版本化 header 和完整 turn record。
- 一条完整记录包含原始 `AgentMessage` 序列及必要 usage/时间元数据。
- 只在完整 turn 边界 append；不原地改写历史。
- 重启恢复；允许忽略或截断最后一条未完整写入的 JSONL 尾记录，不能吞掉中间损坏。

明确不做：

- 自动 compaction。
- 数据库。
- Pi 当前完整 session tree、分支、队列和 durable harness。
- 旧 TypeScript session 迁移。

验收：

- 文本轮和工具轮重启后无损恢复，assistant 元数据与 tool-call/result 配对不丢失。
- 中间损坏报错；仅尾部半行可恢复。
- 默认测试不读取真实凭据。

建议提交：

- `✨ feat: 添加 append-only JSONL Session`

#### 4.2 接入真实 CLI / TUI 路径

类型：行为。

预计文件：

- `internal/app/app.go`
- `internal/app/app_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- 必要的 CLI/TUI 集成测试

改动：

- app composition root 创建和关闭 Session。
- interactive runner 从 Session 派生 history/context，不由 TUI 保存真相。
- 每次完整 run 在稳定终点 append 一次。
- print 模式明确是一次性 session 还是加入指定 session，先选择最简单且可测试的语义。

验收：

- 通过真实 `aice` 命令完成两轮对话后重启，第二次请求包含恢复出的完整上下文。
- 取消或失败不会留下半个 tool-call/result 对。

建议提交：

- `✨ feat: 接入 Session 生命周期`

#### 4.3 usage 保护与手动 compaction

类型：新增行为。

前置条件：

- JSONL 重启恢复稳定。
- Message/AgentMessage 已无损。

改动：

- usage 计数和 context-limit 保护。
- 手动 compaction 产生新的派生摘要记录或视图，不覆盖旧消息。
- compaction 只在完整 turn 边界运行。

建议拆为至少两个提交：

- `✨ feat: 添加上下文用量保护`
- `✨ feat: 添加手动 Session 压缩`

#### 4.4 JSONL Session tree 与分支导航

在 4.1-4.3 的线性 Session、恢复和手动压缩稳定后，Session 格式继续演进为 append-only tree：

- 完整 turn 与 compaction checkpoint 都拥有稳定 `id`、`parent_id`，仍只在完整 turn 边界写入。
- checkout/backtrack 追加 `leaf` 记录，只移动当前分支指针，不删除或改写被离开的分支。
- 重启时按 JSONL 顺序重放节点和 leaf move，恢复最后活动叶子。
- 模型上下文只从当前 root-to-leaf 路径派生；回溯后追加的下一轮成为旧节点的新 child。
- compaction 只作用于当前分支，并以 `first_kept_turn_id` 记录稳定边界，不能依赖全局 turn 数组下标。
- 会话格式升级为 version 2；不兼容 version 1，符合本项目不保留旧 Session 格式兼容层的约束。
- 用户入口为 `aice session tree`、`aice session checkout` 和已有的 `aice compact`。

仍不做自动 compaction、数据库和第二套 transcript 真相。

### 5. 开箱即用的完整内建工具

类型：行为。

预计文件：

- `internal/app/app.go`
- `internal/app/app_test.go`
- `internal/tui/model.go`
- 真实 CLI/TUI 验收测试

改动：

- 默认装配 Pi 熟悉的 `read`、`write`、`edit`、`bash`、`grep`、`find`、`ls`。
- built-in 与未来替换工具走同一个 `agent.Tool` 合同。
- 不在 Agent Loop 增加工具白名单、审批或 sandbox 分支。

验收：

- 临时工作目录中通过真实命令完成一次读取、写入、编辑和受控 bash。
- TUI 展示的工具列表与实际装配一致。
- 外部容器启动 AICE 时，无需修改 Agent 内核即可限制其宿主权限。

建议提交：

- `✨ feat: 默认启用完整内建工具`

### 6. 沙箱与扩展：满足条件后再实施

这部分是产品路线，不是当前纠偏代码。

沙箱接入前必须先确定：

- 隔离整个 AICE 进程，还是只路由工具执行。
- 文件、进程、网络和凭据分别由谁拥有。
- 本地与远程 workspace 如何同步。
- 取消、超时、输出、退出码如何穿过边界。
- 选定实现的维护状态、许可证和供应链风险。

有两个真实执行实现后，才从共同需求提取最小 execution interface。不要预建 service locator、全局 registry 或“万能 sandbox manager”。

扩展系统开始前必须稳定：

- `Message` / `AgentMessage` 转换和持久化规则。
- `AgentEvent` 顺序和失败语义。
- `Tool` 定义、执行、更新和结果合同。
- Session append/recovery/compaction 边界。

未来扩展使用 composition root 注册或替换 provider、tool、TUI 行为和 context transform；built-in 不得拥有扩展无法使用的私有捷径。是否提供 Go plugin、子进程协议或静态编译扩展，需要真实分发需求后再决定。

## 每步验证矩阵

| 改动 | focused test | 全量验证 | 真实路径 |
| --- | --- | --- | --- |
| 文档 | `git diff --check` | 不要求 Go 重跑 | 不要求 |
| Message / Event | `go test ./internal/llm ./internal/agent ./internal/api/anthropic ./internal/provider/deepseek` | `go test ./...`、`go vet ./...` | print + TUI smoke |
| 截断保护 | 指定 Agent Loop 测试 | `go test ./...`、`go vet ./...` | faux provider |
| 权限 / 工具 | `go test ./internal/tool ./internal/app` | `go test -race ./...`、`go vet ./...` | 临时目录中的真实 CLI |
| Session | `go test ./internal/session ./internal/app` | `go test -race ./...`、`go vet ./...` | 启动、对话、退出、重启 |
| 默认全工具 | `go test ./internal/app ./internal/cli ./internal/tui` | `go test -race ./...`、`go vet ./...` | 真实 print + TUI |

若默认 Go cache 在受限环境不可写，使用 `GOCACHE=/tmp/aice-go-cache`；不能把环境权限错误误判为源码错误。

## 提交纪律

- 每次只完成上面一个编号项，验证后立即提交。
- 提交前检查 `git status`、精确 diff 和 staged diff。
- 不使用 `git add .`、`git add -A`、stash、hard reset 或兼容 shim。
- 若某一步发现前提错误，先更新本计划并独立提交，不在实现提交中偷偷改变架构方向。
- 任何第三方代码引入与 AICE 自有重构分开提交。
