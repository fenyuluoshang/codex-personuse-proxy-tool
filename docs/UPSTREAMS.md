# 上游组件清单

本清单记录项目复用的上游组件、固定版本、构建方式和发布要求。版本必须使用提交哈希或不可变发布标签；不要直接依赖 `main`。更新上游时先更新本文件，再执行隔离构建和回归验证。

## CC Switch

- **仓库**：<https://github.com/farion1231/cc-switch>
- **固定来源**：`main` 当前提交 `7afceec14b977ecc2f4373e8ffb40e7223b435ac`
- **许可证**：MIT，版权声明为 Jason Young。发布复制或修改后的代码时保留上游 `LICENSE`。
- **技术栈**：Tauri 2、Rust、React 18、TypeScript/Vite、SQLite。
- **目标复用范围**：供应商管理、Codex provider 配置、本地路由、已有协议处理和原始桌面界面。
- **集成边界**：CC Switch 是 Codex provider 配置的唯一管理者。本项目不得与它并发写入同一份 Codex TOML，也不得把其内部 SQLite 或 Rust 模块当成稳定公共 API。网关通过已验证的本地转发边界接入。
- **构建环境**：以该提交的 `rust-toolchain.toml`、`package.json`、`pnpm-lock.yaml` 和 `CONTRIBUTING.md` 为准。当前上游文档要求 Rust 工具链、Node 20.19+ 或 22.12+、pnpm 10；Windows 构建还需要 C++ Build Tools 和 WebView2。
- **建议构建步骤**：在隔离检出目录中安装锁定依赖，执行上游文档规定的检查和 Tauri 构建命令；不要在用户当前 Codex 配置目录中运行集成测试。若上游支持 `CC_SWITCH_TEST_HOME`，使用临时目录而不是修改 `HOME`。
- **发布注意**：没有上游签名私钥时不要伪造 updater artifact 或签名；发布包必须包含 MIT 文本，并另行核对第三方依赖、字体、图标和商标。

## chatgpt-proxy-manager

- **仓库**：<https://github.com/xztsummer/chatgpt-proxy-manager>
- **固定来源**：`main` 当前提交 `48e3b0dedbe523c2915473017d2595c34d2df7da`
- **许可证**：MIT，版权声明为 xztsummer。发布复制或修改后的代码时保留上游 `LICENSE`。
- **技术栈**：Windows 原生 C# / WPF，使用 Windows 自带 .NET Framework 4.8 编译为单文件 EXE。
- **目标复用范围**：Windows MSIX 应用激活、Chromium `--proxy-server` 启动参数、Rust/Node 进程代理环境设置、检测和原 WPF 界面。
- **集成边界**：优先作为独立 sidecar/窗口运行，由主程序通过固定可信路径和参数数组调用；不把其 CLI 的文本日志和退出码当成稳定 API，除非增加版本化结构化结果契约。
- **构建方式**：上游 `build/build.ps1` 使用 Windows 自带 C# 编译器和资源文件生成 EXE。应在隔离副本中执行并固定源码哈希；不要从网络下载后直接执行构建产物。
- **安全补丁要求**：上游操作可能写入用户级 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`、快捷方式和临时系统代理，且存在恢复和按进程名处理的边界风险。集成前必须保存原值、类型和存在状态，恢复时比较当前值并处理并发修改，支持崩溃恢复，并禁止批量终止不属于本工具的 Codex 会话。
- **系统影响**：临时启用 WinINET 系统代理期间可能影响其他程序；已有浏览器进程可能忽略新的启动参数。必须分别验证桌面前端、后台组件、浏览器登录和回调的代理路径。

## 版本更新流程

1. 在隔离目录获取上游提交，记录新的提交哈希、构建工具版本和变更摘要。
2. 确认上游许可证和第三方依赖没有变化；如有变化更新发布清单。
3. 运行 A1/A2 的干净构建和原界面启动/退出验证，不启动真实 Codex 或修改系统代理。
4. 运行本项目的模拟链路、配置所有权和恢复测试。
5. 评估上游改动是否影响凭据隔离、端口、路径、协议转换、系统设置恢复或进程生命周期；通过后再更新固定版本。

## 当前状态

两个固定提交的子模块已在 2026-11-05 初始化，并完成隔离构建：CC Switch Renderer/typecheck、Personal Proxy 页面测试、Rust/Tauri Desktop 构建，以及 chatgpt-proxy-manager 的 VerifyOnly、WPF 构建和无参数窗口启动/关闭探测均通过。构建产物只保留在未跟踪的 `.build` 目录；完整 CC Switch 窗口生命周期、Rust 端到端模型链路、真实桌面端、官方登录和代理站验收仍属于后续任务。
