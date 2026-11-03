# Codex 隔离 provider 验证

`scripts/verify-codex.py` 只验证本地 Codex CLI 与本地模拟 Responses 服务。它为每次运行创建临时 `CODEX_HOME`，使用虚构的环境变量令牌，显式传入 `--no-daemon --ephemeral --ignore-user-config --ignore-rules --skip-git-repo-check`，并在结束时只终止自己启动的模拟服务和 Codex 子进程。

运行：

```powershell
python scripts/verify-codex.py --report .artifacts/codex-verification.json
```

脚本不会执行官方登录、连接真实代理站或读取现有 `auth.json`，也不会把原始输出、请求正文或令牌写入报告。`requires_openai_auth=false` 和 `true` 两种配置都会运行，以区分 provider 的环境变量凭据行为；这不能证明个人账号登录、桌面应用、插件或 MCP 的兼容性。

真实桌面验收仍需在用户指定的测试时机单独进行，届时记录 Codex 和桌面应用版本、登录浏览器路径、代理出口和所需功能；不要把本地模拟通过等同于官方账号验收。
