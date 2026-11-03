# 本地模型网关

网关是一个绑定到 `127.0.0.1` 的单上游 HTTP 反向代理。CC Switch 继续负责供应商和 Codex provider 配置；网关不读取或修改 Codex `auth.json`，也不选择供应商。

## 启动

```powershell
go run ./cmd/gateway -config .\gateway.json
```

配置文件参见 [`gateway.example.json`](../gateway.example.json)。`upstream` 必须是明确的 `http` 或 `https` URL，且其 `host:port` 必须出现在 `allowed_upstream_hosts` 中。网关拒绝将上游配置为自己的监听地址，防止简单的代理循环。`allowed_paths` 是模型接口路径白名单，未知路径返回 `404 path_not_allowed`，不会被发送到上游；模型列表仅接受 GET，Responses 接口仅接受 POST。

启动前设置 `client_token_env` 指定的随机本机令牌。集成补丁的 CC Switch Rust forwarder 会在目标是 numeric loopback HTTP 地址时从同一环境变量注入 `X-Codex-Gateway-Token: Bearer <本机令牌>`；它不会写入 ProviderMeta、数据库或 JavaScript。网关缺少令牌时拒绝转发；该头不会送到上游。代理站的 `Authorization` 是另一份凭据，仍由下面的第三方认证策略控制。不要把官方账户令牌当成本机令牌。

默认会删除客户端的 `Authorization`、`ChatGPT-Account-ID`、Cookie、代理认证、OpenAI 账户头和 ChatGPT 账户头。第三方供应商需要 `Authorization` 时，只有在确认 CC Switch 已经用第三方凭据替换该头后，才可以设置 `forward_authorization: true`。更推荐使用 `headers_from_env` 注入代理站专用的自定义 API 头，例如 `X-API-Key`，避免把官方令牌带入第三方链路。

`/healthz` 和 `/readyz` 用于本机状态检查；`/admin/status` 是只读管理状态接口，需要通过 `admin_token_env` 指定的环境变量提供 Bearer 令牌，并且只接受回环 Host 和同源回环 Origin。状态包含累计请求/失败数、进行中请求数和最近请求的关联 ID、允许路径、状态码、耗时；不保存正文、原始请求头或 URL 查询参数。接口无写操作，因此不会通过浏览器 GET 改变服务设置。

上游重定向不会被自动跟随，跨目标跳转也不会自动携带敏感请求头。默认日志只记录允许路径、状态码、耗时和固定错误阶段，不记录请求正文、响应正文、查询参数或密钥。

网关默认直连其配置的第三方目标。如代理站本身需要网络代理，可用 `outbound_proxy_env` 指定一个环境变量名，其值为 HTTP(S) 代理 URL（不含账户信息）。回环目标始终直连，避免把本地服务送进网络代理。此设置与传给上游的 API 凭据独立。

## 当前边界

网关当前提供透明 HTTP/SSE 转发、取消、超时、背压、目标白名单和基础脱敏。它不实现供应商选择、Responses 与 Chat Completions 的协议转换、请求修复脚本、WebSocket 或真实代理站兼容性保证；这些需要在 CC Switch 串接模拟验证及后续 Issue 中完成。
