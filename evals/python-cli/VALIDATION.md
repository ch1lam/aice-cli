# 离线验证记录

- 日期：2026-09-05
- 环境：本地 macOS，Python 3.14.4。
- 命令：`PYTHONDONTWRITEBYTECODE=1 python3 evals/python-cli/fixture.py self-check`
- 实际退出码：0
- 未调用模型、网络或付费 API。

```text
reference stage 1: PASS (21 cases)
reference stage 2: PASS (25 cases)
reference stage 3: PASS (29 cases)
reference stage 4: PASS (31 cases)
starter stage 2: prior stage PASS (21 cases)
starter stage 2: expected failure net-before-minimum
starter stage 3: prior stage PASS (25 cases)
starter stage 3: expected failure large-exact-cents
starter stage 4: prior stage PASS (29 cases)
starter stage 4: expected failure prefix-net-threshold
CRLF mutation: expected byte-output failure sum-sort-file
```

参考实现通过所有阶段。各非空起点通过上一阶段用例，但在新阶段指定用例失败，
包括大金额精度回归。这避免把无效测试或已经完成的任务当作评估挑战。
阶段 4 的结构质量仍需人工检查“仅重构”diff；固定输出测试无法证明维护性。

审阅后补充：stdout 按 UTF-8 字节精确比较，避免文本模式将 CRLF 归一化。
临时 CRLF 变异实现被首个输出用例拒绝；同时覆盖多行 CSV 后错误的物理行号、
金额周围空白与 25 位整数拒绝、阶段 3 的 24 位整数最大精确金额。参考实现无需修正。

## 参考实现维护性审阅

以下审阅针对所提供参考源码，不是对模型的评分。主审独立复跑上述命令，结果一致。

| 维度 | 分数 | 证据与限制 |
| --- | --- | --- |
| 理解成本 | 2 | `cli.py` 管参数和流，`csv_io.py` 管格式，`domain.py` 管交易；调用路径直接可追踪。 |
| 修改局部性 | 2 | 前缀需求只涉及参数传入和汇总后筛选，无需改变 CSV 解码或金额计算。 |
| 抽象合理性 | 1 | 没有框架或无消费者接口；金额正则已约束格式，后续 Decimal 防御判断存在重复，可进一步简化。 |
| 正确性依据 | 2 | 验收只观察子进程行为，覆盖大金额回归、原子输入失败和字节级输出；预置变异在指定用例失败。 |
| 简洁与惯例 | 1 | 状态通过参数和返回值传递；参考代码保留了生成阶段起点所用的 `FEATURE_STAGE`，真实交付不需要这层开关。 |

一次将所有有效交易读入内存适合当前小任务，但未测量大文件内存上界。
这些固定用例并未穷尽所有输入或平台；尤其不能从参考实现通过推断 AICE
能自主产出同等质量代码。未来实际运行应另存模型结果及两段重构 diff。
