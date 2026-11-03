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

## Isolation boundary

The submodule is intentionally pinned and is not treated as a public library. Its Tauri backend owns CC Switch's provider database and Codex configuration. Integration code must not independently write the same provider fields or use internal Rust modules as a stable API. Use a controlled fork or explicit external process boundary for future changes.
