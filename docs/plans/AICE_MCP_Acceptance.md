# MCP 与 CUA 交付核对

范围来自 [D1–D7 与实施方案](AICE_MCP_Design.md)。实现、确定性回归、真实服务和
原生平台验证分别判断；评测数量不作为额外产品需求，未验证能力不宣称通过。
P0–P4 的实现与清理已交付，以下限制仍然适用。

## 当前交付

| 范围 | 实现及保留的验证 |
| --- | --- |
| 通用连接与配置 | `mcpclient` 处理 stdio/HTTP、分页、通知、取消和有界清理；config/app 负责来源、凭证、连接审批及惰性生命周期。Filesystem 与 DeepWiki 配置到 Print 的实际读取已通过。 |
| 按需发现与选择 | catalog/`tool_search` 提议工具，Loop 在完整记录后下一轮生效；旧版本和未提供的工具拒绝执行。保留目录、词法检索及 [选择边界测试](../../internal/agent/tool_selection_test.go)。 |
| 权限 | 同一 Guard 检查普通工具、MCP 和 resources；授权绑定身份、Schema 和 scope，deny 优先。保留 [应用审批](../../internal/app/mcp_guard_test.go) 与 [持久规则](../../internal/app/mcp_permissions_test.go) 回归。 |
| 结果与历史 | 有序图文、结构化 JSON、partial/unknown 进入 Session；先保存源结果，再裁剪模型视图；回读不重放。保留三个 API adapter、Session 和 [Print 回读](../../internal/app/result_read_test.go) 测试。 |
| 管理与认证 | CLI、`/mcp`、Settings 共享应用能力；OAuth 支持发现、PKCE、注册和操作前刷新。Linear 单账号只读登录、同连接读取与持久刷新已通过。 |
| CUA 迁移 | 通用 MCP 单受管入口和版本 Skill；保留会话、目标、坐标、控制模式与最终许可约束。旧模型包装删除，旧历史展示保留，无动作 CLI 自动回退。 |

主要端到端回归是 [发现→授权→HTTP 调用→Session 重开](../../internal/app/mcp_loop_test.go)。
它检查下一轮 Schema、非交互拒绝、审批后撤权、精确大整数和有序部分结果。
真实服务互通入口与当前支持范围见 [MCP](../mcp.md#verification-evidence)。

## 保留与删除的依据

- 保留协议、权限、取消后不重放、凭证隔离、结果恢复等生产边界测试；不同边界的
  相似输入不等于重复覆盖。
- 保留小型离线检索语料、真实服务 opt-in 检查及原生 CUA gate；它们验证产品行为。
- 删除批量模型路由执行/计时设施、报告汇总与汇总器测试、原始报告和模型会话。
  这些只服务一次评测，不属于产品运行或必要回归。
- 删除实施流水账和重复评测叙述。原始现场已在仓库外备份，维护文档只描述当前行为。

清理后已通过全量 `go test ./...`、`go vet ./...`、离线互通/OAuth 检查、
integration 编译及离线检索检查；整理后的中间提交也均可独立编译。
本轮没有重新调用付费模型、外部账号或原生桌面。

## 验证范围与限制

- 既有 macOS 全量 unit/vet/race 与 Linux arm64 MCP 子集通过；Windows 交叉编译不等于原生验收。
- Filesystem 与 DeepWiki 证明各一个真实非 CUA 服务互通；后续 DeepWiki 有关闭错误。
  本地清理回归证明连接有界关闭，不证明远端 session 已删除。
- Linear 验证主动调早本地 expiry 后的真实刷新，不代表自然过期、撤权及所有 OAuth 服务。
- CUA 在 macOS 三个表单的旧/新入口实际模型对照通过；Linux 私有 X11 有 managed CLI 证据。
  Windows 动作及其他输入/平台缺口见 [平台限制](../desktop.md#platform-evidence)。
- 关键词检索不自动翻译；模型曾因英文查询漏掉中文工具描述并误选相邻操作。
  合成任务结果不能用来声称普遍质量或性能优势。
- 运行内撤权即时复核；其他进程修改配置不会即时广播到冻结 Run，不新增文件 watcher。

后续工作按明确产品缺陷推进。不得为了补齐评测数字扩大任务、重复付费测试或把生成产物提交入库。
