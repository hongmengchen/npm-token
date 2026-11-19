package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultRefreshDays 是没指定阈值时的刷新线：剩这么多天以内才换
const defaultRefreshDays = 30

// autoSettings 是 npm-token 自己的本机配置：只有开关和阈值，不放任何 token。
// 路径在 os.UserConfigDir()/npm-token/config.json（macOS 是
// ~/Library/Application Support/npm-token/config.json），权限 0600。
type autoSettings struct {
	AutoRefresh bool   `json:"auto_refresh"`
	IfDays      int    `json:"if_days"`
	File        string `json:"file,omitempty"` // 开关管的那个 .npmrc；空 = 默认 ~/.npmrc
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// settingsPathOverride 只给测试用；生产为空，走用户配置目录。
var settingsPathOverride string

func settingsPath() (string, error) {
	if settingsPathOverride != "" {
		return settingsPathOverride, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "npm-token", "config.json"), nil
}

// loadAutoSettings 没配置过时返回零值（开关关、阈值 0），不报错。
func loadAutoSettings() (autoSettings, error) {
	path, err := settingsPath()
	if err != nil {
		return autoSettings{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return autoSettings{}, nil
	}
	if err != nil {
		return autoSettings{}, err
	}
	var s autoSettings
	if err := json.Unmarshal(b, &s); err != nil {
		return autoSettings{}, fmt.Errorf("%s 读不懂: %w", displayPath(path), err)
	}
	s.IfDays = clampIfDays(s.IfDays)
	return s, nil
}

func clampIfDays(days int) int {
	switch {
	case days <= 0:
		return defaultRefreshDays
	case days > 3650:
		return 3650
	}
	return days
}

func saveAutoSettings(s autoSettings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	s.IfDays = clampIfDays(s.IfDays)
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// 新文件由 writeFileAtomic 的临时文件带 0600，里面也没有秘密，但还是按秘密文件对待
	return writeFileAtomic(path, string(b)+"\n")
}

// targets 判断开关管的是不是这个文件；空 File 表示默认的 ~/.npmrc
func (s autoSettings) targets(path string) bool {
	target := s.File
	if target == "" {
		p, err := defaultNpmrcPath()
		if err != nil {
			return false
		}
		target = p
	}
	return sameFile(target, path)
}

func sameFile(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return aa == bb
}

func absPath(path string) string {
	if p, err := filepath.Abs(path); err == nil {
		return p
	}
	return path
}

// hasRotationScope 判断 token 带的 scope 够不够自我轮换。
// 没拿到 scopes（老版本 GitLab / 假服务）时先放行，真换的时候 403 会给出同样提示。
func hasRotationScope(scopes []string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if s == "api" || s == "self_rotate" {
			return true
		}
	}
	return false
}

const missingScopeHint = "缺 api / self_rotate scope：去 GitLab 头像 → Preferences → Access tokens → Add new token，勾选 `api`（或 `self_rotate`），再用 npm-token set-token 写入（自我轮换要 GitLab 16.10 以上）"

// refreshProblem 说明这组 token 为什么不能自动换新；空串 = 能换。
// 判定和真正换新走同一个 buildRotationPlan，提示不会两处不一致。
func refreshProblem(it tokenItem) string {
	if _, err := buildRotationPlan(it.Entries, it.Rep, "", 0); err != nil {
		return err.Error()
	}
	// buildRotationPlan 在 scopes 未知时放行；开启开关要求更严一点，
	// 因为开启的承诺就是「到期前一定能换」。
	if it.Rep.Kind == "gitlab-pat" && len(it.Rep.Scopes) == 0 {
		return "GitLab 没报告这个 token 的 scopes，没法确认它能不能自我轮换；先 npm-token check 看看，必要时重建一个勾了 `api` 的 PAT"
	}
	return ""
}

type autoProblem struct {
	Item   tokenItem
	Reason string
}

func checkRefreshable(items []tokenItem) []autoProblem {
	var out []autoProblem
	for _, it := range items {
		if reason := refreshProblem(it); reason != "" {
			out = append(out, autoProblem{Item: it, Reason: reason})
		}
	}
	return out
}

// ---------- 命令行 ----------

type autoFlags struct {
	file      string
	ifDays    int
	ifDaysSet bool
	asJSON    bool
}

func parseAutoFlags(name string, args []string) (autoFlags, error) {
	var fl autoFlags
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&fl.file, "file", "", "npmrc 路径（默认 ~/.npmrc）")
	fs.IntVar(&fl.ifDays, "if-days", 0, "还剩多少天以内才换（默认 30）")
	fs.BoolVar(&fl.asJSON, "json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return fl, err
	}
	if fs.NArg() > 0 {
		return fl, fmt.Errorf("多余的参数: %s", strings.Join(fs.Args(), " "))
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "if-days" {
			fl.ifDaysSet = true
		}
	})
	if fl.ifDaysSet && (fl.ifDays <= 0 || fl.ifDays > 3650) {
		return fl, errors.New("--if-days 要在 1 到 3650 之间")
	}
	return fl, nil
}

func runAuto(args []string) int {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	switch action {
	case "on":
		fl, err := parseAutoFlags("auto on", args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
			return 2
		}
		return runAutoOn(fl)
	case "off":
		fl, err := parseAutoFlags("auto off", args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
			return 2
		}
		return runAutoOff(fl)
	case "status":
		fl, err := parseAutoFlags("auto status", args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
			return 2
		}
		return runAutoStatus(fl)
	default:
		fmt.Fprintf(os.Stderr, "未知的 auto 子命令: %s（只有 on / off / status）\n", action)
		return 2
	}
}

func runAutoOn(fl autoFlags) int {
	path, err := resolvePath(fl.file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到家目录: "+err.Error())
		return 2
	}
	items, _, err := loadTokenItems(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读不了 %s: %v\n", path, err)
		return 2
	}
	if len(items) == 0 {
		fmt.Fprintf(os.Stderr, "%s 里没有 //host/:_authToken 条目，没什么可自动换的\n", path)
		return 2
	}

	ifDays := fl.ifDays
	if ifDays == 0 {
		if old, err := loadAutoSettings(); err == nil && old.IfDays > 0 {
			ifDays = clampIfDays(old.IfDays)
		} else {
			ifDays = defaultRefreshDays
		}
	}

	problems := checkRefreshable(items)
	cfgPath, _ := settingsPath()
	if len(problems) > 0 {
		if fl.asJSON {
			return writeAutoJSON(autoJSON{
				Enabled:  false,
				IfDays:   ifDays,
				File:     path,
				Config:   cfgPath,
				Tokens:   autoTokensJSON(items),
				Problems: autoProblemsText(problems),
			}, 2)
		}
		p := painter{on: isTTY(os.Stderr)}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\n", p.red("不能开启自动换新：下面这些 token 没法在后台刷新"))
		for _, pr := range problems {
			fmt.Fprintf(&b, "  %s  %s\n     %s\n", p.red("✗"), p.bold(pr.Item.Rep.Masked), p.yellow(pr.Reason))
		}
		b.WriteString("\n修好后重新跑 npm-token auto on。\n")
		fmt.Fprint(os.Stderr, b.String())
		return 2
	}

	s := autoSettings{AutoRefresh: true, IfDays: ifDays, File: absPath(path)}
	if err := saveAutoSettings(s); err != nil {
		fmt.Fprintf(os.Stderr, "写配置失败: %v\n", err)
		return 2
	}
	if fl.asJSON {
		return writeAutoJSON(autoJSON{
			Enabled: true,
			IfDays:  ifDays,
			File:    path,
			Config:  cfgPath,
			Tokens:  autoTokensJSON(items),
		}, 0)
	}

	p := painter{on: isTTY(os.Stdout)}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  剩 %d 天以内时换\n", p.green("自动换新  已开启"), ifDays)
	fmt.Fprintf(&b, "%s  %s\n", p.dim("文件      "), displayPath(path))
	if cfgPath != "" {
		fmt.Fprintf(&b, "%s  %s\n", p.dim("配置      "), displayPath(cfgPath))
	}
	b.WriteString("\n")
	for _, it := range items {
		b.WriteString(autoTokenLine(p, it, "") + "\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "以后启动 TUI 时，剩不到 %d 天会自动换一组；定时任务里跑 %s 即可。\n",
		ifDays, p.bold("npm-token refresh"))
	b.WriteString(p.yellow("旧 token 换完立刻作废：别的机器、CI 变量里还有同一串的话，记得一起清理。") + "\n")
	fmt.Print(b.String())
	return 0
}

func runAutoOff(fl autoFlags) int {
	path, err := settingsPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到配置目录: "+err.Error())
		return 2
	}
	s, loadErr := loadAutoSettings()
	if loadErr != nil {
		// 配置坏了也照样写成关闭，等于顺手修好
		s = autoSettings{}
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) && !s.AutoRefresh {
		if fl.asJSON {
			return writeAutoJSON(autoJSON{Enabled: false, Config: path}, 0)
		}
		fmt.Println("自动换新  本来就没开启")
		return 0
	}
	wasOn := s.AutoRefresh
	s.AutoRefresh = false
	if err := saveAutoSettings(s); err != nil {
		fmt.Fprintf(os.Stderr, "写配置失败: %v\n", err)
		return 2
	}
	if fl.asJSON {
		return writeAutoJSON(autoJSON{Enabled: false, IfDays: s.IfDays, File: s.File, Config: path}, 0)
	}
	if wasOn {
		fmt.Println("自动换新  已关闭")
	} else {
		fmt.Println("自动换新  本来就没开启")
	}
	return 0
}

func runAutoStatus(fl autoFlags) int {
	s, err := loadAutoSettings()
	if err != nil {
		cfg, _ := settingsPath()
		fmt.Fprintf(os.Stderr, "%v\n删掉它重来：rm '%s'，或跑 npm-token auto off\n", err, cfg)
		return 2
	}
	path, err := resolvePath(fl.file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到家目录: "+err.Error())
		return 2
	}
	if fl.file == "" && s.File != "" {
		path = s.File
	}
	ifDays := clampIfDays(s.IfDays)
	if fl.ifDaysSet {
		ifDays = fl.ifDays
	}
	cfgPath, _ := settingsPath()

	items, _, loadErr := loadTokenItems(path)
	var problems []autoProblem
	if loadErr == nil {
		problems = checkRefreshable(items)
	}

	if fl.asJSON {
		out := autoJSON{
			Enabled: s.AutoRefresh,
			IfDays:  ifDays,
			File:    path,
			Config:  cfgPath,
			Tokens:  autoTokensJSON(items),
		}
		if loadErr != nil {
			out.Problems = []string{loadErr.Error()}
		} else {
			out.Problems = autoProblemsText(problems)
		}
		code := 0
		if loadErr != nil {
			code = 2
		}
		return writeAutoJSON(out, code)
	}

	p := painter{on: isTTY(os.Stdout)}
	var b strings.Builder
	if s.AutoRefresh {
		fmt.Fprintf(&b, "%s  剩 %d 天以内时换\n", p.green("自动换新  已开启"), ifDays)
		fmt.Fprintf(&b, "%s  %s\n", p.dim("文件      "), displayPath(path))
		fmt.Fprintf(&b, "%s  TUI 启动时自动换 · 定时跑 npm-token refresh\n", p.dim("触发      "))
		if s.File != "" && !sameFile(s.File, absPath(path)) {
			fmt.Fprintf(&b, "%s  开关指向 %s\n", p.yellow("注意      "), displayPath(s.File))
		}
	} else {
		fmt.Fprintf(&b, "%s\n", p.dim("自动换新  未开启"))
		fmt.Fprintf(&b, "%s  %s\n", p.dim("文件      "), displayPath(path))
		fmt.Fprintf(&b, "%s  npm-token auto on（--if-days 改阈值）\n", p.dim("开启      "))
	}
	if cfgPath != "" {
		fmt.Fprintf(&b, "%s  %s\n", p.dim("配置      "), displayPath(cfgPath))
	}
	b.WriteString("\n")

	if loadErr != nil {
		fmt.Fprintf(&b, "  %s\n", p.red("读不了 "+displayPath(path)+": "+loadErr.Error()))
		fmt.Print(b.String())
		return 2
	}
	if len(items) == 0 {
		b.WriteString("  " + p.dim("没有 //host/:_authToken 条目") + "\n")
		fmt.Print(b.String())
		return 0
	}
	for _, it := range items {
		reason := refreshProblem(it)
		line := autoTokenLine(p, it, reason)
		if reason == "" && s.AutoRefresh {
			if due := autoDue(it, ifDays); due != "" {
				line += "  " + p.yellow("← "+due)
			}
		}
		b.WriteString(line + "\n")
	}
	if len(problems) > 0 {
		b.WriteString("\n" + p.dim("关掉开关不影响这些 token 的正常使用；只是到期时得手动换。") + "\n")
	}
	fmt.Print(b.String())
	return 0
}

// autoDue 说明这组 token 会不会在下次 TUI 启动 / refresh 时被换
func autoDue(it tokenItem, ifDays int) string {
	rep := it.Rep
	if rep.Kind != "gitlab-pat" || rep.Valid == nil || !*rep.Valid {
		return ""
	}
	if rep.ExpiresAt == "" {
		return ""
	}
	left, ok := daysLeft(rep.ExpiresAt)
	if !ok {
		return ""
	}
	if left <= ifDays {
		return fmt.Sprintf("已到期线内（剩 %d 天），下次启动就换", left)
	}
	return ""
}

func autoTokenLine(p painter, it tokenItem, reason string) string {
	rep := it.Rep
	label := p.dim(kindLabel(rep.Kind))
	if reason != "" {
		return fmt.Sprintf("  %s  %s  %s\n     %s", p.red("✗"), p.bold(rep.Masked), label, p.yellow(reason))
	}
	var meta []string
	if len(rep.Scopes) > 0 {
		meta = append(meta, "scopes: "+strings.Join(rep.Scopes, ", "))
	}
	if rep.ExpiresIn != "" {
		meta = append(meta, rep.ExpiresIn)
	}
	line := fmt.Sprintf("  %s  %s  %s  %s", p.green("✓"), p.bold(rep.Masked), label, p.green("可自动换新"))
	if len(meta) > 0 {
		line += "  " + p.dim(strings.Join(meta, " · "))
	}
	return line
}

// ---------- JSON ----------

type autoJSON struct {
	Enabled  bool            `json:"enabled"`
	IfDays   int             `json:"if_days"`
	File     string          `json:"file,omitempty"`
	Config   string          `json:"config"`
	Tokens   []autoTokenJSON `json:"tokens,omitempty"`
	Problems []string        `json:"problems,omitempty"`
}

type autoTokenJSON struct {
	Token       string   `json:"token"` // 掩码
	Kind        string   `json:"kind"`
	Refreshable bool     `json:"refreshable"`
	Reason      string   `json:"reason,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	ExpiresIn   string   `json:"expires_in,omitempty"`
}

func autoTokensJSON(items []tokenItem) []autoTokenJSON {
	var out []autoTokenJSON
	for _, it := range items {
		reason := refreshProblem(it)
		out = append(out, autoTokenJSON{
			Token:       it.Rep.Masked,
			Kind:        it.Rep.Kind,
			Refreshable: reason == "",
			Reason:      reason,
			Scopes:      it.Rep.Scopes,
			ExpiresAt:   it.Rep.ExpiresAt,
			ExpiresIn:   it.Rep.ExpiresIn,
		})
	}
	return out
}

func autoProblemsText(problems []autoProblem) []string {
	var out []string
	for _, pr := range problems {
		out = append(out, pr.Item.Rep.Masked+"："+pr.Reason)
	}
	return out
}

func writeAutoJSON(v autoJSON, code int) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 2
	}
	return code
}
