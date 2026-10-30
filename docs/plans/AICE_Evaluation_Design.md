# AICE 评测架构与标准接入设计

状态：D1–D8 已获用户批准，未开始实施。设计日期：2026-10-30。起始代码基线：`269eeec`。

本文是已批准的实施方案，不覆盖尚未变更的运行契约。按阶段实现，并把实际行为写入对应的维护文档；
全部阶段交付后删除本文，只保留维护文档中的现行设计。

## 1. 目标

AICE 需要一个能指导迭代的评测闭环：对任意两个 AICE 构建，回答“这个改动是否让 harness 变好，
好在哪里，为什么”。同时让 Harbor 等通用评测框架以标准方式接入，不为每个框架维护私有适配器。

“有效的评测”在本方案中有五个可验证的含义：

1. 任意 commit 的本地构建都能被评测，不依赖发布版本。
2. 结论带噪声估计：成对比较、多次运行、置信区间，而不是单个总分。
3. 失败可以解释：每个 trial 都能回到完整的原生 Session，追溯每一步模型看到了什么。
4. 关键决策可以单独复现：从真实 Session 重建某一步的完整模型输入，用候选构建只重跑这一步。
5. 北极星是长期维护质量，公开基准只作为参照和通用能力回归。

## 2. 架构原则

**AICE 是被测系统，不认识任何评测框架。** 它对外只暴露标准协议（ACP）和自己的数据（Session）。
Harbor 是使用方。外部代码不解析 Session 的内部格式。

**内部模型是超集，标准只在边界投影。** AICE 的 Session 是带分支的追加式树，工具调用严格配对，
压缩可回溯，权限决策有来源记录。ATIF 是线性步骤序列，ACP 是面向界面的事件协议。
它们是投影目标，不是存储格式。借鉴它们揭示的缺失语义，不降级内部模型。

**一个执行底座。** Harbor 负责任务格式、沙箱、verifier、多步任务、查看器和排行榜通道。
公开任务与私有任务都写成 Harbor task。AICE 不自建任务运行器或沙箱。

**用一致性测试跟随标准演进。** 投影输出用标准的官方 Schema 或校验器验证，标准版本固定。
升级标准就是修改版本并修复失败的测试。

## 3. 已批准的决定

| 编号 | 决定 | 代价与边界 |
| --- | --- | --- |
| D1 | ACP 成为 AICE 的标准无界面接口，以新前端实现于 `interaction` 契约之上 | 新增依赖 `github.com/coder/acp-go-sdk`（用户已批准，P0 完成维护与许可审查）；ACP 类型不进入 `agent`/`llm` |
| D2 | Harbor 通过其通用 ACP 接入运行 AICE；删除 `integrations/harbor` 的自定义适配器与 Python Session 转换器 | 依赖 P0 验证 Harbor ACP 能运行本地构建；不满足时见 §6 退路 |
| D3 | Session 新增追加式“运行配置”记录，使每次模型调用的完整输入可从 Session 单独重建 | v3 内追加记录类型，有 `title` 与工具元数据的先例；旧文件没有该记录，只是不能精确重建 |
| D4 | Guard 归一化的动作随工具结果记录，并作为 ACP `kind`/`locations` 与评测过程指标的唯一来源 | 工具或 MCP 自报的语义只作展示提示，永不参与授权 |
| D5 | 把 `llm.Usage` 的计量口径写成契约，逐个适配器用测试固定 | 现有口径不变（输入、缓存读、缓存写为互斥加项）；投影负责换算到 ATIF 的包含式口径 |
| D6 | 评测分析读原生 Session，由 AICE 的 Go 代码派生统计；Harbor 的 ATIF 只用于查看器与排行榜 | 不产生第二个 ATIF 生成方；不新增 OpenTelemetry 运行时依赖 |
| D7 | 实验协议固定：先写假设，A/A 噪声底线，成对比较，k≥3，开发集与验收集分离 | 公开基准不作为优化目标；LLM 评审只作辅助信号，不作关卡 |
| D8 | 北极星评测是私有的多步维护任务，使用 Harbor 多步任务格式 | 替代 `evals/*/prepare.py` 与 `verify.py`，替代完成后删除旧驱动 |

## 4. 设计

### 4.1 ACP 前端（D1）

ACP 前端与 TUI、print 一样，是 `interaction.Frontend` 的一个使用方，由 `app` 组装，
新命令以 stdio 运行。映射：

| ACP | AICE |
| --- | --- |
| `session/new`（`cwd`、`mcpServers`） | 新 Session；`cwd` 即工作目录。客户端提供的 MCP 服务作为本次进程的服务来源，仍经 Guard 授权 |
| `session/load` / `session/resume` | 打开已有 Session；`load` 按顺序回放活动分支 |
| `session/prompt` / `session/cancel` | `Runner.NewRun` 与运行取消；停止原因映射到 `end_turn`、`max_tokens`、`max_turn_requests`、`refusal`、`cancelled` |
| `session/update`：消息与思考分块 | `EventAssistantDelta`，`messageId` 取 Session 消息 ID |
| `session/update`：`tool_call` / `tool_call_update` | `EventToolStart` / `EventToolEnd`；`kind`、`locations` 取自 D4，编辑结果输出 `diff` 内容 |
| `session/request_permission` | Guard 的 ask；选项来自 `GuardOption`。deny 不发请求，客户端永远不能放行被 deny 的调用 |
| `session/set_mode` | 权限模式（manual、auto） |
| `usage_update`、压缩更新 | 上下文用量、成本；`compaction` 记录 |

AICE 的分支、`/btw`、设置面板不进入 ACP v1 映射，没有对应能力时不发明扩展。
ACP 的 `_meta` 只承载可丢弃的附加信息。

### 4.2 运行配置记录（D3）

模型实际输入 = 活动路径上的源消息与压缩记录 + 当时的运行配置 + AICE 的上下文派生规则。
前两者已在 Session 中，派生规则是确定性代码，缺的只有运行配置。它不能从其他记录推出：
系统提示词取决于当时的项目文件与 Skill，工具定义取决于当时的 MCP 服务与运行内加载。

新记录是树节点，与 `compaction` 一样参与祖先链但不进入模型上下文。内容为一次模型调用使用的
完整生效配置：系统提示词全文、按发送顺序的工具定义、模型身份、流式选项（推理强度、输出上限等）、
影响上下文派生的参数（如结果视图 token 上限）、AICE 构建版本。

写入规则：

- 只在生效配置变化时追加，内容按哈希去重；分支切换后从新分支点继续即可，不改写已有记录。
- 必须在依赖它的模型调用之前持久化，与“工具调用在执行前持久化”的顺序要求相同。
- 运行内工具集变化（按需加载 MCP 工具）在工具配对边界追加新记录。

由此成立一个可测试的性质：**任意 assistant 消息的模型输入，都能仅从 Session 精确重建。**
所有上下文变换要么被记录（压缩、配置），要么是记录内容的确定性函数（结果视图截断）。
ATIF 的 `context_management` 由此投影，不另建一套上下文操作日志。

### 4.3 动作记录（D4）

Guard 已经为每次工具调用解析出权威的归一化动作（文件读写与路径、命令、网络目标、MCP 身份），
其 `Review` 已随工具结果记录。同样记录 `Action`，ACP 投影按动作映射 `kind`：

| Guard 动作 | ACP `kind` |
| --- | --- |
| 文件读（`grep`、`find`、`ls` 为搜索） | `read` / `search` |
| 文件写 | `edit` |
| 命令 | `execute` |
| 网络 | `fetch` |
| MCP | `other`；服务端注解只作展示提示 |

评测的过程指标（读写比例、触及文件、命令次数）读同一份记录。

### 4.4 用量口径（D5）

现有实现已经统一口径：`streamcore.UsageFromReport` 从供应商报告的输入中扣除缓存读写，
`EstimateCost` 把四项相加。缺的是契约：`llm.Usage` 只注明“normalized”。

字段注释写明：`InputTokens` 不含缓存命中与缓存写入，三者与 `OutputTokens` 互斥相加；
`ReasoningTokens` 是 `OutputTokens` 的子集。每个 API 适配器各有一个固定样例测试，
覆盖其供应商的原始口径；已有的测试保留，缺失的补齐。成本按执行时价格快照记录，已是现状。

### 4.5 实验与分析（D6、D7）

```text
固定模型 + 固定任务集版本
  基线构建、候选构建 → Harbor（ACP）各跑 k 次
  每个 trial：result.json（得分、错误归类、计时）+ 原生 Session
  AICE 统计命令：Session → 过程指标（token、轮数、工具错误、重试、超时、动作分布）
  比较工具：成对通过率差与 bootstrap 置信区间、过程指标成对差、翻转题列表、每解决一题的成本
```

比较工具放在 `evals/` 下，读 Harbor 结果目录，并调用 AICE 统计命令获取过程指标。
实验记录包含两个构建的 commit、模型与推理设置、任务集版本、k 和假设陈述。

**单步回放。** 回放用例 = Session 文件 + assistant 消息 ID + 判定规则。运行器从 Session 与运行配置
重建该步输入，可用当前构建的系统提示词或工具定义替换记录值，只调用一次模型并判定输出。
用例来源是失败 trial 的原生 Session，以及已有的审批记录导出。
它与分类器评估采用相同的运行方式：显式环境变量门控，结果文件进入仓库。

**任务集。** Terminal-Bench 2.1（`terminal-bench/terminal-bench-2-1`）固定约 25 题作为开发集，
优先选基线表现不稳定的题；全量作为验收集，只在阶段节点运行。不使用 SWE-bench Verified。

### 4.6 北极星任务（D8）

`evals/go-service` 与 `evals/python-cli` 的“新建、扩展、修复、重构”改写为 Harbor 多步任务：
环境跨步骤保留，每步开新会话，每步有独立 verifier。奖励维度：

- 本步的隐藏验收测试；
- 之前各步的测试仍然通过（改进能否撑过下一个需求）；
- 构建、vet、lint 与改动规模；
- 可读性的 LLM 评审，只作辅助维度。

## 5. 阶段与验收

### P0：验证

在临时分支上做最小 ACP agent（`initialize`、`session/new`、`session/prompt`、文本更新），
用 Harbor 的 ACP 接入跑 1–2 道 Terminal-Bench 2.1 题。回答：

1. Harbor ACP 能否运行本地构建（本地注册表条目或其他方式）？
2. Harbor 生成的 ATIF 含哪些字段，能否通过官方 `trajectory_validator`？
3. 无人值守时权限选项如何被选择，与 Guard 的 deny、非交互 ask 失败关闭是否一致？
4. 原生 Session 如何从容器取回（日志目录或 Harbor 制品收集）？

同时完成 `acp-go-sdk` 的版本、维护状态、许可与 Go 版本兼容审查。
**验收：** 四个问题的结论写回本文 §6，并据此确认或调整 D2。临时分支不合入。

### P1：数据模型

按 D5、D4、D3 的顺序，各自独立提交。

**验收：**

- 每个 API 适配器有用量口径测试。
- 工具结果记录 Guard 动作，旧记录读取不变。
- 一个集成测试对每次模型调用比较“实际传给 Streamer 的输入”与“仅从 Session 重建的输入”，两者完全相等，
  覆盖压缩、分支切换、运行内工具加载与恢复后的继续运行。
- `docs/execution-sessions.md` 与 `docs/contracts.md` 写入新记录与重建性质。

### P2：ACP 与 Harbor 接入

**验收：**

- ACP 前端覆盖 §4.1 的映射，发出的消息通过固定版本的 ACP JSON Schema 校验。
- Harbor 通过 ACP 在 Terminal-Bench 2.1 开发集上运行本地构建，ATIF 通过官方校验器。
- 删除 `integrations/harbor`，运行说明迁入新的评测文档并加入 AGENTS 任务表。
- print 模式的 JSON 输出改用与 ACP 相同的工具语义词汇（`kind`、`status`、`locations`）。
- `docs/architecture.md` 加入新包、新依赖与前端边界。

### P3：实验底座

**验收：**

- AICE 统计命令与比较工具可用。
- 在开发集上完成一次 A/A 运行，噪声底线写入评测文档。
- 单步回放运行器能从至少一个真实失败 trial 构造用例并重跑。
- `docs/collaboration.md` 写入实验协议：何时需要附实验报告，报告包含哪些内容。

### P4：北极星任务

**验收：**

- 两组维护任务以 Harbor 多步任务运行并产出多维奖励。
- 删除被替代的 `prepare.py` 与 `verify.py` 驱动。
- ROADMAP 中“代码质量评估”的进展证据指向这些结果。

## 6. P0 结论与退路

待 P0 填写。

若 Harbor ACP 不能运行本地构建，保留一个 Harbor agent 子类，只负责上传本地二进制，
其余复用 Harbor 的 ACP 实现，仍不解析 Session。

若 Harbor 生成的 ATIF 不满足排行榜要求，由 AICE 的 Go 代码从 Session 投影 ATIF，替代 Harbor 的生成结果，
保持只有一个生成方，并用官方校验器做一致性测试。

## 7. 不做的事

- 不以 ATIF 或 ACP 类型作为内部存储或 Loop 类型。
- 不记录 token ID、logprobs 等训练用途字段。
- 不预先设计子 agent 的轨迹结构；子 agent 成为产品需求时，按 ATIF 的“嵌套独立轨迹 + 引用”建模。
- 不在内容块中预先补齐 MCP 的全部类型；出现具体的结果丢失时再扩展 `llm.ContentPart`。
- 不自建任务运行器、沙箱或排行榜，不在运行时引入 OpenTelemetry。
- 不为公开基准分数调优，不使用污染严重的基准。

## 来源

- [Harbor 文档索引](https://docs.harborframework.com/llms.txt)：ACP 接入、ATIF、多步任务、错误归类、指标
- [ATIF RFC](https://github.com/harbor-framework/harbor/blob/main/rfcs/0001-trajectory-format.md)
- [Terminal-Bench 2.1 提交规则](https://github.com/harbor-framework/terminal-bench-2-1/blob/main/leaderboard/SUBMIT.md)
- [ACP 协议概览](https://agentclientprotocol.com/protocol/overview)
- [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk)
- [Harness or Model?（arXiv:2609.11987）](https://arxiv.org/abs/2609.11987)：同模型换 harness 的成对实验方法
- [OpenAI：不再评测 SWE-bench Verified](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)
