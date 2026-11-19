package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "0.7.0"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		// 终端里直接给 TUI；管道/CI 里保持输出可 grep 的纯文本
		if isTTY(os.Stdin) && isTTY(os.Stdout) {
			return runTUI(nil)
		}
		return runCheck(nil)
	}

	cmd := "check"
	if !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "check":
		return runCheck(args)
	case "tui", "ui":
		return runTUI(args)
	case "set-token", "set":
		return runSetToken(args)
	case "rotate":
		return runRotate(args, 0)
	case "refresh":
		return runRotate(args, 30)
	case "auto":
		return runAuto(args)
	case "skill":
		return runSkill(args)
	case "upgrade":
		return runUpgrade(args)
	case "version", "-v", "--version":
		fmt.Println("npm-token " + version)
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `npm-token %s — 检查并更新 ~/.npmrc 里的 registry token

用法:
  npm-token                       终端里打开 TUI；管道里输出检查结果
  npm-token tui [--file PATH]     强制打开 TUI
  npm-token check [--file PATH] [--json] [--no-color]
  npm-token set-token [--file PATH] [--host HOST | --project ID | --all]
                      [--token TOKEN | -] [--force] [--no-verify]
  npm-token rotate [--file PATH] [--host HOST | --project ID]
                   [--expires-at YYYY-MM-DD | --days N] [--dry-run] [--json] [--no-verify]
  npm-token refresh [--if-days N] [同上]   token 快到期时自动换新（默认 30 天内）
  npm-token auto on|off|status [--file PATH] [--if-days N] [--json]
                                           自动换新开关：开启时先确认每组 token 都换得了；
                                           refresh、TUI 启动都按保存的阈值走
  npm-token skill [--dir DIR] [--print]   把 agent skill 装到 ~/.claude/skills
  npm-token upgrade [--check] [--force]   升级自身（--check 只看版本）
  npm-token version
  npm-token help

TUI:
  ↑↓ 选 token，enter 换新值，R 让 GitLab 自动换新（先出确认页），a 开关自动换新，
  e 看 .npmrc 所有条目，r 重新检查，q 退出。粘贴后回车确认，写入前自动备份，
  写完联网复验；新 token 被拒会自动还原。开关打开后，启动时 token 若已进到
  阈值内会直接换新（不再确认，旧 token 立刻作废）。标题栏显示当前版本；
  有新版时按 U 直接升级。

check:
  读出 .npmrc 里所有 //host/path/:_authToken 条目，按 token 分组，尽量联网问出
  「还有效吗 / 什么时候过期 / 哪些条目能用」。GitLab PAT 查
  /api/v4/personal_access_tokens/self，JWT 本地看 exp，其它 registry 试 /-/whoami。
  只读，不会改任何文件。

set-token:
  把选中的条目换成新 token。先备份成 <file>.bak.<时间>，再原子写入，写完复验。
  --host    只改该主机的条目（如 git.example.com）
  --project 只改该 GitLab 项目 id 的条目
  --all     改文件里所有条目（多组 token 时必须显式指定）
  --token   直接给值；写 "-" 或让 stdin 是管道则从 stdin 读
  --force   连 ${VAR} 这种环境变量引用也一起改成字面值（默认跳过并提示）
  --no-verify  写完不联网复验

rotate / refresh:
  用 token 自己调 GitLab 的 POST /personal_access_tokens/self/rotate 换一组新的
  PAT（GitLab 16.10+），然后把 .npmrc 里同一串值的条目一起改掉、联网复验。
  旧 token 换完立刻作废；别的机器、CI 里还留着同一串的话那边会一起断，而且
  GitLab 发现旧 token 再被使用，会把同一家族的新 token 也撤销。
  需要 token 带 api 或 self_rotate scope——现有 token 通常就够了，不用另给一个。
  只有 GitLab PAT 能这样换：npmjs 的 token 要求交互式 2FA，JWT 由签发方续期。
  --expires-at  新 token 的到期日；不给 --days 也不给这个时，沿用原 token 的有效期
  --days        新 token 从今天起有效多少天
  --dry-run     只打印换新计划，不改文件（仍要联网确认 token 状态）
  --no-verify   换完不联网复验
  refresh 只在剩余天数 <= --if-days（默认 30）时才换；永不过期或还早的直接退出 0

auto:
  自动换新开关，状态存在本机配置里（不是 .npmrc，也不含 token）。
  auto on       先逐组确认能自我轮换（GitLab PAT、未失效、带 api 或 self_rotate
                scope、条目是 _authToken 字面值）；有一组不行就拒绝并列出原因和
                建 token 时该勾什么；都行才写开关，--if-days 设阈值（默认 30）
  auto off      关掉开关，.npmrc 和 token 都不动
  auto status   看开关、阈值、配置文件位置，以及每组 token 现在能不能换
  开关打开后：TUI 启动时若 token 已进阈值就自动换一组；refresh 用保存的
  --if-days 和文件，文件里有多组时逐组都看，换不了的那组跳过并提示。

退出码:
  0  全部有效 / 换新成功 / 不需要换
  1  有 token 失效或过期；rotate 换完但新 token 被 GitLab 拒绝
  2  用法错误 / 网络不通 / 没有条目 / 换新失败（旧 token 未被撤销）
`, version)
}

// ---------- TUI ----------

func runTUI(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file string
	fs.StringVar(&file, "file", "", "npmrc 路径（默认 ~/.npmrc）")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
		return 2
	}
	path, err := resolvePath(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到家目录: "+err.Error())
		return 2
	}

	p := tea.NewProgram(newTuiModel(path), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TUI 出错: "+err.Error())
		return 2
	}
	return 0
}

func resolvePath(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	return defaultNpmrcPath()
}

// ---------- check ----------

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file string
	var asJSON, noColor bool
	fs.StringVar(&file, "file", "", "npmrc 路径（默认 ~/.npmrc）")
	fs.BoolVar(&asJSON, "json", false, "输出 JSON")
	fs.BoolVar(&noColor, "no-color", false, "不输出颜色")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
		return 2
	}

	path, err := resolvePath(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到家目录: "+err.Error())
		return 2
	}
	f, err := parseNpmrc(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "没有这个文件: %s\n", path)
			return 2
		}
		fmt.Fprintf(os.Stderr, "读不了 %s: %v\n", path, err)
		return 2
	}
	if len(f.Auth) == 0 {
		fmt.Fprintf(os.Stderr, "%s 里没有 //host/:_authToken 条目，没什么可查的\n", path)
		return 2
	}

	reports := make([]tokenReport, 0, len(f.Auth))
	for _, g := range groupByToken(f.Auth) {
		reports = append(reports, checkToken(g.Entries))
	}

	code := worstCode(reports)
	if asJSON {
		out := struct {
			File     string        `json:"file"`
			Tokens   []tokenReport `json:"tokens"`
			Settings []configLine  `json:"settings"`
		}{path, reports, f.Settings()}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return 2
		}
		return code
	}

	color := !noColor && isTTY(os.Stdout)
	fmt.Print(renderCheck(path, f, reports, color))
	return code
}

func worstCode(reports []tokenReport) int {
	code := 0
	for _, r := range reports {
		switch {
		case r.Valid != nil && !*r.Valid:
			return 1
		case r.Valid == nil || r.Revoked:
			code = 2
		}
	}
	return code
}

// ---------- set-token ----------

func runSetToken(args []string) int {
	fs := flag.NewFlagSet("set-token", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file, host, project, tokenFlag string
	var all, force, noVerify bool
	fs.StringVar(&file, "file", "", "npmrc 路径（默认 ~/.npmrc）")
	fs.StringVar(&host, "host", "", "只改该主机的条目")
	fs.StringVar(&project, "project", "", "只改该 GitLab 项目 id 的条目")
	fs.StringVar(&tokenFlag, "token", "", "新 token；- 表示从 stdin 读")
	fs.BoolVar(&all, "all", false, "改所有条目")
	fs.BoolVar(&force, "force", false, "连 ${VAR} 条目也改成字面值")
	fs.BoolVar(&noVerify, "no-verify", false, "写完不联网复验")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "多余的参数: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	path, err := resolvePath(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到家目录: "+err.Error())
		return 2
	}
	f, err := parseNpmrc(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读不了 %s: %v\n", path, err)
		return 2
	}

	targets, err := selectTargets(f.Auth, host, project, all)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	token, err := readNewToken(tokenFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if err := validateToken(token); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	var write, skipped []authEntry
	for _, t := range targets {
		if t.EnvName != "" && !force {
			skipped = append(skipped, t)
			continue
		}
		write = append(write, t)
	}
	for _, t := range skipped {
		fmt.Fprintf(os.Stderr, "跳过 %s:%d（值来自环境变量 %s，改环境变量本身更合适；要硬改加 --force）\n",
			t.File, t.LineNo+1, t.EnvName)
	}
	if len(write) == 0 {
		fmt.Fprintln(os.Stderr, "没有可写的条目")
		return 2
	}

	backup, err := rewriteAuth(path, write, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if backup != "" {
			fmt.Fprintf(os.Stderr, "原文件已备份在 %s\n", backup)
		}
		return 2
	}
	fmt.Printf("已写入 %s：%d 条\n", path, len(write))
	fmt.Printf("备份    %s\n", backup)
	if skippedCount := len(skipped); skippedCount > 0 {
		fmt.Printf("跳过    %d 条（环境变量）\n", skippedCount)
	}

	if noVerify {
		return 0
	}

	// 用写进去的实际条目复验一次
	f2, err := parseNpmrc(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "写完了但重读失败: %v\n", err)
		return 2
	}
	var fresh []authEntry
	for _, e := range f2.Auth {
		if e.Value == token {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		fmt.Fprintln(os.Stderr, "写完了但在文件里找不到新值，请检查 "+path)
		return 2
	}
	rep := checkToken(fresh)
	if rep.Valid != nil && *rep.Valid {
		fmt.Printf("复验    %s 有效%s\n", rep.Masked, expiresSuffix(rep))
		return 0
	}

	fmt.Fprintf(os.Stderr, "复验不通过：%s %s\n", rep.Masked, firstNonEmpty(rep.Error, rep.ExpiresIn, "状态未知"))
	if rep.Valid != nil && !*rep.Valid {
		// 明确被拒：自动还原，别让 npm 因为一个错 token 全线报错
		if err := copyFile(backup, path); err != nil {
			fmt.Fprintf(os.Stderr, "自动还原失败（%v），手动还原：\n  cp '%s' '%s'\n", err, backup, path)
			return 2
		}
		fmt.Printf("已还原  %s（备份里原来的 token 继续生效）\n", path)
		return 2
	}
	// 问不出来（网络/服务异常）：保持写入，留备份备查
	fmt.Fprintf(os.Stderr, "无法联网确认新 token。要还原：\n  cp '%s' '%s'\n", backup, path)
	return 2
}

// ---------- rotate / refresh ----------

type rotateJSON struct {
	File       string       `json:"file"`
	Host       string       `json:"host"`
	Token      string       `json:"token"` // 旧 token，已打码
	DryRun     bool         `json:"dry_run,omitempty"`
	NoChange   bool         `json:"no_change,omitempty"`
	Rotated    bool         `json:"rotated"`
	ExpiresAt  string       `json:"expires_at,omitempty"`
	ExpiryNote string       `json:"expiry_note,omitempty"`
	Updated    int          `json:"updated"`
	Skipped    int          `json:"skipped,omitempty"`
	Backup     string       `json:"backup,omitempty"`
	Recovery   string       `json:"recovery_file,omitempty"`
	Verify     *tokenReport `json:"verify,omitempty"`
	Error      string       `json:"error,omitempty"`
}

// runRotate 处理 rotate（defaultIfDays=0，总是换）和 refresh（快到期的才换）
func runRotate(args []string, defaultIfDays int) int {
	name := "rotate"
	if defaultIfDays > 0 {
		name = "refresh"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file, host, project, expiresAt string
	var days, ifDays int
	var dryRun, asJSON, noVerify bool
	fs.StringVar(&file, "file", "", "npmrc 路径（默认 ~/.npmrc）")
	fs.StringVar(&host, "host", "", "只换该主机的 token")
	fs.StringVar(&project, "project", "", "只换该 GitLab 项目 id 的 token")
	fs.StringVar(&expiresAt, "expires-at", "", "新 token 的到期日 YYYY-MM-DD")
	fs.IntVar(&days, "days", 0, "新 token 从今天起有效多少天")
	fs.IntVar(&ifDays, "if-days", defaultIfDays, "还剩多少天以内才换（0 = 总是换）")
	fs.BoolVar(&dryRun, "dry-run", false, "只打印计划，不真换")
	fs.BoolVar(&asJSON, "json", false, "输出 JSON")
	fs.BoolVar(&noVerify, "no-verify", false, "换完不联网复验")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "多余的参数: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if expiresAt != "" && days > 0 {
		fmt.Fprintln(os.Stderr, "--expires-at 和 --days 只能给一个")
		return 2
	}

	// refresh 没显式给 --file / --if-days 时，用开关保存的值，定时任务才能照常跑
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if defaultIfDays > 0 {
		if s, err := loadAutoSettings(); err == nil && s.AutoRefresh {
			if !explicit["if-days"] {
				ifDays = clampIfDays(s.IfDays)
			}
			if !explicit["file"] && s.File != "" {
				file = s.File
			}
		}
	}

	path, err := resolvePath(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到家目录: "+err.Error())
		return 2
	}
	f, err := parseNpmrc(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读不了 %s: %v\n", path, err)
		return 2
	}
	if len(f.Auth) == 0 {
		fmt.Fprintf(os.Stderr, "%s 里没有 //host/:_authToken 条目，没法换\n", path)
		return 2
	}

	// refresh 不挑组：文件里每组都看一眼，能换且到期的才换，换不了的跳过；
	// rotate 保持一次一组，必须用 --host / --project 指定。
	sweep := defaultIfDays > 0 && host == "" && project == ""
	var groups []tokenGroup
	if sweep {
		groups = groupByToken(f.Auth)
	} else {
		sel, err := selectTargets(f.Auth, host, project, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 2
		}
		groups = groupByToken(sel)
		if len(groups) != 1 {
			var b strings.Builder
			fmt.Fprintf(&b, "匹配到 %d 组 token，一次只换一组，请用 --host / --project 挑一组：\n", len(groups))
			for _, g := range groups {
				fmt.Fprintf(&b, "  %s  %s（%d 条）\n", maskToken(g.Entries[0].Value), strings.Join(uniqueHosts(g.Entries), ", "), len(g.Entries))
			}
			fmt.Fprintln(os.Stderr, strings.TrimRight(b.String(), "\n"))
			return 2
		}
	}

	color := isTTY(os.Stdout)
	var results []rotateJSON
	code := 0
	for _, g := range groups {
		rep := checkToken(g.Entries)
		plan, err := buildRotationPlan(g.Entries, rep, expiresAt, days)
		if err != nil {
			if !sweep {
				fmt.Fprintln(os.Stderr, err.Error())
				return 2
			}
			// 多组刷新：这组换不了就跳过（比如 npmjs token），别拖累别的组
			if !asJSON {
				fmt.Fprintf(os.Stderr, "跳过 %s：%s\n", rep.Masked, err.Error())
			}
			results = append(results, rotateJSON{File: path, Host: firstHost(g.Entries), Token: rep.Masked, Error: err.Error()})
			code = 2
			continue
		}

		if ifDays > 0 {
			noChange := ""
			if plan.Old.ExpiresAt == "" {
				noChange = plan.Old.Masked + " 永不过期，不用刷新"
			} else if left, ok := daysLeft(plan.Old.ExpiresAt); ok && left > ifDays {
				noChange = fmt.Sprintf("%s 还剩 %d 天，超过 %d 天的刷新线，这次不换", plan.Old.Masked, left, ifDays)
			}
			if noChange != "" {
				if !asJSON {
					fmt.Println(noChange)
				}
				results = append(results, rotateJSON{File: path, Host: plan.Host, Token: plan.Old.Masked,
					ExpiresAt: plan.Old.ExpiresAt, NoChange: true})
				continue
			}
		}

		if dryRun {
			if !asJSON {
				fmt.Print(renderRotatePlan(plan, path, color))
			}
			results = append(results, rotateJSON{File: path, Host: plan.Host, Token: plan.Old.Masked,
				DryRun: true, ExpiresAt: plan.ExpiresAt, ExpiryNote: plan.ExpiryNote,
				Updated: len(plan.Entries), Skipped: plan.Skipped})
			continue
		}

		out := performRotation(path, plan, noVerify)
		if c := rotateExitCode(out); c > code {
			code = c
		}
		if !asJSON {
			fmt.Print(renderRotateResult(out, path, color))
		}
		v := rotateJSON{File: path, Host: out.Host, Token: plan.Old.Masked, Rotated: out.Rotated,
			ExpiresAt: out.NewExpiry, Updated: out.Updated, Skipped: out.Skipped,
			Backup: out.Backup, Recovery: out.Recovery}
		if out.Verify.Masked != "" {
			vr := out.Verify
			v.Verify = &vr
		}
		if out.Err != nil {
			v.Error = out.Err.Error()
		}
		results = append(results, v)
	}

	if asJSON {
		if len(groups) == 1 {
			return writeRotateJSON(results[0], code)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			File    string       `json:"file"`
			Results []rotateJSON `json:"results"`
		}{path, results}); err != nil {
			return 2
		}
	}
	return code
}

func firstHost(entries []authEntry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[0].Host
}

func writeRotateJSON(v rotateJSON, code int) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 2
	}
	return code
}

func expiresSuffix(rep tokenReport) string {
	if rep.ExpiresIn != "" {
		return "，" + rep.ExpiresIn
	}
	return ""
}

// selectTargets 决定改哪些行：显式过滤 > 单一 token 全改 > 报错让人选
func selectTargets(entries []authEntry, host, project string, all bool) ([]authEntry, error) {
	host, project = strings.TrimSpace(host), strings.TrimSpace(project)
	if host != "" || project != "" {
		var out []authEntry
		for _, e := range entries {
			if host != "" && e.Host != host {
				continue
			}
			if project != "" && e.ProjectID != project {
				continue
			}
			out = append(out, e)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("没有匹配的条目（host=%q project=%q），先用 npm-token check 看看有哪些", host, project)
		}
		return out, nil
	}
	if all {
		return entries, nil
	}

	groups := groupByToken(entries)
	if len(groups) == 1 {
		return groups[0].Entries, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "文件里有 %d 组不同的 token，请用 --host / --project 挑一组，或 --all 全改：\n", len(groups))
	for _, g := range groups {
		hosts := uniqueHosts(g.Entries)
		fmt.Fprintf(&b, "  %s  %s（%d 条）\n", maskToken(g.Entries[0].Value), strings.Join(hosts, ", "), len(g.Entries))
	}
	return nil, errors.New(strings.TrimRight(b.String(), "\n"))
}

func readNewToken(flagVal string) (string, error) {
	if flagVal != "" && flagVal != "-" {
		return strings.TrimSpace(flagVal), nil
	}
	if flagVal == "-" || !isTTY(os.Stdin) {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
		if err != nil {
			return "", fmt.Errorf("从 stdin 读 token 失败: %w", err)
		}
		v := strings.TrimSpace(string(b))
		if v == "" {
			return "", errors.New("stdin 是空的，没有拿到新 token")
		}
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("NPM_TOKEN")); v != "" {
		return v, nil
	}
	return "", errors.New("没拿到新 token。可以：--token <新token>，或 echo '<新token>' | npm-token set-token")
}

func validateToken(t string) error {
	if t == "" {
		return errors.New("新 token 是空的")
	}
	if strings.ContainsAny(t, " \t\r\n=\"'") {
		return errors.New("新 token 里有空白或引号，粘贴时可能带进了多余字符")
	}
	if len(t) < 8 {
		return fmt.Errorf("新 token 只有 %d 个字符，看起来不像完整值", len(t))
	}
	return nil
}

// ---------- 分组 ----------

type tokenGroup struct {
	Entries []authEntry
}

// groupByToken 按文件里写的那串值分组：${VAR} 和字面值分开算
func groupByToken(entries []authEntry) []tokenGroup {
	var groups []tokenGroup
	index := map[string]int{}
	for _, e := range entries {
		key := e.RawValue
		if e.EnvName != "" {
			key = "env:" + e.EnvName
		}
		if i, ok := index[key]; ok {
			groups[i].Entries = append(groups[i].Entries, e)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, tokenGroup{Entries: []authEntry{e}})
	}
	return groups
}

func uniqueHosts(entries []authEntry) []string {
	var out []string
	for _, e := range entries {
		out = appendUnique(out, e.Host)
	}
	return out
}

func firstNonEmpty(list ...string) string {
	for _, s := range list {
		if s != "" {
			return s
		}
	}
	return ""
}
