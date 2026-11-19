package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const httpTimeout = 12 * time.Second

// 这两个函数在测试里会被换成 httptest 的地址
var (
	gitlabAPIBase = func(host string) string { return "https://" + host + "/api/v4" }
	whoamiURL     = func(registry string) string { return strings.TrimRight(registry, "/") + "/-/whoami" }
)

type entryReport struct {
	File      string `json:"file"`
	Registry  string `json:"registry"`
	ProjectID string `json:"project_id,omitempty"`
	Access    string `json:"access"` // ok / denied / unknown
	Note      string `json:"note,omitempty"`
}

type tokenReport struct {
	Value     string        `json:"-"`
	Masked    string        `json:"token"`
	Kind      string        `json:"kind"` // gitlab-pat / jwt / npm / unknown
	EnvName   string        `json:"env,omitempty"`
	Valid     *bool         `json:"valid,omitempty"`
	Revoked   bool          `json:"revoked,omitempty"`
	Scopes    []string      `json:"scopes,omitempty"`
	CreatedAt string        `json:"created_at,omitempty"`
	ExpiresAt string        `json:"expires_at,omitempty"`
	ExpiresIn string        `json:"expires_in,omitempty"`
	Expired   bool          `json:"expired,omitempty"`
	Details   []string      `json:"details,omitempty"`
	Error     string        `json:"error,omitempty"`
	Hosts     []string      `json:"hosts"`
	Entries   []entryReport `json:"entries"`
}

func boolPtr(b bool) *bool { return &b }

// checkToken 判断 token 类型，并尽量问出「还有效吗、什么时候到期、能访问哪些条目」
func checkToken(entries []authEntry) tokenReport {
	first := entries[0]
	value := first.Value
	// _auth 是 base64(user:password)，真正的 token 在冒号后面
	b64Note := ""
	if first.Param == "_auth" {
		if decoded := decodeBasicAuth(value); decoded != "" {
			value = decoded
			b64Note = "_auth 里 base64 解出的密码"
		}
	}
	rep := tokenReport{Value: value, Masked: maskToken(value), EnvName: first.EnvName}
	if b64Note != "" {
		rep.Details = append(rep.Details, b64Note)
	}
	for _, e := range entries {
		rep.Hosts = appendUnique(rep.Hosts, e.Host)
		rep.Entries = append(rep.Entries, entryReport{File: e.File, Registry: e.Registry(), ProjectID: e.ProjectID})
	}
	first.Value = value // 下面按真实 token 判断类型
	if first.Value == "" {
		rep.Kind = "unknown"
		rep.Valid = boolPtr(false)
		rep.Error = "值是空的" + envHint(first.EnvName)
		return rep
	}

	switch {
	case strings.HasPrefix(first.Value, "glpat-"):
		rep.Kind = "gitlab-pat"
		checkGitLabToken(&rep, first.Host, first.Value)
	case looksLikeJWT(first.Value):
		rep.Kind = "jwt"
		checkJWT(&rep, first.Value)
	case strings.HasPrefix(first.Value, "npm_"):
		rep.Kind = "npm"
		checkWhoami(&rep, first.Registry())
	default:
		rep.Kind = "unknown"
		checkWhoami(&rep, first.Registry())
	}

	// 逐条目看能不能访问（GitLab 项目注册表能问出权限，其它 registry 靠 whoami 已覆盖）
	projCache := map[string]bool{}
	for i, e := range rep.Entries {
		switch {
		case e.ProjectID != "" && rep.Kind == "gitlab-pat":
			ok, known := projCache[e.ProjectID]
			if !known {
				ok = gitlabProjectAccess(first.Host, e.ProjectID, first.Value)
				projCache[e.ProjectID] = ok
			}
			rep.Entries[i].Access = verdict(ok)
			if ok {
				rep.Entries[i].Note = "项目 #" + e.ProjectID
			} else {
				rep.Entries[i].Note = "项目 #" + e.ProjectID + " 不可访问（不存在或该 token 无权限）"
			}
		case rep.Valid != nil && *rep.Valid:
			rep.Entries[i].Access = "ok"
		default:
			rep.Entries[i].Access = "unknown"
		}
	}
	return rep
}

// decodeBasicAuth 解 `_auth` 的 base64(user:password)，返回密码部分
func decodeBasicAuth(v string) string {
	if v == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(v)
	}
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(string(b), ':'); i >= 0 && i+1 < len(b) {
		return string(b)[i+1:]
	}
	return ""
}

func verdict(ok bool) string {
	if ok {
		return "ok"
	}
	return "denied"
}

func envHint(name string) string {
	if name == "" {
		return ""
	}
	return "（环境变量 " + name + " 未设置）"
}

// checkGitLabToken 问 GitLab：这个 PAT 是否有效、什么时候过期、有哪些 scope
func checkGitLabToken(rep *tokenReport, host, token string) {
	base := gitlabAPIBase(host)
	body, code, err := httpJSON("GET", base+"/personal_access_tokens/self", "PRIVATE-TOKEN", token, nil)
	if err != nil {
		rep.Error = "连不上 " + host + ": " + firstLine(err.Error())
		return
	}
	switch code {
	case http.StatusOK:
		var info struct {
			Name       string   `json:"name"`
			Scopes     []string `json:"scopes"`
			Active     bool     `json:"active"`
			Revoked    bool     `json:"revoked"`
			CreatedAt  string   `json:"created_at"`
			ExpiresAt  *string  `json:"expires_at"`
			LastUsedAt string   `json:"last_used_at"`
		}
		if err := json.Unmarshal(body, &info); err != nil {
			rep.Error = "GitLab 返回的内容看不懂: " + firstLine(err.Error())
			return
		}
		rep.Valid = boolPtr(info.Active && !info.Revoked)
		rep.Revoked = info.Revoked
		rep.Scopes = info.Scopes
		rep.CreatedAt = info.CreatedAt
		if info.Name != "" {
			rep.Details = append(rep.Details, "名称「"+info.Name+"」")
		}
		if len(info.Scopes) > 0 {
			rep.Details = append(rep.Details, "scopes: "+strings.Join(info.Scopes, ", "))
		}
		if info.LastUsedAt != "" {
			rep.Details = append(rep.Details, "最近使用 "+shortTime(info.LastUsedAt))
		}
		if info.ExpiresAt == nil || *info.ExpiresAt == "" {
			rep.ExpiresIn = "永不过期"
			return
		}
		rep.ExpiresAt = *info.ExpiresAt
		checkExpiry(rep, *info.ExpiresAt)
	case http.StatusUnauthorized, http.StatusForbidden:
		rep.Valid = boolPtr(false)
		rep.Error = "GitLab 不认这个 token（" + codeText(code) + "），可能已过期、被撤销或复制不全"
	default:
		rep.Error = "GitLab 返回 " + codeText(code) + "，没法判断 token 状态"
	}
}

// checkJWT 不联网，直接看 JWT 里的 exp
func checkJWT(rep *tokenReport, token string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		rep.Error = "看起来像 JWT，但格式不对"
		return
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		rep.Error = "JWT 载荷解不开"
		return
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		rep.Error = "JWT 里没有 exp，无法判断有效期"
		rep.Valid = boolPtr(true)
		return
	}
	exp := time.Unix(claims.Exp, 0)
	rep.ExpiresAt = exp.Format("2006-01-02 15:04")
	if time.Now().After(exp) {
		rep.Valid = boolPtr(false)
		rep.Expired = true
		rep.ExpiresIn = "已过期 " + shortDur(time.Since(exp))
	} else {
		rep.Valid = boolPtr(true)
		rep.ExpiresIn = "剩 " + shortDur(time.Until(exp))
	}
}

// checkWhoami 用 registry 的 /-/whoami 验证（npmjs 支持；GitLab 会 302 到 npmjs，等于问不出来）
func checkWhoami(rep *tokenReport, registry string) {
	url := whoamiURL(registry)
	body, code, err := httpJSON("GET", url, "Authorization", "Bearer "+rep.Value, nil)
	if err != nil {
		rep.Error = "连不上 " + registry + ": " + firstLine(err.Error())
		return
	}
	switch code {
	case http.StatusOK:
		var who struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal(body, &who)
		rep.Valid = boolPtr(true)
		if who.Username != "" {
			rep.Details = append(rep.Details, "登录名 "+who.Username)
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		rep.Valid = boolPtr(false)
		rep.Error = "registry 拒绝了这个 token（" + codeText(code) + "）"
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		rep.Details = append(rep.Details, "registry 没有 /-/whoami，只能靠实际安装/发布验证")
	case http.StatusNotFound:
		rep.Details = append(rep.Details, "registry 没有 /-/whoami，只能靠实际安装/发布验证")
	default:
		rep.Error = "registry 返回 " + codeText(code)
	}
}

// gitlabProjectAccess 看这个 token 能不能读到该 GitLab 项目
func gitlabProjectAccess(host, id, token string) bool {
	url := gitlabAPIBase(host) + "/projects/" + id
	_, code, err := httpJSON("GET", url, "PRIVATE-TOKEN", token, nil)
	return err == nil && code == http.StatusOK
}

func httpJSON(method, url, header, value string, payload []byte) (body []byte, code int, err error) {
	var rdr io.Reader
	if payload != nil {
		rdr = strings.NewReader(string(payload))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set(header, value)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "npm-token/"+version)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, nil
}

func checkExpiry(rep *tokenReport, date string) {
	exp, err := time.Parse("2006-01-02", date)
	if err != nil {
		rep.Error = "到期日看不懂: " + date
		return
	}
	days := int(time.Until(exp.Add(24*time.Hour)).Hours() / 24)
	switch {
	case days < 0:
		rep.Valid = boolPtr(false)
		rep.Expired = true
		rep.ExpiresIn = "已于 " + date + " 过期"
	case days <= 30:
		rep.ExpiresIn = fmt.Sprintf("%s 到期（剩 %d 天，该换了）", date, days)
	default:
		rep.ExpiresIn = fmt.Sprintf("%s 到期（剩 %d 天）", date, days)
	}
}

func codeText(code int) string {
	if t := http.StatusText(code); t != "" {
		return fmt.Sprintf("%d %s", code, t)
	}
	return fmt.Sprintf("%d", code)
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// maskToken 只露头尾
func maskToken(t string) string {
	if t == "" {
		return "(空)"
	}
	for _, prefix := range []string{"glpat-", "npm_", "ghp_", "gho_", "gldt-"} {
		if strings.HasPrefix(t, prefix) && len(t) > len(prefix)+8 {
			return prefix + "…" + t[len(t)-4:]
		}
	}
	if len(t) <= 8 {
		return t[:2] + "****"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

func looksLikeJWT(t string) bool {
	parts := strings.Split(t, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	return err == nil && json.Valid(payload) && strings.Contains(string(payload), "\"exp\"")
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func shortTime(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return s
}

func shortDur(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours() / 24)
	switch {
	case days >= 1:
		return fmt.Sprintf("%d 天", days)
	case d >= time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
}

var errNoAuthEntries = errors.New("这个 .npmrc 里没有 //host/:_authToken 条目")
