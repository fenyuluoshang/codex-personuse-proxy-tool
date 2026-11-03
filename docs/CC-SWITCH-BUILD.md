# CC Switch 隔离构建记录

对应 Issue：#1。记录固定提交 `7afceec14b977ecc2f4373e8ffb40e7223b435ac` 的只读构建验证。

## 来源

- Repository: `https://github.com/farion1231/cc-switch.git`
- Path: `upstream/cc-switch`
- Commit: `7afceec14b977ecc2f4373e8ffb40e7223b435ac`
- License: upstream MIT `LICENSE` must be retained when distributing the source or a substantial portion.
- Package: `4.0.25`

## Validation

The following commands were run inside the submodule. They do not launch Codex, write the user's Codex configuration, or change system proxy settings.

```text
pnpm install --frozen-lockfile
pnpm typecheck
pnpm build:renderer
```

Results:

- `pnpm install --frozen-lockfile`: passed with the pinned `pnpm@10.12.3` lockfile. The registry emitted transient retry warnings; installation completed.
- `pnpm typecheck`: passed.
- `pnpm build:renderer`: passed. Vite reported existing CSS/chunk-size warnings only.
- Full Tauri build was not run because this machine does not currently expose `rustc`/`cargo`; Windows Tauri prerequisites and Rust 1.95 remain required for the desktop artifact.

The generated `node_modules` and `dist` directories are local submodule build outputs and are ignored by the upstream repository. They are not part of this repository's tracked source.

## Personal proxy integration

The replayable integration patch is `patches/cc-switch/0001-personal-proxy-integration.patch`.
Use the repository script from PowerShell to export the pinned submodule into a fresh
`.build` directory, apply the patch, and run the renderer checks:

```powershell
.\scripts\build-cc-switch.ps1 -Action Renderer
.\scripts\build-cc-switch.ps1 -Action Test
```

The desktop action additionally requires the pinned Rust toolchain, Windows C++ build
tools, and WebView2:

```powershell
.\scripts\build-cc-switch.ps1 -Action Desktop
```

The fork disables Tauri updater artifacts because it does not own the upstream signing
key or update endpoint. The personal proxy page reads `/admin/status` through Rust with
`CODEX_GATEWAY_ADMIN_TOKEN`; the token is never passed to JavaScript. Start/stop only
controls the gateway child process that this application started, using the fixed
`helpers/codex-gateway.exe` and `gateway.json` paths. The forwarder injects
`X-Codex-Gateway-Token` from `CODEX_GATEWAY_CLIENT_TOKEN` only for numeric loopback HTTP
targets; the protected header cannot be supplied through provider metadata.

## Isolation boundary

The submodule is intentionally pinned and is not treated as a public library. Its Tauri backend owns CC Switch's provider database and Codex configuration. Integration code must not independently write the same provider fields or use internal Rust modules as a stable API. Use a controlled fork or explicit external process boundary for future changes.
