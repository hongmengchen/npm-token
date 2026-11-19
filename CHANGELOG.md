# 变更记录

更早的版本见 [GitHub Releases](https://github.com/hongmengchen/npm-token/releases)。

## [0.7.0] - 2026-11-19

### 新增

- `npm-token auto on|off|status`：自动换新开关。开启前逐组确认 token 真的能自我轮换（GitLab PAT、未失效、带 `api` / `self_rotate` scope、条目是 `_authToken` 字面值），有一组不行就拒绝并列出原因——包括去 GitLab 建 token 时该勾哪个 scope
- 开关打开后，`refresh` 不带参数也按保存的阈值和文件走，直接放进定时任务；`refresh` 不指定 `--host` / `--project` 时会遍历文件里所有组，换不了的（npmjs、JWT 等）跳过并说明，不影响其它组
- TUI 启动时若开关打开且某组 token 已进阈值，会直接换新（只换一组，不弹确认页）；`a` 键在 TUI 里开关自动换新，标题行显示开关状态和阈值
- 开关状态存在本机配置里（macOS `~/Library/Application Support/npm-token/config.json`，Linux `~/.config/npm-token/config.json`），0600，只放开关、阈值和文件路径，没有任何 token

### 变更

- TUI 里 `R` 的提示改成「现在换新」，和自动开关区分开
- 已知 `scopes` 但不够轮换时，`rotate` / `refresh` 在调接口之前就给出同样的 scope 提示（`--dry-run` 也不会白联网）

## [0.6.0] - 2026-11-19

### 新增

- `npm-token rotate`：用现有 GitLab PAT 调 `POST /personal_access_tokens/self/rotate` 换一个新 token，自动改写 `.npmrc` 里同一串值的所有条目并复验；`--dry-run` 先看计划，`--expires-at` / `--days` 指定新有效期，不给就沿用原 token 的有效期
- `npm-token refresh`：只剩 `--if-days`（默认 30）天以内才换，适合放进定时任务；永不过期或还早的直接退出 0
- rotate 的安全顺序：新值先落 0600 恢复文件，再原子改写；复验通过才删恢复文件，失败会保留并提示路径。旧 token 换完即失效，不会回滚到死值
- TUI 按 `R` 走同一条换新流程，先出确认页（主机、条目数、新到期日，以及旧 token 立刻失效和复用检测的提醒）
- 查看 `.npmrc` 的所有条目：`check` 末尾列「其它配置」，`check --json` 多出 `settings` 字段，TUI 里按 `e` 打开带行号、可翻页的条目页；敏感值在解析阶段就打码，`${VAR}` 原样显示
- `tokenReport` 的 JSON 增加 `scopes` 和 `created_at`

### 变更

- TUI 统计行改成「N 个 token · N 条配置」，检查结果摘要改成「N 条认证配置 · N 个 token · N 条其它配置」
- `check` 在 GitLab token 快到期时提示可以用 `refresh` 自动换新
- 退出码补充：rotate 换完但新 token 被拒绝为 `1`，换新失败（旧 token 未被撤销）为 `2`

### 不做

- npmjs token 不能自动刷新：官方建 token 接口要求交互式 2FA，`bypass_2fa` 的 token 又被禁止调用
- JWT 由签发方续期，本工具只检查 `exp`

## [0.5.0] - 2026-11-18

### 新增

- npm-token 有了自己的标志：`docs/logo.svg`（方框 + 里面一颗小方块）
- TUI 左上角用纯文字画出同系列的招牌（方框 + N），标题、统计和更新状态排在它右边；不依赖图片、字体或终端图形支持

## [0.4.1] - 2026-11-18

### 变更

- Windows 安装脚本默认装到 `%USERPROFILE%\.local\bin`，和 hmck 管理的其他工具一致
- 安装时清理旧的 `%LOCALAPPDATA%\npm-token` 二进制和 PATH 项

## [0.4.0] - 2026-11-18

### 新增

- `npm-token upgrade` / `upgrade --check` / `upgrade --force` 自更新
- TUI 标题栏显示当前版本；有新版时按 `U` 在界面内升级
- 自更新和安装脚本校验 release 的 `checksums.txt`（SHA-256）
- 增加 push / PR CI
- 安装脚本的默认目录统一为 `~/.local/bin`（root 为 `/usr/local/bin`）
