package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func configNpmrc(t *testing.T) *npmrcFile {
	t.Helper()
	t.Setenv("MY_NPM_TOKEN", "glpat-fromenv")
	f, err := parseNpmrc(writeTemp(t, strings.Join([]string{
		"# 注释不算条目",
		"registry=https://registry.npmjs.org/",
		"@corp:registry=https://git.example.com/api/v4/packages/npm/",
		"//git.example.com/api/v4/projects/192/packages/npm/:_authToken=glpat-literal",
		"//git.example.com/:username=alice",
		"//git.example.com/:_password=\"hunter2\"",
		"//other.example.com/:_authToken=${MY_NPM_TOKEN}",
		"strict-ssl=false",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParseNpmrcConfigLines(t *testing.T) {
	f := configNpmrc(t)

	if len(f.Config) != 7 {
		t.Fatalf("配置行数 = %d, want 7（注释不算）: %+v", len(f.Config), f.Config)
	}
	if f.Config[0].LineNo != 2 || f.Config[0].Key != "registry" || f.Config[0].Kind != "registry" {
		t.Errorf("第一条 = %+v", f.Config[0])
	}
	if c := f.Config[1]; c.Key != "@corp:registry" || c.Scope != "corp" {
		t.Errorf("scope 行 = %+v", c)
	}

	// token 行归到 auth，值必须打码
	authLine := f.Config[2]
	if authLine.Kind != "auth" || !authLine.Secret {
		t.Errorf("token 行 = %+v", authLine)
	}
	if strings.Contains(authLine.Value, "glpat-literal") {
		t.Errorf("token 明文漏出来了: %q", authLine.Value)
	}

	// 用户名不是秘密，原样显示，而且不算 auth
	user := f.Config[3]
	if user.Kind != "setting" || user.Secret || user.Value != "alice" {
		t.Errorf("username 行 = %+v", user)
	}

	// 密码类要打码，引号也去掉
	pw := f.Config[4]
	if !pw.Secret || strings.Contains(pw.Value, "hunter2") {
		t.Errorf("password 行 = %+v", pw)
	}

	// ${VAR} 本身没暴露秘密，原样保留
	env := f.Config[5]
	if env.Secret || env.Value != "${MY_NPM_TOKEN}" || env.Kind != "auth" {
		t.Errorf("环境变量行 = %+v", env)
	}

	settings := f.Settings()
	if len(settings) != 5 {
		t.Fatalf("Settings 行数 = %d, want 5（去掉两条 auth）: %+v", len(settings), settings)
	}
	for _, c := range settings {
		if c.Kind == "auth" {
			t.Errorf("Settings 里不该有认证行: %+v", c)
		}
	}
}

func TestConfigJSONNeverHasRawToken(t *testing.T) {
	f := configNpmrc(t)
	b, err := json.Marshal(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"glpat-literal", "hunter2"} {
		if strings.Contains(string(b), raw) {
			t.Errorf("JSON 里有明文 %q: %s", raw, b)
		}
	}
	if !strings.Contains(string(b), "alice") {
		t.Errorf("非秘密的 username 应当保留: %s", b)
	}
}

func TestRenderCheckListsSettings(t *testing.T) {
	f := configNpmrc(t)
	out := renderCheck("/tmp/.npmrc", f, []tokenReport{{Masked: "glpat-…eral", Kind: "gitlab-pat", Valid: boolPtr(true)}}, false)
	for _, want := range []string{"其它配置", "//git.example.com/:username = alice", "strict-ssl = false", "5 条其它配置"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q:\n%s", want, out)
		}
	}
	for _, raw := range []string{"glpat-literal", "hunter2"} {
		if strings.Contains(out, raw) {
			t.Errorf("输出里有明文 %q:\n%s", raw, out)
		}
	}
}

func TestViewConfigShowsEntries(t *testing.T) {
	f := configNpmrc(t)
	m := tuiModel{path: "/tmp/.npmrc", width: 120, config: f.Config}
	got := ansiEscape.ReplaceAllString(m.viewConfig(), "")
	for _, want := range []string{"7 条配置", "registry", "@corp:registry", "alice", "strict-ssl = false", "esc", "打码"} {
		if !strings.Contains(got, want) {
			t.Errorf("条目页缺少 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "glpat-literal") {
		t.Errorf("条目页漏了 token 明文:\n%s", got)
	}
	if !strings.Contains(got, "4") { // 行号
		t.Errorf("应当带行号:\n%s", got)
	}
}

func TestViewConfigScrolls(t *testing.T) {
	var cfg []configLine
	for i := 0; i < 40; i++ {
		cfg = append(cfg, configLine{LineNo: i + 1, Key: "k", Value: "v", Kind: "setting"})
	}
	m := tuiModel{path: "/tmp/.npmrc", width: 120, height: 20, config: cfg, configScroll: 30}
	got := ansiEscape.ReplaceAllString(m.viewConfig(), "")
	if !strings.Contains(got, "pgup/pgdn") {
		t.Errorf("条目多时应有翻页提示:\n%s", got)
	}
	if strings.Contains(got, "  1 ") {
		t.Errorf("滚动后不该还从第 1 行开始:\n%s", got)
	}
}
