package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// 列表页左边有招牌，和 docs/logo.svg 同一套方框 + N
func TestListViewShowsLogo(t *testing.T) {
	m := tuiModel{
		path: "/tmp/.npmrc", width: 100,
		items: []tokenItem{{Rep: tokenReport{
			Masked: "glpat-…abcd", Kind: "gitlab-pat", Hosts: []string{"git.example.com"},
		}}},
	}
	got := ansiEscape.ReplaceAllString(m.viewList(), "")
	for _, want := range []string{"┌───┐", "│ N │", "└───┘"} {
		if !strings.Contains(got, want) {
			t.Errorf("列表页缺招牌 %q:\n%s", want, got)
		}
	}
	if first := strings.SplitN(got, "\n", 2)[0]; !strings.Contains(first, "┌───┐") || !strings.Contains(first, "npm-token") {
		t.Errorf("第一行应是招牌 + 标题，got %q", first)
	}
}

func fakeGitLab(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/personal_access_tokens/self":
			fmt.Fprint(w, `{"name":"tui","scopes":["api"],"active":true,"revoked":false,"expires_at":null}`)
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
	return srv
}

func TestApplyTokenTUI(t *testing.T) {
	fakeGitLab(t)
	path := writeTemp(t, strings.Join([]string{
		"//git.example.com/api/v4/projects/1/packages/npm/:_authToken=glpat-old",
		"@foo:registry=https://git.example.com/api/v4/packages/npm/",
	}, "\n"))

	f, err := parseNpmrc(path)
	if err != nil {
		t.Fatal(err)
	}
	item := tokenItem{Entries: f.Auth, Rep: checkToken(f.Auth)}
	if item.Rep.Valid == nil || !*item.Rep.Valid {
		t.Fatalf("准备阶段应当有效: %+v", item.Rep)
	}

	msg := applyToken(path, item, "glpat-brandnew")
	if msg.err != nil {
		t.Fatalf("applyToken err = %v", msg.err)
	}
	if !msg.wrote || msg.restored {
		t.Errorf("应当写入且未还原: %+v", msg)
	}
	if msg.verify.Valid == nil || !*msg.verify.Valid {
		t.Errorf("复验应当有效: %+v", msg.verify)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "glpat-brandnew") {
		t.Errorf("文件没更新:\n%s", got)
	}
	if !strings.Contains(string(got), "@foo:registry") {
		t.Errorf("registry 行被破坏了:\n%s", got)
	}
	if msg.backup == "" {
		t.Error("应当有备份路径")
	}
}

func TestApplyTokenTUIEnvOnly(t *testing.T) {
	path := writeTemp(t, "//git.example.com/:_authToken=${SOME_TOKEN}\n")
	t.Setenv("SOME_TOKEN", "glpat-fromenv")
	f, err := parseNpmrc(path)
	if err != nil {
		t.Fatal(err)
	}
	item := tokenItem{Entries: f.Auth, Rep: tokenReport{Valid: boolPtr(true)}}
	msg := applyToken(path, item, "glpat-new")
	if msg.err == nil || !strings.Contains(msg.err.Error(), "环境变量") {
		t.Errorf("应提示改环境变量: %+v", msg)
	}
	if msg.skipped != 1 {
		t.Errorf("skipped = %d", msg.skipped)
	}
}
