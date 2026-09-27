# Computer Use 人工验收

本页用于记录需要真人输入或独立测试环境的结果。自动测试通过的范围与
已知限制见 [Computer Use](desktop.md#platform-evidence)。没有做的项目记为
“未验证”；一次成功不能覆盖已复现的其他失败。

## 当前验收结果

2026-09-27，操作者反馈除首次安装外，本页人工检查均已执行且认为正常。
据此，已有安装的修复与启用保持、中文输入和焦点、代理光标、普通应用切换
无新增审批、Stop 后的输入与停止行为，记录为**操作者报告通过**。
这不是自动化断言，也不覆盖文末单列的多显示器、权限撤销和中断拖动。

操作者提供的任务回报记录了三款真实应用的结果：

- TextEdit 的 `source.txt` 两次读回 `AICE-MANUAL-314 中文 ✓`。
- Safari 的 `AICE Manual Form` 输入相同文本，Commit 只点击一次；提交后
  读到一致的 Result 和 `Commits: 1`。
- VS Code 的 `result.txt` 经界面全选并输入相同文本，AXTextArea 读回完整值；
  文件未保存。过程中使用 `Shift+Option+F1` 开启 Screen Reader Optimized，
  因而本次通过以开启该无障碍模式为条件，不证明默认 Electron 状态可用。
- 回报声明仅使用三个桌面工具，没有操作其他窗口、启动或关闭应用，也没有
  使用 shell、文件工具或 browser 工具完成任务。

最后再次查看 Safari 时，`desktop_apps` 报 `Driver session unavailable`。
此前已经确认的 Result 和一次提交仍作为本次任务证据；末次复查及连接恢复
没有验证，不能把断连记作正常行为。模型没有再次提交，符合未知结果不重放的
约定。仅凭这条错误不能判断是会话到期、启动失败或其他原因；需原始工具结果
与时序才能定位。本次未提供原始 Session、模型用量、应用版本或实际运行的
AICE 提交号，因此不推算 token 用量，也不声称做过独立 Session 重放验证。

首次安装和首次系统授权因没有独立环境保持**未验证**。此前自动焦点测试的
失败仍保留在 [验证记录](collaboration.md#explicit-real-model-desktop-gate)；
本次人工通过不能抹去这些失败或证明所有像素动作均已修复。

## 当前这台 Mac：先做修复检查

这台机器已经安装 Cua Driver 0.29.1，也已经授予系统权限。
现在可以验证修复流程，不能把它记作首次安装或首次授权。

1. 从当前源码构建并启动 AICE，输入 `/desktop`。
2. 保持 `Control mode` 为 `Background only`，打开 `Set up / Repair`。
3. 确认后检查辅助功能、屏幕录制和实际截图的结果；成功后保存启用。
4. 退出并重开 AICE，检查启用状态保留。打开设置本身不应创建模型对话。

本次已构建的本机版本可从终端启动：

```sh
/private/tmp/aice-cua-manual/aice \
  --workspace /private/tmp/aice-cua-manual/workspace \
  --provider opencode-go --model deepseek-v4.1-flash --thinking high \
  --run-token-budget 1000000 --max-turns 20 --run-timeout 5m \
  --no-update-check --no-approve
```

这个临时路径只适用于本次准备的 Mac；清理临时文件后需要重新构建。
`--no-approve` 忽略项目资源，桌面启用仍通过设置完成；不要添加 `--yolo`。
每个模型运行最多 100 万供应商报告 token，实际账单不由该数值精确限定。

## 中文输入、焦点和真实应用

准备三个只含测试数据的窗口：TextEdit 打开 `source.txt`、Safari 打开
本次临时目录的 `form.html`、VS Code 打开 `result.txt`。文件位于
`/private/tmp/aice-cua-manual/workspace`。没有 VS Code 时可使用另一款现有
文本编辑器，并在提示词中写清名称；不要用第二个 TextEdit 窗口冒充第三款应用。

在 AICE 中发送：

> 仅用 desktop_apps、desktop_observe、desktop_act 操作这三个测试窗口。
> 读取 TextEdit 的 source.txt，把其中的测试文本填写到 Safari 标题为
> AICE Manual Form 的表单并提交一次，确认 Result 显示一致，再通过 VS Code
> 界面把相同文本写入 result.txt（不必保存）。不要操作其他窗口，不要启动
> 或关闭应用，不要使用 shell、文件工具或 browser 工具代替桌面操作。
> 如果不能完成就说明原因；未知结果不要重复提交。

发送后保持 AICE 终端在前台，在输入框里用中文输入法缓慢输入
“人工输入保持在这里”，先不要按回车。在候选词出现时停留片刻，再完成选词。
观察整个过程，而不只检查最后的前台窗口：

| 检查项 | 通过标准 |
| --- | --- |
| 跨应用结果 | 三款应用内文本一致，网页提交计数为 1 |
| 输入和焦点 | 中文候选框不中断、不丢字，人工输入未进入目标应用；任务结束后仍可正常输入和使用鼠标 |
| 代理光标 | 操作时可见；系统真实指针未被后台动作搬走 |
| 审批 | 启用完成后，普通切换应用没有新的 AICE 应用授权弹窗 |

另开一个简单测试任务，执行中输入 `/desktop`，使用设置里的 Stop（F6）。
检查任务停止后没有新动作，界面仍可输入；已经发生的编辑不能因 Stop 被当作
未执行，也不能自动重放。不要用真实发送、购买或删除操作做这项检查。

## 首次安装和首次系统授权

这项留给没有安装 Cua 的测试 Mac 或独立 macOS 测试环境。仅创建新的用户账户
不等于机器上没有系统级安装。不要为了这项测试重置当前工作机的 TCC 或删除
已可用的 App。

从未安装状态进入 AICE `/desktop`：先取消安装，确认功能没有被启用；再重试，
按向导安装，亲自接受 macOS 的辅助功能和屏幕录制授权。核对授权对象是
`CuaDriver`，让向导完成实际截图检查后保存。重开 AICE，检查启用状态和截图。
只看权限开关为开不算截图通过；已有授权的修复检查不算首次授权通过。

## 把结果发回来

记录：AICE 提交号、macOS/Cua 版本、应用名称、通过或失败的步骤，以及是否
人工切过窗口。焦点异常时先记录现象，再尝试切走并切回恢复输入；恢复了也要
保留失败记录。无需发送账号、密钥或私人窗口截图。

Windows 目前只有安装与只读状态路径，动作运行时尚未接通；不要把 Windows
真机准备好等同于动作验收通过。多显示器、权限撤销和中断拖动仍分别记录，
本页的基础检查不替代它们。
