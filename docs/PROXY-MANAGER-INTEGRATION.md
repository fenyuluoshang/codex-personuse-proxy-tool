# chatgpt-proxy-manager 集成审计

本仓库以 Git submodule 固定引用 `third_party/chatgpt-proxy-manager` 的提交 `48e3b0dedbe523c2915473017d2595c34d2df7da`。该项目采用 MIT 许可证，发布或分发其源码及实质部分时须保留其 `LICENSE` 文件。

## 允许的调用边界

- 默认只使用原项目的检测、测试和独立 WPF 窗口；主程序通过固定的本地路径和参数数组启动它。
- `--enable`、`--restore` 和 `--launch` 可能修改用户快捷方式、用户环境变量、WinINET 系统代理或启动 Codex。自动化集成在完成快照、并发修改检测和异常恢复之前不得直接调用这些参数。
- 构建脚本只在隔离工作目录中运行。它可能把源文件重写为 UTF-8 BOM，并生成 `dist/ChatGPTProxyManager.exe`；不要在 submodule 工作树有未提交修改时运行。
- 不按进程名批量结束 `codex.exe`。辅助程序退出码为 0 也不代表所有修改成功，调用方须重新检测实际状态。

## 固定构建流程

1. `git submodule update --init --recursive third_party/chatgpt-proxy-manager`
2. 验证 `git -C third_party/chatgpt-proxy-manager rev-parse HEAD` 等于规格中记录的提交。
3. 在 Windows PowerShell 中运行 `scripts/build-proxy-manager.ps1 -VerifyOnly`；该模式只验证来源和所需工具，不执行任何代理或 Codex 操作。
4. 如需生成测试 EXE，运行 `scripts/build-proxy-manager.ps1 -Build`。构建完成后检查 SHA-256，并将产物留在未跟踪的隔离输出目录；不要从网络位置直接执行。

当前上游构建脚本使用 Windows 自带 .NET Framework 4.8 的 `csc.exe` 和 WPF 引用。若环境缺少这些组件，验证应失败并说明原因，而不是下载或替换编译器。

## 后续安全补丁要求

在接入主界面之前，必须分别完成以下能力：

- 保存用户环境变量的存在状态、值和类型，恢复前比较当前值；检测到外部修改时报告冲突。
- 保存 WinINET 代理、PAC 和快捷方式的完整原状态；异常退出和超时恢复使用同样的比较策略。
- 为启动和恢复提供结构化结果与版本化错误码，不依赖日志文本或单一退出码。
- 用进程句柄或明确的子进程身份管理本工具启动的实例，不按名称杀掉其他 Codex 会话。
- 默认不改变系统代理；登录确实需要临时系统代理时，在界面展示影响范围并记录事务状态。

这些要求在实现前不能被“原项目本身可用”替代。原项目的 GUI 仍可作为独立窗口复用；WPF 控件不直接嵌入 Tauri/React 页面。
