# 变更记录

更早的版本见 [GitHub Releases](https://github.com/hongmengchen/npm-token/releases)。

## [0.4.0] - 2026-11-18

### 新增

- `npm-token upgrade` / `upgrade --check` / `upgrade --force` 自更新
- TUI 标题栏显示当前版本；有新版时按 `U` 在界面内升级
- 自更新和安装脚本校验 release 的 `checksums.txt`（SHA-256）
- 增加 push / PR CI
- 安装脚本的默认目录统一为 `~/.local/bin`（root 为 `/usr/local/bin`）
