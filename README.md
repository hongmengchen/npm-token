# npm-token

检查并更新 `~/.npmrc` 里 registry 认证 token 的小工具：**还有效吗、什么时候到期、哪些条目真的能用、怎么换**。

```
$ npm-token
~/.npmrc
6 条认证配置 · 1 个 token · 3 个 registry 映射

✓ glpat-…coco  GitLab PAT  有效
   git.example.com · 名称「ci」 · scopes: api · 最近使用 2026-11-18 14:56
   2027-03-31 到期（剩 133 天）
   条目
     ✓ /api/v4/packages/npm/
     ✓ /api/v4/projects/192/packages/npm/
     ✓ /api/v4/projects/213/packages/npm/
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

支持 macOS、Linux、Windows 的 amd64 / arm64（共 6 个单文件二进制，无运行时依赖）。安装脚本会校验 SHA-256；macOS/Linux 默认装到 `/usr/local/bin`（不可写则 `~/.local/bin`），Windows 装到 `%LOCALAPPDATA%\npm-token` 并加入用户 PATH。

指定版本或目录：

```bash
curl -fsSL .../install.sh | sh -s -- -v v0.1.0 -b ~/bin
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
npm-token  v0.4.0  .npmrc 里的 registry token
  ~/.npmrc · 6 条认证 · 1 个 token · 已是最新

│ glpat-…coco   GitLab PAT   ✓ 有效
│ git.example.com · scopes: api
│ 2027-03-31 到期（剩 133 天） · 6 个条目全部可访问

  ↑↓ 选择  ·  enter 换 token  ·  r 重新检查  ·  q 退出
```

在 TUI 里：`↑↓` 选一组 token → `enter` → 粘贴新值（`ctrl+y` 读剪贴板）→ 确认 → 自动备份、写入、联网复验；新值被拒会当场还原，列表跟着刷新。

标题栏始终显示当前版本，启动时会顺手查一下 GitHub 最新版。有新版本时状态行变成 `最新 0.4.1，按 U 升级`，按 `U` 就在界面里下载、校验 SHA-256 并原子替换自身，完成后提示重开生效——不用退出再去敲 `npm-token upgrade`。

非交互场景：

```bash
npm-token check                    # 只读检查
npm-token check --json             # 给脚本/agent 用
npm-token check --file ./.npmrc    # 查别的文件
npm-token tui --file ./.npmrc      # 指定文件进 TUI
npm-token upgrade --check          # 只检查新版本
npm-token upgrade                  # 升级自身

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

退出码：`0` 全部有效 · `1` 有失效或过期 · `2` 用法错误 / 网络不通 / 判断不了。

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
- 输出只露头尾（`glpat-…coco`），JSON 里也没有明文（`"token"` 字段是掩码）
- 备份文件 0600，写在同目录，方便随时 `cp` 回去

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
go test ./...
go build -o npm-token .
```

测试用 `httptest` 假 GitLab / registry，不依赖网络。
