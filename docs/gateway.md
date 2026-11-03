# 本地模型网关

网关是一个绑定到 `127.0.0.1` 的单上游 HTTP 反向代理。CC Switch 继续负责供应商和 Codex provider 配置；网关不读取或修改 Codex `auth.json`，也不选择供应商。

## 启动

```powershell
go run ./cmd/gateway -config .\gateway.json
```

配置文件参见 [`gateway.example.json`](../gateway.example.json)。`upstream` 必须是明确的 `http` 或 `https` URL，且其 `host:port` 必须出现在 `allowed_upstream_hosts` 中。网关拒绝将上游配置为自己的监听地址，防止简单的代理循环。

默认会删除客户端的 `Authorization`、`ChatGPT-Account-ID`、Cookie、代理认证、OpenAI 账户头和 ChatGPT 账户头。第三方供应商需要 `Authorization` 时，只有在确认 CC Switch 已经用第三方凭据替换该头后，才可以设置 `forward_authorization: true`。更推荐使用 `headers_from_env` 注入代理站专用的自定义 API 头，例如 `X-API-Key`，避免把官方令牌带入第三方链路。

`/healthz` 和 `/readyz` 用于本机状态检查；其余请求按照固定上游转发。上游重定向不会被自动跟随，跨目标跳转也不会自动携带敏感请求头。默认日志只记录错误阶段、方法和路径，不记录请求正文、响应正文或密钥。

## 当前边界

网关当前提供透明 HTTP/SSE 转发、取消、超时、背压、目标白名单和基础脱敏。它不实现供应商选择、Responses 与 Chat Completions 的协议转换、请求修复脚本、WebSocket 或真实代理站兼容性保证；这些需要在 CC Switch 串接模拟验证及后续 Issue 中完成。
