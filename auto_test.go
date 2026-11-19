package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// cmdMsgs 展开 tea.Batch 包起来的一批命令（直接调用 cmd() 只会拿到 BatchMsg）
func cmdMsgs(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		if c != nil {
			out = append(out, c())
		}
	}
	return out
}

// useTempSettings 把本机配置指到临时目录，测试不碰用户的真实配置
func useTempSettings(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	old := settingsPathOverride
	settingsPathOverride = path
	t.Cleanup(func() { settingsPathOverride = old })
	return path
}

// newScopeFake 只答 self 接口，用来模拟 scope 不够 / 没报告的 token
func newScopeFake(t *testing.T, scopes string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/personal_access_tokens/self" {
			fmt.Fprintf(w, `{"name":"ci","scopes":%s,"active":true,"revoked":false}`, scopes)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	old := gitlabAPIBase
	gitlabAPIBase = func(string) string { return srv.URL + "/api/v4" }
	t.Cleanup(func() { gitlabAPIBase = old })
}

func TestAutoSettingsRoundtrip(t *testing.T) {
	cfg := useTempSettings(t)

	if _, err := loadAutoSettings(); err != nil {
		t.Fatalf("没配置过时应该返回零值: %v", err)
	}
	if err := saveAutoSettings(autoSettings{AutoRefresh: true, IfDays: 45, File: "/tmp/.npmrc"}); err != nil {
		t.Fatal(err)
	}
	s, err := loadAutoSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.AutoRefresh || s.IfDays != 45 || s.File != "/tmp/.npmrc" {
		t.Errorf("读回来不对: %+v", s)
	}
	if s.UpdatedAt == "" {
		t.Error("应该记更新时间")
	}

	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("配置权限 = %o, want 600", perm)
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), `"auto_refresh": true`) {
		t.Errorf("配置文件内容不对:\n%s", b)
	}
	for _, secret := range []string{"glpat-", "npm_"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("配置里不该有 token 前缀 %q", secret)
		}
	}
}

func TestClampIfDays(t *testing.T) {
	cases := map[int]int{0: 30, -5: 30, 1: 1, 90: 90, 4000: 3650}
	for in, want := range cases {
		if got := clampIfDays(in); got != want {
			t.Errorf("clampIfDays(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestAutoOnRefusesTokenWithoutScope(t *testing.T) {
	useTempSettings(t)
	newScopeFake(t, `["read_api"]`)
	path := writeTemp(t, "//git.example.com/:_authToken=glpat-readonly1234\n")

	items, _, err := loadTokenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	problems := checkRefreshable(items)
	if len(problems) != 1 {
		t.Fatalf("problems = %+v", problems)
	}
	if reason := problems[0].Reason; !strings.Contains(reason, "self_rotate") || !strings.Contains(reason, "api") {
		t.Errorf("应告诉用户要勾哪个 scope: %q", reason)
	}

	if code := runAuto([]string{"on", "--file", path}); code != 2 {
		t.Errorf("退出码 = %d, want 2", code)
	}
	cfg, _ := settingsPath()
	if _, err := os.Stat(cfg); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("拒绝开启时不该写配置: %v", err)
	}
}

func TestAutoOnRefusesUnknownScopes(t *testing.T) {
	useTempSettings(t)
	newScopeFake(t, `[]`)
	path := writeTemp(t, "//git.example.com/:_authToken=glpat-noscopes123\n")

	if code := runAuto([]string{"on", "--file", path}); code != 2 {
		t.Errorf("拿不到 scopes 时也不能开启，退出码 = %d", code)
	}
}

func TestAutoOnThenRefreshUsesStoredSettings(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.expiresAt = time.Now().AddDate(0, 0, 10).Format(dateLayout)
	useTempSettings(t)
	path := writeTemp(t, rotateNpmrc())

	if code := runAuto([]string{"on", "--file", path, "--if-days", "20"}); code != 0 {
		t.Fatalf("auto on 退出码 = %d", code)
	}
	s, err := loadAutoSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.AutoRefresh || s.IfDays != 20 || !sameFile(s.File, path) {
		t.Fatalf("保存的设置不对: %+v", s)
	}

	// refresh 不带 --file / --if-days：照配置里的文件（10 天 ≤ 20 天）换掉
	if code := runRotate(nil, 30); code != 0 {
		t.Fatalf("refresh 退出码 = %d", code)
	}
	if fake.rotations != 1 {
		t.Errorf("rotations = %d", fake.rotations)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), testNewToken) {
		t.Errorf("文件没更新:\n%s", got)
	}
}

func TestRefreshUsesStoredThreshold(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.expiresAt = time.Now().AddDate(0, 0, 20).Format(dateLayout)
	useTempSettings(t)
	path := writeTemp(t, rotateNpmrc())
	orig, _ := os.ReadFile(path)

	if code := runAuto([]string{"on", "--file", path, "--if-days", "10"}); code != 0 {
		t.Fatalf("auto on 退出码 = %d", code)
	}
	// 剩 20 天，比保存的 10 天阈值远：不换（如果错用默认 30 就会换）
	if code := runRotate(nil, 30); code != 0 {
		t.Fatalf("refresh 退出码 = %d", code)
	}
	if fake.rotations != 0 {
		t.Errorf("不该换，rotations = %d", fake.rotations)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(orig) {
		t.Errorf("文件不该改:\n%s", got)
	}
}

func TestAutoOffKeepsThreshold(t *testing.T) {
	useTempSettings(t)
	if err := saveAutoSettings(autoSettings{AutoRefresh: true, IfDays: 45, File: "/tmp/.npmrc"}); err != nil {
		t.Fatal(err)
	}
	if code := runAuto([]string{"off"}); code != 0 {
		t.Fatalf("退出码 = %d", code)
	}
	s, err := loadAutoSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoRefresh {
		t.Error("应该关掉")
	}
	if s.IfDays != 45 || s.File != "/tmp/.npmrc" {
		t.Errorf("关开关时不该丢阈值/文件: %+v", s)
	}
}

func TestAutoOffWithoutConfig(t *testing.T) {
	useTempSettings(t)
	if code := runAuto([]string{"off", "--json"}); code != 0 {
		t.Errorf("本来没开启也应退出 0，得到 %d", code)
	}
	cfg, _ := settingsPath()
	if _, err := os.Stat(cfg); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("本来没配置就别凭空写一个: %v", err)
	}
}

func TestRefreshSweepSkipsIneligibleGroup(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.expiresAt = time.Now().AddDate(0, 0, 5).Format(dateLayout)
	useTempSettings(t)
	path := writeTemp(t, strings.Join([]string{
		"//git.example.com/:_authToken=" + testOldToken,
		"//registry.npmjs.org/:_authToken=npm_abcdefghijklmnop",
	}, "\n"))

	// npmjs 那组换不了：跳过它，GitLab 那组照换
	code := runRotate([]string{"--file", path}, 30)
	if code != 2 {
		t.Errorf("有组跳过时退出码 = %d, want 2", code)
	}
	if fake.rotations != 1 {
		t.Errorf("rotations = %d", fake.rotations)
	}
}

func TestTuiAutoRotatesOnceOnStartup(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.expiresAt = time.Now().AddDate(0, 0, 10).Format(dateLayout)
	useTempSettings(t)
	path := writeTemp(t, rotateNpmrc())
	if err := saveAutoSettings(autoSettings{AutoRefresh: true, IfDays: 30, File: absPath(path)}); err != nil {
		t.Fatal(err)
	}

	m := newTuiModel(path)
	items, config, err := loadTokenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	mm, cmd := m.Update(tuiLoadedMsg{path: path, items: items, config: config})
	m = mm.(tuiModel)
	if m.state != tuiWorking || cmd == nil {
		t.Fatalf("应该直接进入换新: state=%v", m.state)
	}

	for _, msg := range cmdMsgs(cmd) {
		if _, ok := msg.(tuiRotateDoneMsg); ok {
			mm, _ = m.Update(msg)
			m = mm.(tuiModel)
		}
	}
	if m.state != tuiRotateResult {
		t.Fatalf("state = %v, want tuiRotateResult", m.state)
	}
	if fake.rotations != 1 {
		t.Errorf("rotations = %d", fake.rotations)
	}

	// 再 reload 一次（同一个 model）：不该再换
	items, config, _ = loadTokenItems(path)
	mm, _ = m.Update(tuiLoadedMsg{path: path, items: items, config: config})
	if got := mm.(tuiModel); got.state != tuiList {
		t.Errorf("第二次加载不该再自动换，state = %v", got.state)
	}
	if fake.rotations != 1 {
		t.Errorf("rotations = %d", fake.rotations)
	}
}

func TestTuiAutoSkipsWhenDisabled(t *testing.T) {
	fake, _ := newRotateFake(t)
	fake.expiresAt = time.Now().AddDate(0, 0, 5).Format(dateLayout)
	useTempSettings(t)
	path := writeTemp(t, rotateNpmrc())

	m := newTuiModel(path)
	items, config, _ := loadTokenItems(path)
	mm, _ := m.Update(tuiLoadedMsg{path: path, items: items, config: config})
	if got := mm.(tuiModel); got.state != tuiList {
		t.Errorf("开关关着不该换，state = %v", got.state)
	}
	if fake.rotations != 0 {
		t.Errorf("rotations = %d", fake.rotations)
	}
}

func TestTuiToggleAuto(t *testing.T) {
	newRotateFake(t)
	useTempSettings(t)
	path := writeTemp(t, rotateNpmrc())

	m := newTuiModel(path)
	items, _, _ := loadTokenItems(path)
	m.items = items
	m.state = tuiList

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = mm.(tuiModel)
	if !m.autoOn {
		t.Fatalf("应该开启: %s", m.note)
	}
	s, _ := loadAutoSettings()
	if !s.AutoRefresh || s.IfDays != defaultRefreshDays || !sameFile(s.File, path) {
		t.Errorf("设置不对: %+v", s)
	}

	mm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = mm.(tuiModel)
	if m.autoOn {
		t.Fatal("应该关闭")
	}
	s, _ = loadAutoSettings()
	if s.AutoRefresh {
		t.Error("配置里应该也是关")
	}
}

func TestTuiToggleAutoRefusesBadToken(t *testing.T) {
	useTempSettings(t)
	newScopeFake(t, `["read_api"]`)
	path := writeTemp(t, "//git.example.com/:_authToken=glpat-readonly1234\n")

	m := newTuiModel(path)
	items, _, _ := loadTokenItems(path)
	m.items = items
	m.state = tuiList

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = mm.(tuiModel)
	if m.autoOn {
		t.Fatal("scope 不够时不该开启")
	}
	if !strings.Contains(m.note, "self_rotate") {
		t.Errorf("提示里应说清缺什么 scope: %q", m.note)
	}
}

func TestListViewShowsAutoStatus(t *testing.T) {
	off := tuiModel{path: "/tmp/.npmrc", width: 100}
	if got := ansiEscape.ReplaceAllString(off.viewList(), ""); !strings.Contains(got, "自动换新关") {
		t.Errorf("关闭状态没显示:\n%s", got)
	}

	on := tuiModel{path: "/tmp/.npmrc", width: 100, autoOn: true,
		auto: autoSettings{AutoRefresh: true, IfDays: 30, File: "/tmp/.npmrc"}}
	if got := ansiEscape.ReplaceAllString(on.viewList(), ""); !strings.Contains(got, "自动换新开（≤30 天）") {
		t.Errorf("开启状态没显示:\n%s", got)
	}
}
