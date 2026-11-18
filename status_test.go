package main

import (
	"encoding/base64"
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

func makeJWT(t *testing.T, expIn time.Duration) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	payload := map[string]any{"sub": "test", "exp": time.Now().Add(expIn).Unix()}
	return enc(map[string]string{"alg": "none", "typ": "JWT"}) + "." + enc(payload) + ".sig"
}

func entry(file, prefix, value string) authEntry {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return authEntry{
		File:      file,
		Prefix:    prefix,
		Host:      hostOf(prefix),
		ProjectID: projectIDOf(prefix),
		Param:     "_authToken",
		RawValue:  value,
		Value:     value,
	}
}

func TestCheckGitLabTokenValid(t *testing.T) {
	expiry := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v4/personal_access_tokens/self":
			fmt.Fprintf(w, `{"name":"ci-token","scopes":["api","read_repository"],"active":true,"revoked":false,"expires_at":%q,"last_used_at":"2026-11-01T08:00:00Z"}`, expiry)
		case strings.HasPrefix(r.URL.Path, "/api/v4/projects/"):
			if strings.HasSuffix(r.URL.Path, "192") {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	defer func() { gitlabAPIBase = old }()

	rep := checkToken([]authEntry{
		entry("f", "//git.example.com/api/v4/projects/192/packages/npm/", "glpat-good"),
		entry("f", "//git.example.com/api/v4/projects/999/packages/npm/", "glpat-good"),
	})
	if rep.Kind != "gitlab-pat" || rep.Valid == nil || !*rep.Valid {
		t.Fatalf("应当有效: %+v", rep)
	}
	if rep.ExpiresAt != expiry || rep.ExpiresIn == "" {
		t.Errorf("到期信息 = %q / %q", rep.ExpiresAt, rep.ExpiresIn)
	}
	if rep.Entries[0].Access != "ok" || rep.Entries[1].Access != "denied" {
		t.Errorf("条目权限 = %+v", rep.Entries)
	}
	if rep.Masked != "glpat-…good" {
		t.Errorf("掩码 = %q", rep.Masked)
	}
}

func TestCheckGitLabTokenRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	defer func() { gitlabAPIBase = old }()

	rep := checkToken([]authEntry{entry("f", "//git.example.com/", "glpat-bad")})
	if rep.Valid == nil || *rep.Valid {
		t.Fatalf("应当无效: %+v", rep)
	}
	if rep.Error == "" {
		t.Error("应当给出原因")
	}
	if worstCode([]tokenReport{rep}) != 1 {
		t.Errorf("退出码应为 1")
	}
}

func TestCheckGitLabTokenExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":"old","scopes":["api"],"active":true,"revoked":false,"expires_at":"2020-01-01"}`)
	}))
	defer srv.Close()
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	defer func() { gitlabAPIBase = old }()

	rep := checkToken([]authEntry{entry("f", "//git.example.com/", "glpat-old")})
	if rep.Valid == nil || *rep.Valid || !rep.Expired {
		t.Fatalf("应当已过期: %+v", rep)
	}
}

func TestCheckGitLabTokenUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	defer func() { gitlabAPIBase = old }()

	rep := checkToken([]authEntry{entry("f", "//git.example.com/", "glpat-x")})
	if rep.Valid != nil {
		t.Errorf("500 时不应断言有效/无效: %+v", rep)
	}
	if worstCode([]tokenReport{rep}) != 2 {
		t.Errorf("退出码应为 2")
	}
}

func TestCheckJWT(t *testing.T) {
	good := checkToken([]authEntry{entry("f", "//k8s.example.com/", makeJWT(t, time.Hour))})
	if good.Kind != "jwt" || good.Valid == nil || !*good.Valid {
		t.Fatalf("JWT 应当有效: %+v", good)
	}
	if !strings.Contains(good.ExpiresIn, "分钟") && !strings.Contains(good.ExpiresIn, "小时") {
		t.Errorf("剩余时间 = %q", good.ExpiresIn)
	}

	bad := checkToken([]authEntry{entry("f", "//k8s.example.com/", makeJWT(t, -time.Hour))})
	if bad.Valid == nil || *bad.Valid || !bad.Expired {
		t.Fatalf("过期 JWT 应判为无效: %+v", bad)
	}
}

func TestCheckWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/whoami" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer npm_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"username":"alice"}`)
	}))
	defer srv.Close()
	old := whoamiURL
	whoamiURL = func(registry string) string { return srv.URL + "/-/whoami" }
	defer func() { whoamiURL = old }()

	rep := checkToken([]authEntry{entry("f", "//registry.npmjs.org/", "npm_good")})
	if rep.Kind != "npm" || rep.Valid == nil || !*rep.Valid {
		t.Fatalf("应当有效: %+v", rep)
	}
	if !strings.Contains(strings.Join(rep.Details, ","), "alice") {
		t.Errorf("details = %v", rep.Details)
	}

	bad := checkToken([]authEntry{entry("f", "//registry.npmjs.org/", "npm_bad")})
	if bad.Valid == nil || *bad.Valid {
		t.Errorf("应当无效: %+v", bad)
	}
}

func TestCheckBasicAuthEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-inside" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"name":"b64","scopes":["api"],"active":true,"revoked":false}`)
	}))
	defer srv.Close()
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	defer func() { gitlabAPIBase = old }()

	e := entry("f", "//git.example.com/", base64.StdEncoding.EncodeToString([]byte("alice:glpat-inside")))
	e.Param = "_auth"
	rep := checkToken([]authEntry{e})
	if rep.Kind != "gitlab-pat" || rep.Valid == nil || !*rep.Valid {
		t.Fatalf("应从 _auth 里解出 PAT: %+v", rep)
	}
	if strings.Contains(strings.Join(rep.Details, ","), "alice") {
		t.Errorf("不该把用户名当 token 露出来: %v", rep.Details)
	}
}

func TestEnvValueMissing(t *testing.T) {
	e := entry("f", "//r.example.com/", "")
	e.EnvName = "NOT_SET_ANYWHERE"
	rep := checkToken([]authEntry{e})
	if rep.Valid == nil || *rep.Valid {
		t.Errorf("环境变量缺失应判为无效: %+v", rep)
	}
	if !strings.Contains(rep.Error, "NOT_SET_ANYWHERE") {
		t.Errorf("错误里应当点出环境变量名: %q", rep.Error)
	}
}

func TestRenderCheckNoColor(t *testing.T) {
	rep := tokenReport{Masked: "glpat-…good", Kind: "gitlab-pat", Valid: boolPtr(true),
		Hosts: []string{"git.example.com"}, ExpiresIn: "2027-03-31 到期（剩 500 天）",
		Entries: []entryReport{{File: "f", Registry: "https://git.example.com/api/v4/projects/192/packages/npm/", Access: "ok"}}}
	out := renderCheck("/tmp/.npmrc", &npmrcFile{Path: "/tmp/.npmrc", Auth: []authEntry{{}}}, []tokenReport{rep}, false)
	if strings.Contains(out, "\x1b[") {
		t.Error("no-color 下不该有转义序列")
	}
	for _, want := range []string{"glpat-…good", "有效", "2027-03-31", "/api/v4/projects/192/packages/npm/", "GitLab PAT"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestRunSetTokenRollsBackWhenTokenRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	defer func() { gitlabAPIBase = old }()

	orig := "//git.example.com/:_authToken=glpat-old\n"
	path := writeTemp(t, orig)

	code := runSetToken([]string{"--file", path, "--token", "glpat-rejected"})
	if code != 2 {
		t.Fatalf("退出码 = %d, want 2", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != orig {
		t.Errorf("被拒后应自动还原，现在是:\n%s", got)
	}
}

func TestRunSetTokenEndToEnd(t *testing.T) {
	path := writeTemp(t, strings.Join([]string{
		"registry=https://git.example.com/api/v4/packages/npm/",
		"//git.example.com/api/v4/projects/1/packages/npm/:_authToken=glpat-old",
		"//git.example.com/api/v4/projects/2/packages/npm/:_authToken=glpat-old",
		"//other.example.com/:_authToken=${KEEP_ENV}",
	}, "\n"))
	t.Setenv("KEEP_ENV", "env-value")

	code := runSetToken([]string{"--file", path, "--token", "glpat-new", "--no-verify", "--host", "git.example.com"})
	if code != 0 {
		t.Fatalf("退出码 = %d", code)
	}
	got, _ := os.ReadFile(path)
	if strings.Count(string(got), "glpat-new") != 2 {
		t.Errorf("两条 git.example.com 应更新:\n%s", got)
	}
	if !strings.Contains(string(got), "//other.example.com/:_authToken=${KEEP_ENV}") {
		t.Errorf("别的文件/条目不该动:\n%s", got)
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != 1 {
		t.Errorf("备份文件数 = %d", len(matches))
	}
}
