# Codex Personal Proxy Tool

Windows 上的 Codex 个人代理工具。复用 CC Switch 原界面及模型路由、ChatGPT Proxy Manager 的 WPF 代理启动器，并为第三方链路提供本机模型网关。

当前可运行的是 Go 网关和本地模拟供应商；完整桌面整合、官方账号和非 TUN 登录仍在验收中。需求见 [SPECIFICATION.md](SPECIFICATION.md)。

## 获取源码

```powershell
git clone --recurse-submodules https://github.com/fenyuluoshang/codex-personuse-proxy-tool.git
cd codex-personuse-proxy-tool
```

已有 clone 可运行 `git submodule update --init --recursive`。两个上游固定在精确提交，构建会在独立副本中应用本项目补丁。

## 测试

```powershell
go test -timeout 60s ./...
Push-Location fixtures/mock-provider
go test -timeout 60s ./...
Pop-Location
```

## 本机网关

复制 `gateway.example.json` 为不入库的 `gateway.json`，设置真实第三方目标及允许的主机与路径。在启动进程中设置随机的 `CODEX_GATEWAY_CLIENT_TOKEN` 和 `CODEX_GATEWAY_ADMIN_TOKEN`，然后运行：

```powershell
go run ./cmd/gateway -config ./gateway.json
```

将 CC Switch 选定的第三方供应商指向 `http://127.0.0.1:8787/v1`。集成补丁会在 Rust 后端发现 numeric loopback HTTP 目标时，从 `CODEX_GATEWAY_CLIENT_TOKEN` 运行时环境变量注入 `X-Codex-Gateway-Token`；该令牌不会写入 ProviderMeta、数据库或 JavaScript。实际串接仍需按已安装版本验证。第三方上游认证与本机访问令牌独立设置，参见 [网关说明](docs/gateway.md)。

网关默认拒绝缺失本机令牌、未知接口和错误方法。健康检查可访问 `/healthz`；管理状态 `/admin/status` 需要独立管理令牌。网关不会读取官方登录缓存或自动修改 Codex 配置。

## 上游与许可

- [固定来源清单](docs/UPSTREAMS.md)
- [CC Switch 构建](docs/CC-SWITCH-BUILD.md)
- [代理启动器构建与恢复边界](docs/PROXY-MANAGER-INTEGRATION.md)

第三方代码保持原有 MIT 版权和许可。尚未完成验收的功能不会因为上游界面可以启动而视为已通过。
