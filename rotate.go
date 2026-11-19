package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	dateLayout = "2006-01-02"
	// 算不出原 token 的有效期时，新 token 默认给一年
	defaultLifetimeDays = 365
)

// rotatePlan 是换新之前算好的一切；--dry-run 和 TUI 确认页都用它
type rotatePlan struct {
	Host       string
	Group      tokenGroup
	Entries    []authEntry // 真正要改的行（字面值、_authToken）
	Skipped    int         // 用环境变量、换不了的行
	ExpiresAt  string      // 新到期日；空 = 不传，由实例默认
	ExpiryNote string      // 到期日是怎么算出来的，给人看
	Old        tokenReport
}

// buildRotationPlan 确认这组 token 能自换，并算好新到期日
func buildRotationPlan(entries []authEntry, rep tokenReport, expiresAt string, days int) (rotatePlan, error) {
	plan := rotatePlan{Host: entries[0].Host, Group: tokenGroup{Entries: entries}, Old: rep}

	switch rep.Kind {
	case "gitlab-pat":
	case "npm":
		return plan, errors.New("npmjs 的 token 不能由程序自动换新：官方接口要求交互式 2FA（密码 + 一次性验证码），bypass-2fa 的 token 又被禁止调用。去 https://www.npmjs.com/settings/~/tokens 生成一个，再用 npm-token set-token 写入")
	case "jwt":
		return plan, errors.New("JWT 由签发方（Vault、CI 之类）续期，本工具换不了")
	default:
		return plan, errors.New("这组 token 认不出类型，只有 GitLab PAT（glpat-…）能自动换新")
	}

	if rep.Valid == nil {
		return plan, fmt.Errorf("没法确认这组 token 的状态：%s", firstNonEmpty(rep.Error, "连不上 GitLab"))
	}
	if !*rep.Valid {
		return plan, fmt.Errorf("这组 token 已经失效（%s），GitLab 不允许对失效/撤销的 token 轮换；去网页新建一个，再用 npm-token set-token 写入",
			firstNonEmpty(rep.Error, rep.ExpiresIn, "无效"))
	}

	var literal []authEntry
	for _, e := range entries {
		if e.EnvName != "" {
			plan.Skipped++
			continue
		}
		if e.Param != "_authToken" {
			return plan, errors.New("这组用的是 `_auth`（base64 用户名:密码）写法，换新只能写回 `_authToken`；先用 set-token 改成 _authToken，或去网页换")
		}
		literal = append(literal, e)
	}
	if len(literal) == 0 {
		return plan, fmt.Errorf("这组条目都用环境变量取值（%s），rotate 改不了环境变量；换环境变量里的值用 set-token",
			strings.Join(uniqueEnvNames(entries), ", "))
	}
	plan.Entries = literal

	exp, note, err := planExpiry(rep, expiresAt, days)
	if err != nil {
		return plan, err
	}
	plan.ExpiresAt, plan.ExpiryNote = exp, note
	return plan, nil
}

// planExpiry 决定新 token 的到期日：显式指定 > 沿用原 token 的有效期
func planExpiry(rep tokenReport, expiresAt string, days int) (expiry, note string, err error) {
	if expiresAt != "" && days > 0 {
		return "", "", errors.New("--expires-at 和 --days 只能给一个")
	}
	today := time.Now()
	if expiresAt != "" {
		if _, err := time.Parse(dateLayout, expiresAt); err != nil {
			return "", "", errors.New("--expires-at 要写成 YYYY-MM-DD，比如 2027-03-31")
		}
		if expiresAt < today.Format(dateLayout) {
			return "", "", fmt.Errorf("--expires-at %s 已经过去了", expiresAt)
		}
		return expiresAt, "按 --expires-at 指定", nil
	}
	if days > 0 {
		if days > 3650 {
			return "", "", errors.New("--days 最多 3650 天")
		}
		return today.AddDate(0, 0, days).Format(dateLayout), fmt.Sprintf("从今天起 %d 天", days), nil
	}
	if rep.ExpiresAt == "" {
		return "", "原 token 不过期，新 token 也不设到期日", nil
	}
	lifetime := defaultLifetimeDays
	if exp, err := time.Parse(dateLayout, rep.ExpiresAt); err == nil {
		if created, err := time.Parse(time.RFC3339, rep.CreatedAt); err == nil {
			if d := int(exp.Sub(created).Hours() / 24); d > 0 && d <= 3650 {
				lifetime = d
			}
		}
	}
	return today.AddDate(0, 0, lifetime).Format(dateLayout),
		fmt.Sprintf("沿用原 token 的 %d 天有效期", lifetime), nil
}

func uniqueEnvNames(entries []authEntry) []string {
	var out []string
	for _, e := range entries {
		if e.EnvName != "" {
			out = appendUnique(out, e.EnvName)
		}
	}
	return out
}

// daysLeft 是距到期还有几天，口径和 checkExpiry 一致
func daysLeft(date string) (int, bool) {
	exp, err := time.Parse(dateLayout, date)
	if err != nil {
		return 0, false
	}
	return int(time.Until(exp.Add(24*time.Hour)).Hours() / 24), true
}

// rotateGitLabToken 调 GitLab 的 self-rotate 接口，返回新 token 和新的到期日。
// 成功那一刻旧 token 就已经作废了。
func rotateGitLabToken(host, token, expiresAt string) (newToken, newExpiry string, err error) {
	payload := []byte("{}")
	if expiresAt != "" {
		payload, _ = json.Marshal(map[string]string{"expires_at": expiresAt})
	}
	body, code, err := httpJSON("POST", gitlabAPIBase(host)+"/personal_access_tokens/self/rotate", "PRIVATE-TOKEN", token, payload)
	if err != nil {
		return "", "", fmt.Errorf("连不上 %s: %s", host, firstLine(err.Error()))
	}
	if code != http.StatusOK && code != http.StatusCreated {
		msg := apiErrorText(body)
		switch code {
		case http.StatusUnauthorized:
			return "", "", errors.New("GitLab 不认这个 token（401），可能已过期或被撤销，不能自换")
		case http.StatusForbidden:
			return "", "", fmt.Errorf("这个 token 不能自换（403）：需要 api 或 self_rotate scope，而且得是 PAT。GitLab 说：%s", msg)
		case http.StatusBadRequest:
			return "", "", fmt.Errorf("GitLab 没同意换（400）：%s", msg)
		case http.StatusNotFound, http.StatusMethodNotAllowed:
			return "", "", fmt.Errorf("这个 GitLab 没有 self-rotate 接口（%s），token 轮换要 GitLab 16.10 以上：%s", codeText(code), msg)
		default:
			return "", "", fmt.Errorf("GitLab 拒绝换新（%s）：%s", codeText(code), msg)
		}
	}
	var resp struct {
		Token     string  `json:"token"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Token == "" {
		return "", "", errors.New("GitLab 说换成功了，但响应里没有新 token（接口版本不对？）")
	}
	if resp.ExpiresAt != nil {
		newExpiry = *resp.ExpiresAt
	}
	return resp.Token, newExpiry, nil
}

// apiErrorText 尽量从 GitLab 的错误 JSON 里挖出一句人话
func apiErrorText(body []byte) string {
	var e struct {
		Message json.RawMessage `json:"message"`
		Error   string          `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		if len(e.Message) > 0 {
			var s string
			if json.Unmarshal(e.Message, &s) == nil && s != "" {
				return s
			}
			var m map[string]any
			if json.Unmarshal(e.Message, &m) == nil {
				var parts []string
				for k, v := range m {
					parts = append(parts, fmt.Sprintf("%s %v", k, v))
				}
				sort.Strings(parts)
				if len(parts) > 0 {
					return strings.Join(parts, "; ")
				}
			}
			return strings.TrimSpace(string(e.Message))
		}
		if e.Error != "" {
			return e.Error
		}
	}
	return firstLine(string(body))
}

type rotateOutcome struct {
	Host      string
	Rotated   bool
	NewExpiry string
	Updated   int
	Skipped   int
	Backup    string
	Recovery  string // 恢复文件：里面有新 token；验证通过后删掉
	Verify    tokenReport
	Err       error
}

// performRotation 是 CLI 和 TUI 共用的动作：换新 → 立刻存恢复文件 → 改写 → 复验。
//
// 顺序很重要：GitLab 一旦换了新，旧 token 立刻作废。所以先把新值落到一个
// 0600 的恢复文件里，再去改 .npmrc；中间任何一步失败都还能把新 token 找回来。
// 复验失败也不能回滚到旧值——旧值已经死了。
func performRotation(path string, plan rotatePlan, noVerify bool) rotateOutcome {
	out := rotateOutcome{Host: plan.Host, Skipped: plan.Skipped}

	newToken, newExpiry, err := rotateGitLabToken(plan.Host, plan.Entries[0].Value, plan.ExpiresAt)
	if err != nil {
		out.Err = err
		return out
	}
	out.Rotated = true
	out.NewExpiry = firstNonEmpty(newExpiry, plan.ExpiresAt)

	recovery, err := writeRecoveryFile(path, newToken)
	if err != nil {
		out.Err = fmt.Errorf("新 token 已经生效，但恢复文件写不了（%v）；请立刻手动更新 %s", err, path)
		return out
	}
	out.Recovery = recovery

	backup, err := rewriteAuth(path, plan.Entries, newToken)
	out.Backup = backup
	if err != nil {
		out.Err = fmt.Errorf("新 token 已经生效，但改写 .npmrc 失败：%v；新值在 %s", err, recovery)
		return out
	}
	out.Updated = len(plan.Entries)

	f, err := parseNpmrc(path)
	if err != nil {
		out.Err = fmt.Errorf("写完了但重读失败：%v；新值在 %s", err, recovery)
		return out
	}
	var fresh []authEntry
	for _, e := range f.Auth {
		if e.Value == newToken {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		out.Err = fmt.Errorf("写完了但在文件里找不到新值；新值在 %s", recovery)
		return out
	}

	if noVerify {
		removeQuietly(recovery)
		out.Recovery = ""
		return out
	}

	out.Verify = checkToken(fresh)
	if out.Verify.Valid != nil && *out.Verify.Valid {
		removeQuietly(recovery)
		out.Recovery = ""
		return out
	}
	out.Err = fmt.Errorf("新 token 已写进 .npmrc，但复验没通过：%s",
		firstNonEmpty(out.Verify.Error, out.Verify.ExpiresIn, "状态未知"))
	return out
}

func writeRecoveryFile(npmrcPath, token string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(npmrcPath), ".npm-token-recovery-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func removeQuietly(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// rotateExitCode：换完了但新 token 被明确拒绝是 1，其它失败是 2
func rotateExitCode(out rotateOutcome) int {
	if out.Err == nil {
		return 0
	}
	if out.Rotated && out.Verify.Valid != nil && !*out.Verify.Valid {
		return 1
	}
	return 2
}

// ---------- 命令行渲染 ----------

func renderRotatePlan(plan rotatePlan, path string, color bool) string {
	p := painter{on: color}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", p.bold(displayPath(path)))
	fmt.Fprintf(&b, "%s\n\n", p.dim("换新计划（--dry-run，什么都没改）"))
	fmt.Fprintf(&b, "   %s %s  %s  %s\n", p.green("✓"), p.bold(plan.Old.Masked), p.dim(kindLabel(plan.Old.Kind)), p.green("有效"))
	fmt.Fprintf(&b, "   %s %s\n", p.dim("主机  "), plan.Host)
	entries := fmt.Sprintf("%d 条认证配置", len(plan.Entries))
	if plan.Skipped > 0 {
		entries += fmt.Sprintf("（跳过 %d 条环境变量）", plan.Skipped)
	}
	fmt.Fprintf(&b, "   %s %s\n", p.dim("条目  "), entries)
	if plan.ExpiresAt != "" {
		fmt.Fprintf(&b, "   %s %s %s\n", p.dim("新到期"), plan.ExpiresAt, p.dim("（"+plan.ExpiryNote+"）"))
	} else {
		fmt.Fprintf(&b, "   %s %s %s\n", p.dim("新到期"), "不设", p.dim("（"+plan.ExpiryNote+"）"))
	}
	b.WriteString("\n")
	b.WriteString(p.yellow("   提醒  ") + "换完旧 token 立刻失效；别的机器、CI 变量里还留着同一串的话，那边会一起断\n")
	b.WriteString("         GitLab 发现旧 token 再被使用，会把同一家族的新 token 也撤销\n")
	return b.String()
}

func renderRotateResult(out rotateOutcome, path string, color bool) string {
	p := painter{on: color}
	var b strings.Builder
	switch {
	case out.Err == nil:
		b.WriteString(p.green("✓ 已换新 token") + "\n")
	case !out.Rotated:
		b.WriteString(p.red("✗ 换新失败，什么都没改") + "\n")
	default:
		b.WriteString(p.yellow("! 新 token 已生效，但收尾没走完") + "\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "   %s %s\n", p.dim("文件  "), displayPath(path))
	if out.Host != "" {
		fmt.Fprintf(&b, "   %s %s\n", p.dim("主机  "), out.Host)
	}
	if out.Rotated {
		if out.NewExpiry != "" {
			fmt.Fprintf(&b, "   %s %s\n", p.dim("新到期"), out.NewExpiry)
		} else {
			fmt.Fprintf(&b, "   %s %s\n", p.dim("新到期"), "不设")
		}
	}
	if out.Updated > 0 {
		line := fmt.Sprintf("%d 条认证配置", out.Updated)
		if out.Skipped > 0 {
			line += fmt.Sprintf("（跳过 %d 条环境变量）", out.Skipped)
		}
		fmt.Fprintf(&b, "   %s %s\n", p.dim("更新  "), line)
	}
	if out.Backup != "" {
		fmt.Fprintf(&b, "   %s %s\n", p.dim("备份  "), displayPath(out.Backup))
	}
	if out.Verify.Masked != "" {
		mark, status := statusOf(p, out.Verify)
		detail := expiryLine(out.Verify)
		if detail != "" {
			status += "，" + detail
		} else if out.Verify.Error != "" {
			status += "：" + out.Verify.Error
		}
		fmt.Fprintf(&b, "   %s %s %s\n", p.dim("复验  "), mark, status)
	}
	if out.Err != nil {
		fmt.Fprintf(&b, "   %s %s\n", p.dim("问题  "), p.red(out.Err.Error()))
	}
	if out.Recovery != "" {
		fmt.Fprintf(&b, "   %s %s\n", p.dim("恢复  "), displayPath(out.Recovery))
	}
	if out.Rotated && out.Err != nil {
		b.WriteString("\n")
		b.WriteString(p.yellow("   旧 token 已经被 GitLab 撤销，不能回滚。") + "\n")
		if out.Recovery != "" {
			b.WriteString("   新 token 同时写在 .npmrc 和恢复文件里，确认无误后可以删掉恢复文件。\n")
		}
	}
	return b.String()
}
