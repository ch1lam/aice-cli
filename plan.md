# AICE 架构优化计划

> 创建于 2026-08-07。基于五路并行审计结论(api / tui / app+cli / agent+llm / tool+session+trust+config),目标是让代码重新对齐 AGENTS.md 与 docs/architecture.md 的设计哲学。按以下顺序实施,每波完成后必须通过全量验证。

## 审计结论摘要

- 三个 API adapter 之间约 750 行复制粘贴(流式骨架、错误归一化、协议映射、usage 合并),且存在 5 处行为漂移
- session 包写路径/重放路径双轨验证,活跃分支推导两套实现
- app 层约 15 处 provider switch 手写注册表;模型定价目录重复定义
- TUI 直接依赖 internal/llm、本地累加 usage、checkout 后 transcript 漂移
- 严格 JSON 解码重复 5 处;受保护资源清单两处平行维护
- agent loop.go 重试控制流重复两遍;失败结果修补三路径并存;provider 错误分类硬编码在 agent

## 目标(按实施顺序)

### 目标 1:抽取 API 层流式共享骨架

- 新建职责具体的共享包(如 internal/api/streamcore),只依赖 internal/llm + 标准库,不引入任何 SDK 类型
- 消除以下内容的三份重复:Next/shift/Close/errorEvent/messageSnapshot/contentSnapshot 流式骨架、normalizeProviderError、ThinkingLevel→协议参数映射、tool schema 转换、usage 合并+cost 计算、maxTokens 兜底、跨模型 thinking 折叠、imageDataURL
- 统一 5 处行为漂移:
  - 未知 finish_reason → 全部返回 llm.StopReasonUnknown
  - temperature > 2 → 三个 adapter 全部拒绝
  - 空 tool-call arguments → 三个全部归一化为 {} (零参调用合法,不违反"不执行不完整工具调用"不变项)
  - 未知内容类型 → 全部返回 error
  - 流错误文案统一
- 测试基础设施(collectEvents/newSSEServer/roundTripFunc/minimalRequest)抽 internal/apitest(可选,测试语义不改变)
- 验收:三个 adapter 公开 API(New/Stream 及 provider 使用的方法)不变;相关测试全绿

### 目标 2:session 包双轨合并

- validateNodeBoundary/validateReplayNode 参数化合并;retainNode/retainReplayNode 合并;AppendLeaf 检查与 readSnapshot Leaf 分支复用同一校验
- activeTurnIDs 与 deriveActiveBranch 单一化;Store 内存索引与 indexSnapshot 单一来源(缓存,非第二存储)
- 可选:store.go(883 行)按职责拆分 store.go/replay.go/validate.go
- 验收:JSONL 记录格式与公开 API 完全兼容;压缩边界语义不变;session/app 测试全绿

### 目标 3:provider 目录收敛

- internal/provider 定义小接口(ID/Label/Models/DefaultModel/New/SaveAPIKey/Configured),deepseek/opencode 实现
- deepseek-v4 模型定价目录合并为单一声明;validateMessages 提升为能力参数化(SupportsImage/SupportsRedactedThinking),消除漂移
- app 层 15 处 switch 收敛为构造器注入的 []Provider(禁止 init() 注册或包级注册表)
- 验收:新增 provider 只实现接口即可;app/cli/provider 测试全绿

### 目标 4:TUI 展示事件层

- run.go 桥接层引入 TUI 自有展示模型,切断 internal/llm 依赖(TUI 不再 import llm)
- RuntimeState/runUpdate 增加 Usage,app 在回合/命令后快照 session.TotalUsage 回传;TUI 不再本地累加
- /checkout 后 app 回传会话变更信号,TUI 重建可见 transcript
- conclusion/流式 delta/工具 JSON 解析全部收进桥接层
- 验收:tui 测试全绿;usage/transcript 无双份真相

### 目标 5:共享基础设施

- 新建 internal/jsonutil(单一职责:严格 JSON 解码),替换 5 处 DisallowUnknownFields+尾随值拒绝(tool/common.go、session/store.go、session/records.go、config/config.go、trust/store.go)
- trust 包导出受保护资源清单(唯一来源)与 rooted 读取器;project_prompt/project_init 复用
- 路径规范化统一:trust.CanonicalPath 补 null-byte 检查;app 抽 resolveSessionPath;tool/workspace 复用
- 文本提取 helper 合并(visibleContentText/visibleAssistantText)
- 验收:行为不变;全量测试绿

### 目标 6:loop.go 拆分与失败处理收敛

- 两段重复重试控制流(loop.go:141-186 / 253-282)提取 retryTurn helper
- loop.go(926 行)按职责拆分(stream/retry_flow/finalize 等)
- 失败结果修补三路径(failTruncatedToolCalls/syntheticToolResults/pairUnfinishedToolCalls)收敛为单一策略;消除静默吞错
- provider 错误分类(permanentProviderCode/transientProviderCode)下沉 llm 契约
- 删除 Request.UnmarshalJSON 死代码;validateToolSchema 复用 llm 校验;newErrorToolResult panic→error
- RunInput.Model 与构造注入 service 的一致性校验
- 验收:事件序列测试(loop_test.go)全绿;agent 测试全绿

## 红线(不得违反的设计哲学)

1. session:append-only JSONL、稳定 ID、完整回合边界压缩;不引入第二存储/可变数据库
2. 绝不执行不完整/无效的流式工具调用
3. provider 收敛必须保持构造器注入;禁止 init() 注册或包级注册表
4. trust 受保护清单保持恰好 3 个文件(AGENTS.md、.aice/SYSTEM.md、.aice/APPEND_SYSTEM.md)
5. TUI 只读展示,不得写回 session;agent 不感知 Bubble Tea
6. SDK 类型不得泄漏出 adapter 包;单 Go module 单二进制;标准库优先
7. 禁止笼统包名(shared/utils/helpers)

## 实施方式

- 多智能体分波实施,每波代理处理互不重叠的包:
  - 波 1 = 目标 1(api/provider) + 目标 2(session)
  - 波 2 = 目标 5(tool/session/config/trust/app) + 目标 6(agent/llm)
  - 波 3 = 目标 3(provider/app)
  - 波 4 = 目标 4(tui/app)
- 每波结束后:gofmt -l、go test ./...、go vet ./...;全部完成后 go test -race ./...
- 结构性改动、行为改动分 commit(本次不提交,除非用户要求)

## 状态

- [x] 目标 1:api streamcore(完成 2026-08-07)
- [x] 目标 2:session 双轨合并(完成 2026-08-07)
- [x] 目标 3:provider 目录收敛(完成 2026-08-07)
- [x] 目标 4:TUI 展示事件层(完成 2026-08-07)
- [x] 目标 5:共享基础设施(完成 2026-08-07)
- [x] 目标 6:loop.go 拆分(完成 2026-08-07)
- [x] 最终验证:gofmt + test + vet + race(全部通过,19 包全绿)
