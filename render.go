package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	cReset  = "\x1b[0m"
	cDim    = "\x1b[2m"
	cGreen  = "\x1b[32m"
	cRed    = "\x1b[31m"
	cYellow = "\x1b[33m"
	cBold   = "\x1b[1m"
)

type painter struct{ on bool }

func (p painter) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return code + s + cReset
}

func (p painter) dim(s string) string    { return p.wrap(cDim, s) }
func (p painter) green(s string) string  { return p.wrap(cGreen, s) }
func (p painter) red(s string) string    { return p.wrap(cRed, s) }
func (p painter) yellow(s string) string { return p.wrap(cYellow, s) }
func (p painter) bold(s string) string   { return p.wrap(cBold, s) }

func renderCheck(path string, f *npmrcFile, reports []tokenReport, color bool) string {
	p := painter{on: color}
	var b strings.Builder

	settings := f.Settings()
	fmt.Fprintf(&b, "%s\n", p.bold(displayPath(path)))
	fmt.Fprintf(&b, "%s\n\n", p.dim(fmt.Sprintf("%d 条认证配置 · %d 个 token · %d 条其它配置",
		len(f.Auth), len(reports), len(settings))))

	needNew, canRefresh := false, false
	for i, r := range reports {
		if i > 0 {
			b.WriteString("\n")
		}
		writeTokenBlock(&b, p, r)
		if r.Valid == nil || !*r.Valid || r.Expired {
			needNew = true
		}
		if r.Kind == "gitlab-pat" && r.Valid != nil && *r.Valid && strings.Contains(r.ExpiresIn, "该换了") {
			canRefresh = true
		}
	}

	if needNew {
		b.WriteString("\n")
		b.WriteString(p.yellow("换 token：") + "GitLab 头像 → Preferences → Access tokens → Add new token（勾 api，设好到期日）\n")
		b.WriteString("            然后 echo '<新 token>' | npm-token set-token --host <主机名>\n")
	}
	if canRefresh {
		b.WriteString("\n")
		b.WriteString(p.yellow("快到期的 token 可以让 GitLab 自己换：") + "npm-token refresh --dry-run 看计划，去掉 --dry-run 就换\n")
	}

	if len(settings) > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s %s\n", p.dim("其它配置"), p.dim("（认证行见上面的条目；完整原文含行号：tui 里按 e）"))
		for _, c := range settings {
			val := c.Value
			if c.Secret {
				val = p.dim(val)
			}
			fmt.Fprintf(&b, "   %s  %s = %s\n", p.dim(fmt.Sprintf("%4d", c.LineNo)), c.Key, val)
		}
	}
	return b.String()
}

func writeTokenBlock(b *strings.Builder, p painter, r tokenReport) {
	mark, status := statusOf(p, r)
	fmt.Fprintf(b, "%s %s  %s  %s\n", mark, p.bold(r.Masked), p.dim(kindLabel(r.Kind)), status)

	var meta []string
	if len(r.Hosts) > 0 {
		meta = append(meta, strings.Join(r.Hosts, ", "))
	}
	meta = append(meta, r.Details...)
	if len(meta) > 0 {
		fmt.Fprintf(b, "   %s\n", p.dim(strings.Join(meta, " · ")))
	}
	if line := expiryLine(r); line != "" {
		fmt.Fprintf(b, "   %s\n", line)
	}
	if r.Error != "" {
		fmt.Fprintf(b, "   %s\n", p.red(r.Error))
	}
	if r.EnvName != "" {
		fmt.Fprintf(b, "   %s\n", p.dim("值来自环境变量 "+r.EnvName))
	}

	if len(r.Entries) == 0 {
		return
	}
	fmt.Fprintf(b, "   %s\n", p.dim("条目"))
	for _, e := range r.Entries {
		line := entryPath(e)
		switch e.Access {
		case "ok":
			fmt.Fprintf(b, "     %s %s", p.green("✓"), line)
		case "denied":
			fmt.Fprintf(b, "     %s %s", p.red("✗"), line)
		default:
			fmt.Fprintf(b, "     %s %s", p.dim("·"), line)
		}
		if e.Note != "" && e.Access == "denied" {
			fmt.Fprintf(b, "  %s", p.red("← "+e.Note))
		}
		b.WriteString("\n")
	}
}

func statusOf(p painter, r tokenReport) (mark, status string) {
	switch {
	case r.Revoked:
		return p.red("✗"), p.red("已撤销")
	case r.Expired:
		return p.red("✗"), p.red("已过期")
	case r.Valid != nil && !*r.Valid:
		return p.red("✗"), p.red("无效")
	case r.Valid != nil && *r.Valid && strings.Contains(r.ExpiresIn, "该换了"):
		return p.yellow("!"), p.yellow("有效，但快到期了")
	case r.Valid != nil && *r.Valid:
		return p.green("✓"), p.green("有效")
	default:
		return p.yellow("?"), p.yellow("判断不了")
	}
}

func expiryLine(r tokenReport) string {
	switch {
	case r.ExpiresIn != "":
		return r.ExpiresIn
	case r.ExpiresAt != "":
		return r.ExpiresAt + " 到期"
	}
	return ""
}

func kindLabel(kind string) string {
	switch kind {
	case "gitlab-pat":
		return "GitLab PAT"
	case "jwt":
		return "JWT"
	case "npm":
		return "npm token"
	default:
		return "未知类型"
	}
}

// entryPath 去掉主机，只留路径，短一点
func entryPath(e entryReport) string {
	reg := e.Registry
	i := strings.Index(reg, "://")
	if i < 0 {
		return reg
	}
	rest := reg[i+3:]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[j:]
	}
	return "/"
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return filepath.Clean(path)
}
