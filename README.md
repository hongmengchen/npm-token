# npm-token

<p align="center"><img src="docs/logo.svg" width="96" height="96" alt="npm-token 标志"></p>
<p align="center">方框是 `.npmrc`，里面那颗小方块是它守着的 token。</p>

[![CI](https://github.com/hongmengchen/npm-token/actions/workflows/ci.yml/badge.svg)](https://github.com/hongmengchen/npm-token/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/hongmengchen/npm-token)](https://github.com/hongmengchen/npm-token/releases)
[![License](https://img.shields.io/github/license/hongmengchen/npm-token)](LICENSE)

检查并更新 `~/.npmrc` 里 registry 认证 token 的小工具：**还有效吗、什么时候到期、哪些条目真的能用、怎么换**。

```
$ npm-token
┌───┐  npm-token  v0.7.0  .npmrc 里的 registry token
│ N │  ~/.npmrc · 1 个 token · 3 条配置  ·  已是最新
└───┘

│ glpat-…coco   GitLab PAT   ✓ 有效
│ git.example.com · 名称「ci」 · scopes: api · 最近使用 2026-11-18 14:56
│ 2027-03-31 到期（剩 133 天） · 6 个条目全部可访问

  ↑↓ 选择  ·  enter 换 token  ·  R 现在换新  ·  a 自动开关  ·  e 看条目  ·  r 检查  ·  q 退出
```

## 安装

macOS / Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/hongmengchen/npm-token/main/install.sh | sh
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/hongmengchen/npm-token/main/install.ps1 | iex
```

Windows 的 Git Bash / MSYS 里也能直接用上面的 `install.sh`（会装成 `npm-token.exe`）；
想在 PowerShell / CMD 里也能用，优先选 `install.ps1`。

其它方式：

```bash
go install github.com/hongmengchen/npm-token@latest
```

或者从 [Releases](https://github.com/hongmengchen/npm-token/releases/latest) 直接下二进制。

支持 macOS、Linux、Windows 的 amd64 / arm64（共 6 个单文件二进制，无运行时依赖）。安装脚本会校验 SHA-256；默认装到 `~/.local/bin`（root 下是 `/usr/local/bin`），Windows 装到 `%USERPROFILE%\.local\bin`（Git Bash 里就是 `~/.local/bin`），和其他 hmck 管理的工具同一个目录，并加入用户 PATH。

指定版本或目录：

```bash
curl -fsSL .../install.sh | sh -s -- -v v0.4.0 -b ~/bin
```

Windows 上 TUI 建议用 Windows Terminal；老式控制台对 Bubble Tea 的全屏渲染支持较差。

## 为什么需要它

`.npmrc` 里的 token 是纯文本，坏了只会表现为 `npm install` 报 E401：

```
//git.example.com/api/v4/projects/192/packages/npm/:_authToken=glpat-xxxx
```

而 npm 自带的命令查不了这类 token：

| 命令 | 结果 |
| --- | --- |
| `npm whoami --registry=<GitLab>` | E401 / 302 到 npmjs，GitLab 不实现 `/-/whoami` |
| `npm token list` | 只对 npmjs 有效，GitLab 返回 E401 |
| `glab` | 有自己的登录态，不读 `.npmrc` 里的 token |

所以这里直接问各家自己的 API：GitLab PAT 用 `GET /api/v4/personal_access_tokens/self`，
JWT 本地解 `exp`，npmjs 的 token 才走 `/-/whoami`。不依赖 `npm`，也不需要登录。

## 用法

终端里直接运行 `npm-token` 会打开 TUI（管道 / CI 里则输出上面的纯文本；界面样式来自共享的 [tui](https://github.com/hongmengchen/tui) 主题库）：

```
npm-token  v0.7.0  .npmrc 里的 registry token
  ~/.npmrc · 1 个 token · 3 条配置 · 已是最新

│ glpat-…coco   GitLab PAT   ✓ 有效
│ git.example.com · scopes: api
│ 2027-03-31 到期（剩 133 天） · 6 个条目全部可访问

  ↑↓ 选择  ·  enter 换 token  ·  R 现在换新  ·  a 自动开关  ·  e 看条目  ·  r 检查  ·  q 退出
```

在 TUI 里：`↑↓` 选一组 token → `enter` → 粘贴新值（`ctrl+y` 读剪贴板）→ 确认 → 自动备份、写入、联网复验；新值被拒会当场还原，列表跟着刷新。
按 `R` 走另一条路：让 GitLab 自己换一个新 token，先出确认页，回车才动手。
按 `a` 开关自动换新：开启前先逐组确认这些 token 真能自我轮换，有一组不行就说明原因、不开；开启后每次启动 TUI，若有 token 已进阈值就直接换（只换一组，不弹确认页）。标题行显示「自动换新开（≤30 天）」或「自动换新关」。
按 `e` 看 `.npmrc` 的全部 `key=value` 条目（带行号、可翻页，敏感值已打码）。

标题栏始终显示当前版本，启动时会顺手查一下 GitHub 最新版。有新版本时状态行变成 `最新 0.4.1，按 U 升级`，按 `U` 就在界面里下载、校验 SHA-256 并原子替换自身，完成后提示重开生效——不用退出再去敲 `npm-token upgrade`。

非交互场景：

```bash
npm-token check                    # 只读检查
npm-token check --json             # 给脚本/agent 用
npm-token check --file ./.npmrc    # 查别的文件
npm-token tui --file ./.npmrc      # 指定文件进 TUI
npm-token upgrade --check          # 只检查新版本
npm-token upgrade                  # 升级自身

# 让 GitLab 自己换一个新 token（旧 token 换完立刻失效）
npm-token rotate --dry-run         # 先看计划
npm-token rotate                   # 换，并沿用原 token 的有效期
npm-token rotate --expires-at 2027-03-31
npm-token refresh                  # 只剩 30 天以内才换，否则什么都不做

# 自动换新开关（存本机配置，不含 token）
npm-token auto status              # 看开关、阈值、管的是哪个文件
npm-token auto on                  # 开启前先确认每组 token 都能自我轮换
npm-token auto on --if-days 15     # 顺便把阈值改成 15 天
npm-token auto off                 # 关掉（阈值和文件路径保留）

# 换 token：从 stdin 读新值（推荐，避免留在 shell 历史里）
echo '<新 token>' | npm-token set-token --host git.example.com

# 只改某个项目、或多组 token 时明确指定
npm-token set-token --project 192 --token <新 token>
npm-token set-token --all --token <新 token>

# 给 agent 装用法说明（升级时 hmck 会一起刷新）
npm-token skill
```

`set-token` 的行为：

1. 只替换选中行的 `=` 之后的内容，其余字节不动
2. 先备份成 `<文件>.bak.<时间>`，再同目录临时文件 + rename 原子写入，权限保持 0600
3. 写完**联网复验**：被明确拒绝（401/403）就**自动还原**，并给出原因；只是网络不通则保留写入并提示备份位置
4. 值是 `${VAR}` 的条目默认跳过（改环境变量更合适），要硬改加 `--force`

退出码：`0` 全部有效 / 换新成功 / 不需要换 · `1` 有 token 失效或过期，或换完的新 token 被 GitLab 拒绝 · `2` 用法错误 / 网络不通 / 没有条目 / 换新失败（旧 token 未被撤销）/ `auto on` 时有 token 不能自动换新 / `refresh` 有组被跳过。

### 自动换新（rotate / refresh）

GitLab 16.10 起支持 PAT 自我轮换。`npm-token rotate` 用**现有 token 自己**调
`POST /api/v4/personal_access_tokens/self/rotate` 拿一个新的，再把 `.npmrc` 里同一串值的条目一起改掉、联网复验：

```bash
npm-token rotate --dry-run             # 只打印计划：换哪组、新的到期日、影响几条（会联网确认 token 状态，但不改文件）
npm-token rotate                       # 真换
npm-token rotate --days 180            # 指定新 token 从今天起有效 180 天
npm-token refresh                      # 剩 30 天以内才换（--if-days 改阈值）
```

- **不用额外提供特权 token**：需要 `api` 或 `self_rotate` scope，GitLab 用的 PAT 通常已经带 `api`。只有 `read_api` 之类不够时才会 403，输出里会直说。
- **不传到期参数时沿用原 token 的有效期**（按 `created_at` → `expires_at` 推算）；原 token 永不过期就也不设到期日。不显式传 `expires_at` 的话，要求强制到期的实例会把新 token 默认成一周。
- **旧 token 在换完那一刻立刻作废，不能回滚。** 别的机器、CI 变量里如果还留着同一串值，那边会一起断；而且 GitLab 有复用检测——旧 token 再被使用会撤销同一家族的所有 token，包括刚换的新 token。换之前先把这些地方清掉。
- 只有 GitLab PAT 能这样换：npmjs 的 token 要求交互式 2FA，工具没法在后台刷新；JWT 由签发方续期。
- 换新后新值先写进同目录的 `.npm-token-recovery-*`（0600），再改 `.npmrc`；复验通过自动删除，没通过会留在那儿并告诉你路径——旧 token 已经死了，这时要么修网络，要么手动把恢复文件里的值写进去。
- `--json` 输出只有掩码，永远不带新 token；脚本可以用 `--no-verify` 跳过复验（离线时），退出码含义不变。

### 自动换新开关（auto）

`rotate` / `refresh` 是「现在就换」，`auto` 是「以后自动换」：把开关和阈值存进本机配置，TUI 启动和定时任务都照它走。

```bash
npm-token auto status          # 开关、阈值、管的文件（--json 给脚本）
npm-token auto on              # 开启
npm-token auto off             # 关闭（阈值和文件保留，下次 on 不用重填）
```

`auto on` 不是记住一个布尔值就完事，它会**先逐组把文件里的 token 查一遍**，确认真的能自我轮换：

- 必须是 GitLab PAT（`glpat-`）且状态 `active`
- 接口报告的 `scopes` 里要有 `api` 或 `self_rotate`；拿不到 `scopes` 也算不确认，同样拒绝
- 条目得是 `_authToken=` 的字面值，不是 `${VAR}` 之类

有一组不满足就整条拒绝、退出码 2，并列出是哪一组、缺什么。**缺 scope 时直接告诉你建 token 时该勾哪个**：

```
不能开启自动换新：下面这些 token 没法在后台刷新

  ✗  glpat-…read
     缺 api / self_rotate scope：去 GitLab 头像 → Preferences → Access tokens → Add new token，
     勾选 `api`（或 `self_rotate`），再用 npm-token set-token 写入（自我轮换要 GitLab 16.10 以上）

修好后重新跑 npm-token auto on。
```

开关打开后：

- `npm-token refresh` 不带 `--file` / `--if-days` 时，按配置里保存的文件和阈值走，**定时任务里直接写 `npm-token refresh` 就行**（显式传参仍然优先）；
- 不指定 `--host` / `--project` 时，`refresh` 会遍历文件里**所有** token 组：能换的换，换不了的（npmjs、JWT 等）跳到 stderr 说明，不影响其它组；有组被跳过时退出码 2；
- TUI 启动时若开关打开、且当前文件就是配置里那个文件，某组 token 已进阈值就直接换新（一次启动只换一组，剩下的交给下次启动或定时 `refresh`）；
- 开关状态存在本机配置文件里，0600，**只放开关、阈值和文件路径，没有任何 token**：
  - macOS：`~/Library/Application Support/npm-token/config.json`
  - Linux：`~/.config/npm-token/config.json`
  - Windows：`%AppData%\npm-token\config.json`

`auto` 只管 GitLab PAT：npmjs 的 token 换不了（要交互式 2FA），JWT 由签发方续期，这两类永远开不了自动换新，`auto on` 会直说原因。

### 看 `.npmrc` 里的其它条目

除了 token 行，`.npmrc` 还有 registry 映射、`//host/:username`、`strict-ssl` 之类。这些都会列出来：

- `npm-token check` 末尾的 `其它配置`：带行号，一眼看全；
- `npm-token check --json` 的 `settings` 字段：`line / key / value / kind / scope / secret`；
- TUI 里按 `e`：整份文件的 `key=value`，带行号、可翻页。

看起来像秘密的键（`_authToken`、`_password`、带 `token`/`secret`/`apikey` 的自定义键）解析时就打码；值写成 `${VAR}` 的原样显示——那本身没有秘密。

## 支持的 token 类型

| 类型 | 识别方式 | 怎么判断状态 |
| --- | --- | --- |
| GitLab PAT | `glpat-` 前缀 | `/api/v4/personal_access_tokens/self`：active、revoked、scopes、`expires_at`、`last_used_at`；项目条目另外查 `/api/v4/projects/<id>` |
| JWT（K8s / 自建服务常见） | 三段式、载荷含 `exp` | 本地解出 `exp`，不联网 |
| npmjs token | `npm_` 前缀 | `GET /-/whoami` |
| 其它 | — | 尝试 `/-/whoami`，问不出来就标「判断不了」 |

同一串值被多个条目复用时会合并成一组显示，避免重复请求。

## 安全

- `check` 纯只读，不发写请求
- 输出只露头尾（`glpat-…coco`），JSON 里也没有明文（`"token"` 字段是掩码）；`rotate --json` 同样不含新 token
- `.npmrc` 条目视图里敏感值在解析阶段就打码，渲染层拿不到明文
- 备份文件和换新用的恢复文件都是 0600，写在同目录，方便随时 `cp` 回去
- 自动换新开关的配置文件同样是 0600，里面只有开关、阈值和 `.npmrc` 路径，不含任何 token；`auto on` 校验不通过时不会写这个文件
- 安装和自更新都会校验 release 的 `checksums.txt`（SHA-256）

## 更新

```bash
npm-token upgrade          # 更新到最新版本
npm-token upgrade --check  # 只检查，不下载
npm-token upgrade --force  # 版本相同也重新安装
```

去 GitHub Releases 查最新 tag，和当前版本比对；下载当前平台的二进制后，先用 release 里的
`checksums.txt` 校验 SHA-256，再在可执行文件同目录写临时文件、原子替换，不会留下半截文件。
装过 agent skill 的话，会用新二进制把 skill 一起刷新。安装目录需要提权时用
`sudo npm-token upgrade`。

TUI 里也一样：标题栏显示当前版本，有新版时按 `U` 直接升级。

## Agent skill

`npm-token skill` 会把 [`SKILL.md`](.claude/skills/npm-token/SKILL.md) 写到
`~/.claude/skills/npm-token/SKILL.md`，agent 在新会话里就能看到这个工具的用法、退出码和
「不要回显 token / 不要碰真实 .npmrc」这类约束。内容随二进制走，版本永远和程序一致：

```bash
npm-token skill            # 安装或更新
npm-token skill --print    # 只看内容，不写文件
npm-token skill --dir DIR  # 写到别的 skills 根目录
```

`hmck install npm-token` / `hmck upgrade npm-token` 装完会自动跑一次 `skill --quiet`。

## 开发

只依赖 Go 标准库，Go 1.24+：

```bash
gofmt -l .
go vet ./...
go test ./...
go build -o npm-token .
```

测试用 `httptest` 假 GitLab / registry，不依赖网络。push / PR 会跑 CI，发版前也会先跑测试。

## 参与

- [CHANGELOG](CHANGELOG.md) · [CONTRIBUTING](CONTRIBUTING.md)
- 安全问题请按 [SECURITY](SECURITY.md) 走私密报告，不要公开贴真实 token 或 `.npmrc`

## License

本项目采用 [MIT](LICENSE) 协议，Copyright (c) 2026 chenliang。
