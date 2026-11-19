package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testOldToken = "glpat-oldtoken1234"
	testNewToken = "glpat-newtoken5678"
)

type rotateFake struct {
	oldToken   string
	newToken   string
	expiresAt  string // 旧 token 的到期日
	createdAt  string // 旧 token 的创建日
	rejectNew  bool   // 新 token 复验返回 401
	rotateCode int    // 非 0 时 POST rotate 直接失败
	rotateBody string
	rotations  int
	rotated    bool // 换过之后旧 token 立刻失效（真实 GitLab 行为）
	rotateEcho string
}

func newRotateFake(t *testing.T) (*rotateFake, *httptest.Server) {
	f := &rotateFake{
		oldToken:  testOldToken,
		newToken:  testNewToken,
		expiresAt: time.Now().AddDate(0, 0, 300).Format(dateLayout),
		createdAt: time.Now().AddDate(0, 0, -65).Format(time.RFC3339),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("PRIVATE-TOKEN")
		switch {
		case r.URL.Path == "/api/v4/personal_access_tokens/self/rotate" && r.Method == http.MethodPost:
			if f.rotateCode != 0 {
				w.WriteHeader(f.rotateCode)
				fmt.Fprint(w, f.rotateBody)
				return
			}
			if tok != f.oldToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.rotations++
			f.rotated = true
			exp := f.expiresAt
			var body struct {
				ExpiresAt *string `json:"expires_at"`
			}
			if json.NewDecoder(r.Body).Decode(&body) == nil && body.ExpiresAt != nil {
				exp = *body.ExpiresAt
				f.rotateEcho = exp
			}
			fmt.Fprintf(w, `{"token":%q,"expires_at":%q,"active":true}`, f.newToken, exp)
		case r.URL.Path == "/api/v4/personal_access_tokens/self":
			switch {
			case tok == f.oldToken:
				if f.rotated {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				fmt.Fprintf(w, `{"name":"ci","scopes":["api"],"active":true,"revoked":false,"created_at":%q,"expires_at":%q}`,
					f.createdAt, f.expiresAt)
			case tok == f.newToken:
				if f.rejectNew {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				fmt.Fprintf(w, `{"name":"ci","scopes":["api"],"active":true,"revoked":false,"created_at":%q,"expires_at":%q}`,
					f.createdAt, f.expiresAt)
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		case strings.HasPrefix(r.URL.Path, "/api/v4/projects/"):
			fmt.Fprint(w, `{"id":1}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	t.Cleanup(func() { gitlabAPIBase = old })
	return f, srv
}

func rotateNpmrc() string {
	return strings.Join([]string{
		"registry=https://git.example.com/api/v4/packages/npm/",
		"//git.example.com/api/v4/projects/1/packages/npm/:_authToken=" + testOldToken,
		"//git.example.com/api/v4/projects/2/packages/npm/:_authToken=" + testOldToken,
		"strict-ssl=false",
	}, "\n")
}

func mustPlan(t *testing.T, path, expiresAt string, days int) rotatePlan {
	t.Helper()
	f, err := parseNpmrc(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildRotationPlan(f.Auth, checkToken(f.Auth), expiresAt, days)
	if err != nil {
		t.Fatalf("buildRotationPlan: %v", err)
	}
	return plan
}

func TestPlanExpiry(t *testing.T) {
	rep := tokenReport{ExpiresAt: "2027-01-01", CreatedAt: "2026-01-01T00:00:00Z"}
	cases := []struct {
		name     string
		rep      tokenReport
		explicit string
		days     int
		want     string
		wantErr  string
	}{
		{name: "显式日期", rep: rep, explicit: time.Now().AddDate(1, 0, 0).Format(dateLayout),
			want: time.Now().AddDate(1, 0, 0).Format(dateLayout)},
		{name: "按天数", rep: rep, days: 90, want: time.Now().AddDate(0, 0, 90).Format(dateLayout)},
		{name: "两个都给", rep: rep, explicit: "2027-01-01", days: 30, wantErr: "只能给一个"},
		{name: "格式不对", rep: rep, explicit: "2027/01/01", wantErr: "YYYY-MM-DD"},
		{name: "已经过去", rep: rep, explicit: "2000-01-01", wantErr: "已经过去"},
		{name: "天数太大", rep: rep, days: 3651, wantErr: "3650"},
		{name: "原 token 不过期", rep: tokenReport{ExpiresAt: ""}, want: ""},
		{name: "沿用有效期", rep: rep, want: time.Now().AddDate(0, 0, 365).Format(dateLayout)},
		{name: "短有效期", rep: tokenReport{ExpiresAt: "2026-02-01", CreatedAt: "2026-01-01T00:00:00Z"},
			want: time.Now().AddDate(0, 0, 31).Format(dateLayout)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, note, err := planExpiry(c.rep, c.explicit, c.days)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want 含 %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Errorf("expiry = %q, want %q", got, c.want)
			}
			if note == "" {
				t.Error("应当说明到期日是怎么来的")
			}
		})
	}
}

func TestBuildRotationPlanRejects(t *testing.T) {
	t.Run("npm token", func(t *testing.T) {
		plan, err := buildRotationPlan([]authEntry{entry("f", "//registry.npmjs.org/", "npm_abcdef")}, tokenReport{Kind: "npm"}, "", 0)
		if err == nil || !strings.Contains(err.Error(), "2FA") {
			t.Fatalf("err = %v", err)
		}
		_ = plan
	})
	t.Run("JWT", func(t *testing.T) {
		_, err := buildRotationPlan([]authEntry{entry("f", "//k.example.com/", "a.b.c")}, tokenReport{Kind: "jwt"}, "", 0)
		if err == nil || !strings.Contains(err.Error(), "签发方") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("失效 token", func(t *testing.T) {
		_, err := buildRotationPlan([]authEntry{entry("f", "//git.example.com/", testOldToken)},
			tokenReport{Kind: "gitlab-pat", Valid: boolPtr(false)}, "", 0)
		if err == nil || !strings.Contains(err.Error(), "失效") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("全是环境变量", func(t *testing.T) {
		e := entry("f", "//git.example.com/", "")
		e.EnvName = "MY_TOKEN"
		_, err := buildRotationPlan([]authEntry{e}, tokenReport{Kind: "gitlab-pat", Valid: boolPtr(true)}, "", 0)
		if err == nil || !strings.Contains(err.Error(), "环境变量") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("_auth 写法", func(t *testing.T) {
		e := entry("f", "//git.example.com/", testOldToken)
		e.Param = "_auth"
		_, err := buildRotationPlan([]authEntry{e}, tokenReport{Kind: "gitlab-pat", Valid: boolPtr(true)}, "", 0)
		if err == nil || !strings.Contains(err.Error(), "_authToken") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("跳过环境变量行", func(t *testing.T) {
		e := entry("f", "//git.example.com/", "")
		e.EnvName = "MY_TOKEN"
		plan, err := buildRotationPlan([]authEntry{entry("f", "//git.example.com/", testOldToken), e},
			tokenReport{Kind: "gitlab-pat", Valid: boolPtr(true)}, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Skipped != 1 || len(plan.Entries) != 1 {
			t.Errorf("plan = %+v", plan)
		}
	})
}

func TestPerformRotationEndToEnd(t *testing.T) {
	fake, _ := newRotateFake(t)
	path := writeTemp(t, rotateNpmrc())
	plan := mustPlan(t, path, "", 0)

	out := performRotation(path, plan, false)
	if out.Err != nil {
		t.Fatalf("err = %v（verify=%+v）", out.Err, out.Verify)
	}
	if !out.Rotated || out.Updated != 2 || out.Recovery != "" {
		t.Errorf("out = %+v", out)
	}
	if fake.rotations != 1 {
		t.Errorf("rotations = %d", fake.rotations)
	}
	if fake.rotateEcho == "" {
		t.Error("应当显式带上 expires_at，否则实例会把有效期重置成默认值")
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), testOldToken) {
		t.Errorf("旧 token 应被换掉:\n%s", got)
	}
	if strings.Count(string(got), testNewToken) != 2 {
		t.Errorf("两条都该更新:\n%s", got)
	}
	if !strings.Contains(string(got), "strict-ssl=false") {
		t.Errorf("其它配置不该动:\n%s", got)
	}
	if out.Backup == "" {
		t.Fatal("应当有备份")
	}
	if b, err := os.ReadFile(out.Backup); err != nil || !strings.Contains(string(b), testOldToken) {
		t.Errorf("备份里应有旧 token: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".npm-token-recovery-*")); len(matches) != 0 {
		t.Errorf("复验通过后恢复文件应删掉，还剩 %v", matches)
	}
	// 换新后的 token 本身不能出现在 JSON 输出里
	v := rotateJSON{File: path, Host: out.Host, Token: plan.Old.Masked, Rotated: true,
		ExpiresAt: out.NewExpiry, Updated: out.Updated, Backup: out.Backup}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), testNewToken) || strings.Contains(string(b), testOldToken) {
		t.Errorf("JSON 不该带明文 token: %s", b)
	}
}

func TestPerformRotationVerifyFailedKeepsRecovery(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.rejectNew = true
	path := writeTemp(t, rotateNpmrc())
	plan := mustPlan(t, path, "", 0)

	out := performRotation(path, plan, false)
	if out.Err == nil {
		t.Fatal("复验失败时应当报错")
	}
	if !out.Rotated || out.Recovery == "" {
		t.Fatalf("out = %+v", out)
	}
	if code := rotateExitCode(out); code != 1 {
		t.Errorf("退出码 = %d, want 1", code)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), testNewToken) {
		t.Errorf("新 token 已经生效，文件里应当已经是新值:\n%s", got)
	}
	rec, err := os.ReadFile(out.Recovery)
	if err != nil || strings.TrimSpace(string(rec)) != testNewToken {
		t.Errorf("恢复文件里应有新 token: %v", err)
	}
	if info, err := os.Stat(out.Recovery); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("恢复文件权限 = %v, want 0600", info.Mode().Perm())
	}
}

func TestPerformRotationRotateFailedChangesNothing(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.rotateCode = http.StatusForbidden
	fake.rotateBody = `{"message":"403 Forbidden - Not allowed to rotate this token"}`
	orig := rotateNpmrc()
	path := writeTemp(t, orig)
	plan := mustPlan(t, path, "", 0)

	out := performRotation(path, plan, false)
	if out.Err == nil || out.Rotated {
		t.Fatalf("out = %+v", out)
	}
	if code := rotateExitCode(out); code != 2 {
		t.Errorf("退出码 = %d, want 2", code)
	}
	if !strings.Contains(out.Err.Error(), "self_rotate") {
		t.Errorf("应提示 scope 要求: %v", out.Err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != orig {
		t.Errorf("失败时不该改文件:\n%s", got)
	}
	if matches, _ := filepath.Glob(path + ".bak.*"); len(matches) != 0 {
		t.Errorf("失败时不该留备份: %v", matches)
	}
}

func TestRefreshSkipsFreshToken(t *testing.T) {
	newRotateFake(t) // 旧 token 还剩 300 天
	path := writeTemp(t, rotateNpmrc())
	orig, _ := os.ReadFile(path)

	code := runRotate([]string{"--file", path, "--if-days", "30"}, 30)
	if code != 0 {
		t.Fatalf("退出码 = %d", code)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(orig) {
		t.Errorf("还早的 token 不该换:\n%s", got)
	}
}

func TestRefreshRotatesSoonToExpire(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.expiresAt = time.Now().AddDate(0, 0, 10).Format(dateLayout)
	path := writeTemp(t, rotateNpmrc())

	code := runRotate([]string{"--file", path, "--if-days", "30", "--no-verify"}, 30)
	if code != 0 {
		t.Fatalf("退出码 = %d", code)
	}
	if fake.rotations != 1 {
		t.Errorf("rotations = %d", fake.rotations)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), testNewToken) {
		t.Errorf("文件没更新:\n%s", got)
	}
}

func TestRotateSelectsOneGroupOnly(t *testing.T) {
	path := writeTemp(t, strings.Join([]string{
		"//a.example.com/:_authToken=glpat-aaa11111",
		"//b.example.com/:_authToken=glpat-bbb22222",
	}, "\n"))
	code := runRotate([]string{"--file", path, "--dry-run"}, 0)
	if code != 2 {
		t.Errorf("多组 token 时应要求 --host，退出码 = %d", code)
	}
}

func TestViewRotateConfirmWarns(t *testing.T) {
	m := tuiModel{
		path: "/tmp/.npmrc", width: 100,
		rotatePlan: rotatePlan{
			Host: "git.example.com", ExpiresAt: "2027-03-31", ExpiryNote: "沿用原 token 的 365 天有效期",
			Old:     tokenReport{Masked: "glpat-…1234", Kind: "gitlab-pat"},
			Entries: []authEntry{{}, {}}, Skipped: 1,
		},
	}
	got := ansiEscape.ReplaceAllString(m.viewRotateConfirm(), "")
	for _, want := range []string{"git.example.com", "glpat-…1234", "2027-03-31", "跳过 1 条环境变量",
		"立刻失效", "撤销", "enter"} {
		if !strings.Contains(got, want) {
			t.Errorf("确认页缺少 %q:\n%s", want, got)
		}
	}
}

func TestViewRotateResultFailed(t *testing.T) {
	m := tuiModel{path: "/tmp/.npmrc", width: 100, rotateOut: rotateOutcome{
		Host: "git.example.com", Err: fmt.Errorf("连不上 git.example.com"),
	}}
	got := ansiEscape.ReplaceAllString(m.viewRotateResult(), "")
	if !strings.Contains(got, "什么都没改") || !strings.Contains(got, "连不上") {
		t.Errorf("失败页不对:\n%s", got)
	}
}

func TestViewRotateResultRotatedButBroken(t *testing.T) {
	m := tuiModel{path: "/tmp/.npmrc", width: 100, rotateOut: rotateOutcome{
		Host: "git.example.com", Rotated: true, Updated: 2, Recovery: "/tmp/.recovery",
		Err: fmt.Errorf("复验没通过"), Verify: tokenReport{Masked: "glpat-…5678", Kind: "gitlab-pat", Valid: boolPtr(false)},
	}}
	got := ansiEscape.ReplaceAllString(m.viewRotateResult(), "")
	for _, want := range []string{"新 token 已生效", "不能回滚", ".recovery"} {
		if !strings.Contains(got, want) {
			t.Errorf("结果页缺少 %q:\n%s", want, got)
		}
	}
}
