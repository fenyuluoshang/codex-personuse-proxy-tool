# chatgpt-proxy-manager 集成审计

本仓库以 Git submodule 固定引用 `third_party/chatgpt-proxy-manager` 的提交 `48e3b0dedbe523c2915473017d2595c34d2df7da`。该项目采用 MIT 许可证，发布或分发其源码及实质部分时须保留其 `LICENSE` 文件。

## 允许的调用边界

- 默认只使用原项目的检测、测试和独立 WPF 窗口；主程序通过固定的本地路径和参数数组启动它。
- `--enable`、`--restore` 和 `--launch` 可能修改用户快捷方式、用户环境变量、WinINET 系统代理或启动 Codex。自动化集成在完成快照、并发修改检测和异常恢复之前不得直接调用这些参数。
- 构建脚本只在隔离工作目录中运行。它可能把源文件重写为 UTF-8 BOM，并生成 `dist/ChatGPTProxyManager.exe`；原始 submodule 不会被改写。
- 不按进程名批量结束 `codex.exe`。辅助程序退出码为 0 也不代表所有修改成功，调用方须重新检测实际状态。

## 固定构建流程

1. `git submodule update --init --recursive third_party/chatgpt-proxy-manager`
2. 验证 `git -C third_party/chatgpt-proxy-manager rev-parse HEAD` 等于规格中记录的提交。
3. 在 Windows PowerShell 中运行 `scripts/build-proxy-manager.ps1 -VerifyOnly`；该模式只验证来源和所需工具，不执行任何代理或 Codex 操作。
4. 如需生成测试 EXE，运行 `scripts/build-proxy-manager.ps1 -Build`。脚本从固定提交导出临时副本，应用 `patches/proxy-manager/patch.py` 后编译。构建完成后检查 SHA-256，并将产物留在未跟踪的隔离输出目录；不要从网络位置直接执行。

当前上游构建脚本使用 Windows 自带 .NET Framework 4.8 的 `csc.exe` 和 WPF 引用。若环境缺少这些组件，验证应失败并说明原因，而不是下载或替换编译器。

## 后续安全补丁要求

在接入主界面之前，必须分别完成以下能力：

- 保存用户环境变量的存在状态、值和类型，恢复前比较当前值；检测到外部修改时报告冲突。
- 保存 WinINET 代理、PAC 和快捷方式的完整原状态；异常退出和超时恢复使用同样的比较策略。
- 为启动和恢复提供结构化结果与版本化错误码，不依赖日志文本或单一退出码。
- 用进程句柄或明确的子进程身份管理本工具启动的实例，不按名称杀掉其他 Codex 会话。
- 默认不改变系统代理；登录确实需要临时系统代理时，在界面展示影响范围并记录事务状态。

## 已实现的 Windows 恢复补丁

隔离构建会加入 `RecoveryJournal.cs`、`SafeRecovery.cs` 和新的 `SystemProxyHelper.cs`。用户环境变量和临时 WinINET 设置都使用当前用户 DPAPI 加密的写前日志，恢复时按值比较；环境变量允许互不相关的值分别恢复，WinINET 代理设置作为一组处理，出现外部冲突时整组保留并返回冲突。事务文件保留原始值的存在状态、注册表类型和原始字节，因此空值、`REG_EXPAND_SZ`、多字符串及数值类型不会被误删或转换。

`--safe-status-json` 是只读状态查询，`--safe-recover-json` 返回版本化 JSON 并在仍有冲突时退出码为 3，临时系统代理窗口由带事务 ID 的独立 watchdog 回收。`--safe-enable-json` 和 `--safe-launch-json` 返回结构化结果；已有不匹配的 ChatGPT/Codex 实例会要求用户手动重启。补丁不按进程名结束 Codex，也不把任意同名进程当作本工具子进程。

当前补丁的快捷方式备份仍沿用上游文件备份机制，尚未纳入 DPAPI 事务日志；系统代理快照覆盖 Internet Settings 中使用的代理、PAC 和自动检测值，但不会声称能够恢复第三方 VPN 驱动自己的连接配置。`tests/WindowsRecovery/run.ps1` 只使用随机 `HKCU\\Software\\CodexPersonalProxyTool.Tests\\<guid>` 键和临时目录，覆盖类型保留、崩溃重载、冲突、过期、错误事务 ID 及加密日志等 28 项断言。

这些要求在实现前不能被“原项目本身可用”替代。原项目的 GUI 仍可作为独立窗口复用；WPF 控件不直接嵌入 Tauri/React 页面。
