package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseNpmrc(t *testing.T) {
	t.Setenv("MY_NPM_TOKEN", "glpat-fromenv")
	path := writeTemp(t, strings.Join([]string{
		"# 注释",
		"registry=https://registry.npmjs.org/",
		"@corp:registry=https://git.example.com/api/v4/projects/192/packages/npm/",
		"",
		"//git.example.com/api/v4/projects/192/packages/npm/:_authToken=glpat-literal",
		"//git.example.com/api/v4/projects/213/packages/npm/:_authToken=${MY_NPM_TOKEN}",
		"//registry.npmjs.org/:_authToken=npm_abcdefghijklmnop",
		"//git.example.com/:_authToken=glpat-instance",
		"//git.example.com/:username=alice",
	}, "\n"))

	f, err := parseNpmrc(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Registries) != 2 {
		t.Fatalf("registry 行数 = %d, want 2", len(f.Registries))
	}
	if f.Registries[1].Scope != "corp" {
		t.Errorf("scope = %q, want corp", f.Registries[1].Scope)
	}
	if len(f.Auth) != 4 {
		t.Fatalf("auth 条目 = %d, want 4: %+v", len(f.Auth), f.Auth)
	}

	e := f.Auth[0]
	if e.Host != "git.example.com" || e.ProjectID != "192" || e.Param != "_authToken" {
		t.Errorf("entry0 = %+v", e)
	}
	if e.Registry() != "https://git.example.com/api/v4/projects/192/packages/npm/" {
		t.Errorf("registry = %q", e.Registry())
	}
	if f.Auth[1].EnvName != "MY_NPM_TOKEN" || f.Auth[1].Value != "glpat-fromenv" {
		t.Errorf("env entry = %+v", f.Auth[1])
	}
	if f.Auth[2].ProjectID != "" || f.Auth[2].Host != "registry.npmjs.org" {
		t.Errorf("npmjs entry = %+v", f.Auth[2])
	}
	if f.Auth[3].ProjectID != "" || f.Auth[3].Prefix != "//git.example.com/" {
		t.Errorf("instance entry = %+v", f.Auth[3])
	}
}

func TestGroupByToken(t *testing.T) {
	f, err := parseNpmrc(writeTemp(t, strings.Join([]string{
		"//a.example.com/:_authToken=same",
		"//a.example.com/api/v4/projects/1/packages/npm/:_authToken=same",
		"//b.example.com/:_authToken=other",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	groups := groupByToken(f.Auth)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if len(groups[0].Entries) != 2 || len(groups[1].Entries) != 1 {
		t.Errorf("分组大小 = %d, %d", len(groups[0].Entries), len(groups[1].Entries))
	}
}

func TestRewriteAuthKeepsEverythingElse(t *testing.T) {
	path := writeTemp(t, strings.Join([]string{
		"registry=https://r.example.com/",
		"//r.example.com/:_authToken=old-token-value",
		"//other.example.com/:_authToken=keep-me",
	}, "\n"))
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := parseNpmrc(path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := rewriteAuth(path, f.Auth[:1], "new-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(backup, path+".bak.") {
		t.Errorf("backup = %q", backup)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "registry=https://r.example.com/\n//r.example.com/:_authToken=new-token-value\n//other.example.com/:_authToken=keep-me"
	if string(got) != want {
		t.Errorf("写入后:\n%s\nwant:\n%s", got, want)
	}

	old, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), "old-token-value") {
		t.Errorf("备份内容不对: %s", old)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("权限 = %o, want 600", st.Mode().Perm())
	}
}

func TestSelectTargets(t *testing.T) {
	f, err := parseNpmrc(writeTemp(t, strings.Join([]string{
		"//a.example.com/api/v4/projects/1/packages/npm/:_authToken=t1",
		"//a.example.com/api/v4/projects/2/packages/npm/:_authToken=t2",
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := selectTargets(f.Auth, "", "", false); err == nil {
		t.Error("两组 token 且没指定范围时应当报错")
	}
	got, err := selectTargets(f.Auth, "a.example.com", "2", false)
	if err != nil || len(got) != 1 || got[0].ProjectID != "2" {
		t.Errorf("--project 选择 = %+v, err=%v", got, err)
	}
	got, err = selectTargets(f.Auth, "", "", true)
	if err != nil || len(got) != 2 {
		t.Errorf("--all = %d 条, err=%v", len(got), err)
	}

	one, _ := parseNpmrc(writeTemp(t, "//a.example.com/:_authToken=same\n//a.example.com/api/v4/projects/9/packages/npm/:_authToken=same\n"))
	got, err = selectTargets(one.Auth, "", "", false)
	if err != nil || len(got) != 2 {
		t.Errorf("单组 token 应默认全改, got=%d err=%v", len(got), err)
	}
}

func TestMaskTokenAndJWT(t *testing.T) {
	if got := maskToken("glpat-abcdefgh1234"); got != "glpat-…1234" {
		t.Errorf("maskToken = %q", got)
	}
	if maskToken("") != "(空)" {
		t.Error("空 token 的掩码不对")
	}
	tok := makeJWT(t, 3600)
	if !looksLikeJWT(tok) {
		t.Error("looksLikeJWT 应认出测试用 JWT")
	}
	if looksLikeJWT("not-a-jwt") {
		t.Error("普通字符串不该被当成 JWT")
	}
}
