---
name: npm-token
description: 检查并更新 ~/.npmrc 里的 registry token（GitLab / npmjs 私有源）。当用户说 npm 报 E401/403、token 过期、GitLab PAT 要换新、要确认 .npmrc 里哪些 token 还有效、要把新 token 一次性写到多个项目的 registry 配置时使用。包含 npm-token 的 CLI 用法、退出码、回滚方式与安全约束。
---

# npm-token

`npm-token` 是这台机器上的 TUI 程序（也能当纯 CLI 用，TUI 标题栏会显示版本、有新版可按 `U` 升级）：读出 `.npmrc` 里所有
`//host/path/:_authToken=...` 条目，按 token 分组检查是否有效、什么时候过期，换新值时
先备份、原子写入、写完联网复验，新值被明确拒绝就自动还原。

**agent 一律走 CLI，不要启动 TUI。** TUI 需要真终端（选条目、粘贴 token），非 TTY 里跑不起来。

## 铁律

1. **不要回显 token。** 不要把完整 token 写进回复、日志、临时文件或提交。`npm-token check`
   输出里已经是掩码（如 `glpat-…a1b2`）；自己取到的原始值只在写文件那一次用。
2. **不要提交 `.npmrc`。** 里面是本机 token；也不要把它整份打印出来。
3. **先查再改。** 先跑 `npm-token check`，确认要改哪组（host / project id）；文件里有多组
   token 时必须用 `--host` / `--project` 明确指定，别用 `--all` 一把梭。
4. **改之前自动备份，出问题按提示还原。** 写入会在旁边留 `<file>.bak.<时间戳>`；复验被拒会
   自动还原，网络问不出来时保留写入但给出 `cp <备份> <文件>` 命令——把它原样交给用户。

## 1. 看清现状

```bash
npm-token check            # 纯文本
npm-token check --json     # 给脚本/agent 解析
```

带 `--json` 的输出形如：

```json
{
  "file": "~/.npmrc",
  "tokens": [
    {
      "masked": "glpat-…a1b2",
      "kind": "gitlab-pat",
      "valid": true,
      "expires_in": "剩 498 天",
      "entries": [{"host": "git.example.com", "project": "123", "path": "~/.npmrc:3"}]
    }
  ]
}
```

- GitLab PAT（`glpat-`）查 `/api/v4/personal_access_tokens/self`；GitLab 会把 `/-/whoami`
  302 掉，所以 `npm whoami` / `npm token list` 对 GitLab 源不可用，本工具是另一种路子。
- JWT 直接本地解 `exp`，不联网。
- 其它 registry 试 `/-/whoami`。
- `valid` 为 `null` 表示没问出来（网络/权限），不是失效。

## 2. 换新 token

```bash
# 用户直接给（推荐从 stdin，避免进 shell 历史）
printf '%s' "$NEW_TOKEN" | npm-token set-token --host git.example.com

# 或者显式传值（注意别让它进日志）
npm-token set-token --host git.example.com --token "$NEW_TOKEN"

# 按 GitLab 项目 id 精确匹配
npm-token set-token --project 123 --token "$NEW_TOKEN"
```

| 参数 | 作用 |
| --- | --- |
| `--file PATH` | 指定 npmrc（默认 `~/.npmrc`） |
| `--host HOST` | 只改该主机的条目（如 `git.example.com`） |
| `--project ID` | 只改该 GitLab 项目 id 的条目 |
| `--all` | 改文件里所有条目（多组 token 时必须显式指定） |
| `--token TOKEN` | 直接给值；写 `-` 或 stdin 是管道时从 stdin 读 |
| `--force` | 连 `${VAR}` 这种环境变量引用也改成字面值（默认跳过） |
| `--no-verify` | 写完不联网复验（离线时用） |

退出码：

| 码 | 含义 | 处理 |
| --- | --- | --- |
| `0` | 写入并复验通过（`set-token`）/ 全部有效（`check`） | 报结果 |
| `1` | 有 token 失效或过期（`check`） | 看输出，建议换新值 |
| `2` | 用法错误、网络不通、没有条目；写入被拒时会自动还原 | 看 stderr |

成功时 stdout 形如：

```
已写入 ~/.npmrc：3 条
备份    ~/.npmrc.bak.20261118-143118
复验    glpat-…a1b2 有效，剩 498 天
```

要点：

- `${VAR}` 条目默认跳过（改环境变量本身更合适），需要硬改才加 `--force`。
- 写入只替换 `=` 后面的值，注释、键顺序、其它配置都保留。
- 被明确拒绝（401/403）会自动还原备份；网络异常则保留写入，并给出还原命令。
- 想验证 GitLab 项目权限，用新 token 请求项目 API，不要靠 `npm whoami`。

## 3. 安装与升级

```bash
curl -fsSL https://raw.githubusercontent.com/hongmengchen/npm-token/main/install.sh | sh
```

Windows PowerShell：`irm https://raw.githubusercontent.com/hongmengchen/npm-token/main/install.ps1 | iex`

自己升级（非 TTY 输出纯文本，适合脚本）：

```bash
npm-token upgrade --check   # 只检查新版本
npm-token upgrade           # 下载、校验 SHA-256、原子替换自身
```

用 hmck 统一管理时：

```bash
hmck install npm-token
hmck upgrade npm-token
```

## 4. agent 自己的 skill 文件

`npm-token skill` 会把本文件写到 `~/.claude/skills/npm-token/SKILL.md`，内容跟程序版本一致；
升级 npm-token 时 hmck 会带着刷新。`npm-token skill --print` 只打印不写文件。

## 5. 在本仓库里改代码时

- 版本号在 `main.go` 的 `var version`；打 tag（`git tag vX.Y.Z && git push origin vX.Y.Z`）
  触发 `.github/workflows` 构建六平台二进制并发布。
- 改完跑 `gofmt -l .`、`go vet ./...`、`go test ./...`、`go build -o npm-token .`。
- 测试一律用临时 `.npmrc` 和 `httptest` 假服务，**不要碰真实的 `~/.npmrc`**。
- 不要往仓库里写公司内部信息（真实 registry 域名、项目 id、用户名、token）。
  示例统一用 `git.example.com`、`123`、`alice` 这种假值。

## 6. 常见问题

| 现象 | 原因 / 处理 |
| --- | --- |
| `没有 //host/:_authToken 条目` | `.npmrc` 里没有 token，或用了别的写法（`_auth` 也支持） |
| `valid: null` | 没问出来：网络不通、代理、token 没 self 权限；不代表失效 |
| `文件里有 N 组不同的 token` | 用 `--host` / `--project` 指定一组，或 `--all` |
| `跳过 …（值来自环境变量 …）` | 默认不动 `${VAR}` 条目；要硬改加 `--force` |
| `复验不通过` 后看到 `已还原` | 新 token 无效，原文件已经恢复，换个 token 再来 |
| `npm whoami` 对 GitLab 报 E401 | GitLab 不认这个接口，正常；用 `npm-token check` |
