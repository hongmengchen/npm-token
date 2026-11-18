package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hongmengchen/tui/theme"
)

var styles = theme.Default

type tuiState int

const (
	tuiLoading tuiState = iota
	tuiList
	tuiInput
	tuiConfirm
	tuiWorking
	tuiResult
)

type tokenItem struct {
	Entries []authEntry
	Rep     tokenReport
}

type tuiModel struct {
	path  string
	items []tokenItem
	state tuiState

	cursor        int
	width, height int

	input textinput.Model
	spin  spinner.Model

	note   string
	failed string

	workText string

	// 自身版本检查与升级
	latest    string
	checkErr  string
	updatedTo string

	selected tokenItem
	skipped  int

	// 结果
	backup   string
	verify   tokenReport
	resultOK bool
	wrote    bool
	restored bool
}

type tuiLoadedMsg struct {
	path  string
	items []tokenItem
	err   error
}

type tuiApplyDoneMsg struct {
	backup   string
	verify   tokenReport
	wrote    bool
	restored bool
	skipped  int
	err      error
}

type versionCheckMsg struct {
	latest string
	err    error
}

type selfUpdateDoneMsg struct {
	version string
	changed bool
	err     error
}

func newTuiModel(path string) tuiModel {
	ti := textinput.New()
	ti.Placeholder = "粘贴新的 token"
	ti.CharLimit = 512
	ti.Width = 60

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.Accent

	return tuiModel{path: path, state: tuiLoading, input: ti, spin: sp}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, tuiLoadCmd(m.path), checkVersionCmd())
}

func checkVersionCmd() tea.Cmd {
	return func() tea.Msg {
		rel, err := fetchLatestRelease()
		if err != nil {
			return versionCheckMsg{err: err}
		}
		return versionCheckMsg{latest: rel.TagName}
	}
}

func (m tuiModel) canUpdate() bool {
	return m.updatedTo == "" && m.latest != "" && isNewer(m.latest, version)
}

// selfUpdateCmd 下载并替换当前可执行文件；替换后要重开程序才会用上新版本
func selfUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		rel, err := fetchLatestRelease()
		if err != nil {
			return selfUpdateDoneMsg{err: err}
		}
		latest := strings.TrimPrefix(rel.TagName, "v")
		if !isNewer(latest, version) {
			return selfUpdateDoneMsg{version: latest}
		}
		if _, err := applyRelease(rel); err != nil {
			return selfUpdateDoneMsg{err: err}
		}
		return selfUpdateDoneMsg{version: latest, changed: true}
	}
}

func tuiLoadCmd(path string) tea.Cmd {
	return func() tea.Msg {
		items, err := loadTokenItems(path)
		return tuiLoadedMsg{path: path, items: items, err: err}
	}
}

func loadTokenItems(path string) ([]tokenItem, error) {
	f, err := parseNpmrc(path)
	if err != nil {
		return nil, err
	}
	if len(f.Auth) == 0 {
		return nil, errNoAuthEntries
	}
	var items []tokenItem
	for _, g := range groupByToken(f.Auth) {
		items = append(items, tokenItem{Entries: g.Entries, Rep: checkToken(g.Entries)})
	}
	return items, nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case spinner.TickMsg:
		if m.state != tuiLoading && m.state != tuiWorking {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tuiLoadedMsg:
		m.path = msg.path
		m.items = msg.items
		m.state = tuiList
		m.clampCursor()
		if msg.err != nil {
			m.note = msg.err.Error()
		} else {
			m.note = ""
		}
		return m, nil

	case versionCheckMsg:
		if msg.err != nil {
			m.checkErr = msg.err.Error()
		} else {
			m.latest = strings.TrimPrefix(msg.latest, "v")
		}
		return m, nil

	case selfUpdateDoneMsg:
		m.state = tuiList
		if msg.err != nil {
			m.note = "更新失败: " + msg.err.Error()
			return m, nil
		}
		m.latest = msg.version
		if msg.changed {
			m.updatedTo = msg.version
			m.note = ""
		} else {
			m.note = "已经是最新版本"
		}
		return m, nil

	case tuiApplyDoneMsg:
		m.state = tuiResult
		m.backup = msg.backup
		m.verify = msg.verify
		m.wrote = msg.wrote
		m.restored = msg.restored
		m.skipped = msg.skipped
		m.resultOK = msg.err == nil && msg.wrote && !msg.restored &&
			msg.verify.Valid != nil && *msg.verify.Valid
		if msg.err != nil {
			m.failed = msg.err.Error()
		} else {
			m.failed = ""
		}
		return m, nil
	}

	return m, nil
}

func (m tuiModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case tuiLoading, tuiWorking:
		return m, nil

	case tuiList:
		switch k.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.items) > 0 {
				m.selected = m.items[m.cursor]
				m.input.SetValue("")
				m.input.Focus()
				m.note = ""
				m.state = tuiInput
				return m, textinput.Blink
			}
		case "r":
			m.state = tuiLoading
			m.note = ""
			return m, tea.Batch(m.spin.Tick, tuiLoadCmd(m.path))
		case "U":
			if m.canUpdate() {
				m.note = ""
				m.state = tuiWorking
				m.workText = "正在更新到 " + m.latest + "…"
				return m, tea.Batch(m.spin.Tick, selfUpdateCmd())
			}
		case "q", "esc":
			return m, tea.Quit
		}

	case tuiInput:
		switch k.String() {
		case "esc":
			m.input.Blur()
			m.state = tuiList
			return m, nil
		case "ctrl+y":
			if s, err := readClipboard(); err != nil {
				m.note = err.Error()
			} else if s == "" {
				m.note = "剪贴板是空的"
			} else {
				m.input.SetValue(s)
				m.input.CursorEnd()
				m.note = fmt.Sprintf("已从剪贴板读取 %d 个字符", len(s))
			}
			return m, nil
		case "enter":
			tok := strings.TrimSpace(m.input.Value())
			if err := validateToken(tok); err != nil {
				m.note = err.Error()
				return m, nil
			}
			m.input.Blur()
			m.state = tuiConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return m, cmd

	case tuiConfirm:
		switch k.String() {
		case "esc":
			m.state = tuiInput
			m.input.Focus()
			return m, textinput.Blink
		case "enter":
			tok := strings.TrimSpace(m.input.Value())
			m.state = tuiWorking
			m.workText = ""
			return m, tea.Batch(m.spin.Tick, tuiApplyCmd(m.path, m.selected, tok))
		}

	case tuiResult:
		m.state = tuiLoading
		m.note = ""
		if m.restored {
			// 文件已经回到旧内容，直接把列表重读一遍
			return m, tea.Batch(m.spin.Tick, tuiLoadCmd(m.path))
		}
		return m, tea.Batch(m.spin.Tick, tuiLoadCmd(m.path))
	}
	return m, nil
}

func tuiApplyCmd(path string, item tokenItem, token string) tea.Cmd {
	return func() tea.Msg {
		return applyToken(path, item, token)
	}
}

// applyToken 与 CLI 的 set-token 同一套动作：备份 → 原子写 → 复验 → 被拒自动还原
func applyToken(path string, item tokenItem, token string) tuiApplyDoneMsg {
	var targets []authEntry
	skipped := 0
	for _, e := range item.Entries {
		if e.EnvName != "" {
			skipped++
			continue
		}
		targets = append(targets, e)
	}
	if len(targets) == 0 {
		return tuiApplyDoneMsg{skipped: skipped, err: errors.New("这组条目都用环境变量取值，改环境变量本身更合适")}
	}

	backup, err := rewriteAuth(path, targets, token)
	if err != nil {
		return tuiApplyDoneMsg{backup: backup, skipped: skipped, err: err}
	}

	f, err := parseNpmrc(path)
	if err != nil {
		return tuiApplyDoneMsg{backup: backup, wrote: true, skipped: skipped, err: fmt.Errorf("写完了但重读失败: %w", err)}
	}
	var fresh []authEntry
	for _, e := range f.Auth {
		if e.Value == token {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return tuiApplyDoneMsg{backup: backup, wrote: true, skipped: skipped, err: errors.New("写完了但在文件里找不到新值")}
	}

	rep := checkToken(fresh)
	if rep.Valid != nil && !*rep.Valid {
		if err := copyFile(backup, path); err == nil {
			return tuiApplyDoneMsg{backup: backup, verify: rep, restored: true, skipped: skipped}
		}
		return tuiApplyDoneMsg{backup: backup, verify: rep, wrote: true, skipped: skipped,
			err: errors.New("新 token 被拒绝，自动还原也失败了，请手动执行 cp " + backup + " " + path)}
	}
	return tuiApplyDoneMsg{backup: backup, verify: rep, wrote: true, skipped: skipped}
}

func (m *tuiModel) clampCursor() {
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// ---------- 渲染 ----------

func (m tuiModel) View() string {
	var body string
	switch m.state {
	case tuiLoading:
		body = m.viewLoading()
	case tuiList:
		body = m.viewList()
	case tuiInput:
		body = m.viewInput()
	case tuiConfirm:
		body = m.viewConfirm()
	case tuiWorking:
		body = m.viewWorking()
	case tuiResult:
		body = m.viewResult()
	}
	return "\n" + body + "\n"
}

func (m tuiModel) viewLoading() string {
	return styles.Title.Render("npm-token") + styles.Dim.Render("  v"+version+"  检查中") + "\n\n" +
		"  " + m.spin.View() + "  " + styles.Dim.Render("正在问 registry 这些 token 的状态…")
}

func (m tuiModel) viewList() string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("npm-token") + styles.Dim.Render("  v"+version+"  .npmrc 里的 registry token"))
	b.WriteString("\n")

	authCount := 0
	for _, it := range m.items {
		authCount += len(it.Entries)
	}
	b.WriteString(styles.Dim.Render(fmt.Sprintf("  %s · %d 条认证 · %d 个 token", displayPath(m.path), authCount, len(m.items))))
	b.WriteString(m.updateStatus())
	b.WriteString("\n\n")

	for i, it := range m.items {
		b.WriteString(m.renderCard(it, i == m.cursor))
		b.WriteString("\n")
	}
	if len(m.items) == 0 {
		b.WriteString("  " + styles.Warn.Render("没有可用的 token 条目") + "\n")
	}

	if m.note != "" {
		b.WriteString("\n  " + styles.Accent.Render(m.note) + "\n")
	}

	b.WriteString("\n")
	hints := []string{"↑↓", "选择", "enter", "换 token", "r", "重新检查"}
	if m.canUpdate() {
		hints = append(hints, "U", "升级")
	}
	hints = append(hints, "q", "退出")
	b.WriteString(tuiHints(hints...))
	return b.String()
}

// updateStatus 显示当前版本之外的一小段状态：检查中 / 已是最新 / 可升级 / 已升级
func (m tuiModel) updateStatus() string {
	switch {
	case m.updatedTo != "":
		return styles.Good.Render("  ·  已更新到 " + m.updatedTo + "，重开生效")
	case m.canUpdate():
		return styles.Warn.Render("  ·  最新 " + m.latest + "，按 U 升级")
	case m.latest != "":
		return styles.Dim.Render("  ·  已是最新")
	case m.checkErr != "":
		return styles.Dim.Render("  ·  检查更新失败")
	default:
		return styles.Dim.Render("  ·  检查更新中…")
	}
}

func (m tuiModel) renderCard(it tokenItem, selected bool) string {
	w := m.contentWidth()
	label, style := tuiStatus(it.Rep)

	head := styles.Text.Render(it.Rep.Masked) + "   " + styles.Dim.Render(kindLabel(it.Rep.Kind)) + "   " + style.Render(label)

	meta := append([]string{}, it.Rep.Hosts...)
	meta = append(meta, it.Rep.Details...)
	metaLine := styles.Dim.Render(theme.Truncate(strings.Join(meta, " · "), w))

	last := []string{}
	if line := expiryLine(it.Rep); line != "" {
		last = append(last, line)
	}
	last = append(last, entrySummary(it.Rep))

	body := head + "\n" + metaLine + "\n" + styles.Dim.Render(theme.Truncate(strings.Join(last, " · "), w))
	if selected {
		return styles.CardOn.Render(body)
	}
	return styles.CardOff.Render(body)
}

func (m tuiModel) viewInput() string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("npm-token") + styles.Dim.Render("  换新 token"))
	b.WriteString("\n\n")
	b.WriteString(m.selectedSummary())
	b.WriteString("\n\n")
	b.WriteString("  " + styles.Dim.Render("新 token") + "\n")
	b.WriteString("  " + m.input.View() + "\n\n")
	if m.note != "" {
		b.WriteString("  " + styles.Accent.Render(m.note) + "\n\n")
	} else {
		b.WriteString("  " + styles.Dim.Render("粘贴后回车；也可以按 ctrl+y 从剪贴板读取") + "\n\n")
	}
	b.WriteString(tuiHints("enter", "下一步", "ctrl+y", "读剪贴板", "esc", "返回"))
	return b.String()
}

func (m tuiModel) viewConfirm() string {
	it := m.selected
	tok := strings.TrimSpace(m.input.Value())
	writable := 0
	for _, e := range it.Entries {
		if e.EnvName == "" {
			writable++
		}
	}

	var b strings.Builder
	b.WriteString(styles.Title.Render("npm-token") + styles.Dim.Render("  确认替换"))
	b.WriteString("\n\n")
	b.WriteString("  " + styles.Label.Render("文件") + styles.Text.Render(displayPath(m.path)) + "\n")
	b.WriteString("  " + styles.Label.Render("主机") + styles.Text.Render(strings.Join(it.Rep.Hosts, ", ")) + "\n")
	b.WriteString("  " + styles.Label.Render("旧") + styles.Dim.Render(it.Rep.Masked) + "\n")
	b.WriteString("  " + styles.Label.Render("新") + styles.Accent.Render(maskToken(tok)) + "\n")
	b.WriteString("  " + styles.Label.Render("范围") + styles.Text.Render(fmt.Sprintf("%d 条认证配置", writable)) + "\n")
	if skipped := len(it.Entries) - writable; skipped > 0 {
		b.WriteString("  " + styles.Label.Render("") + styles.Dim.Render(fmt.Sprintf("另有 %d 条用环境变量，会跳过", skipped)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  " + styles.Dim.Render("会先备份成 "+displayPath(m.path)+".bak.<时间戳>，写入后联网复验") + "\n")
	b.WriteString("\n")
	b.WriteString(tuiHints("enter", "写入并验证", "esc", "返回"))
	return b.String()
}

func (m tuiModel) viewWorking() string {
	text := m.workText
	if text == "" {
		text = "正在写入并验证新 token…"
	}
	return styles.Title.Render("npm-token") + styles.Dim.Render("  v"+version+"  处理中") + "\n\n" +
		"  " + m.spin.View() + "  " + styles.Dim.Render(text)
}

func (m tuiModel) viewResult() string {
	var head string
	switch {
	case m.restored:
		head = styles.Bad.Render("✗ 新 token 被拒绝，已自动还原")
	case m.resultOK:
		head = styles.Good.Render("✓ 已更新")
	case m.wrote && m.failed == "":
		head = styles.Warn.Render("! 已写入，但没能确认状态")
	case m.wrote:
		head = styles.Warn.Render("! 已写入，但验证未通过")
	default:
		head = styles.Bad.Render("✗ 替换失败")
	}

	writable := 0
	for _, e := range m.selected.Entries {
		if e.EnvName == "" {
			writable++
		}
	}

	var b strings.Builder
	b.WriteString(styles.Title.Render("npm-token") + "  " + head)
	b.WriteString("\n\n")
	b.WriteString("  " + styles.Label.Render("文件") + styles.Text.Render(displayPath(m.path)) + "\n")
	b.WriteString("  " + styles.Label.Render("条目") + styles.Text.Render(fmt.Sprintf("%d 条", writable)) + "\n")
	if m.skipped > 0 {
		b.WriteString("  " + styles.Label.Render("") + styles.Dim.Render(fmt.Sprintf("跳过 %d 条环境变量条目", m.skipped)) + "\n")
	}
	if m.backup != "" {
		b.WriteString("  " + styles.Label.Render("备份") + styles.Dim.Render(displayPath(m.backup)) + "\n")
	}
	if m.failed != "" {
		b.WriteString("  " + styles.Label.Render("问题") + styles.Bad.Render(m.failed) + "\n")
	} else if m.restored {
		reason := firstNonEmpty(m.verify.Error, m.verify.ExpiresIn)
		if reason != "" {
			b.WriteString("  " + styles.Label.Render("原因") + styles.Bad.Render(reason) + "\n")
		}
		b.WriteString("  " + styles.Label.Render("") + styles.Dim.Render("文件已恢复成原来的 token，可以换个新值再试") + "\n")
	} else if m.verify.Masked != "" {
		label := "验证"
		style := styles.Good
		if m.verify.Valid == nil || !*m.verify.Valid {
			label, style = "问题", styles.Warn
		}
		b.WriteString("  " + styles.Label.Render(label) + style.Render(firstNonEmpty(m.verify.ExpiresIn, m.verify.Error, "状态未知")) + "\n")
		if m.backup != "" {
			b.WriteString("\n  " + styles.Dim.Render("要还原: cp "+displayPath(m.backup)+" "+displayPath(m.path)) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(tuiHints("任意键", "返回列表"))
	return b.String()
}

func (m tuiModel) selectedSummary() string {
	label, style := tuiStatus(m.selected.Rep)
	var b strings.Builder
	b.WriteString("  " + styles.Label.Render("文件") + styles.Text.Render(displayPath(m.path)) + "\n")
	b.WriteString("  " + styles.Label.Render("token") + styles.Text.Render(m.selected.Rep.Masked) +
		"   " + styles.Dim.Render(kindLabel(m.selected.Rep.Kind)) + "   " + style.Render(label) + "\n")
	b.WriteString("  " + styles.Label.Render("主机") + styles.Text.Render(strings.Join(m.selected.Rep.Hosts, ", ")) + "\n")
	b.WriteString("  " + styles.Label.Render("条目") + styles.Dim.Render(entrySummary(m.selected.Rep)))
	return b.String()
}

func (m tuiModel) contentWidth() int {
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	if w > 110 {
		w = 110
	}
	return w
}

func tuiHints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, styles.Key.Render(pairs[i])+styles.Dim.Render(" "+pairs[i+1]))
	}
	return "  " + strings.Join(parts, styles.Dim.Render("  ·  "))
}

func tuiStatus(r tokenReport) (string, lipgloss.Style) {
	switch {
	case r.Revoked:
		return "✗ 已撤销", styles.Bad
	case r.Expired:
		return "✗ 已过期", styles.Bad
	case r.Valid != nil && !*r.Valid:
		return "✗ 无效", styles.Bad
	case r.Valid != nil && *r.Valid && strings.Contains(r.ExpiresIn, "该换了"):
		return "! 快到期了", styles.Warn
	case r.Valid != nil && *r.Valid:
		return "✓ 有效", styles.Good
	default:
		return "? 判断不了", styles.Warn
	}
}

func entrySummary(r tokenReport) string {
	ok, denied, unknown := 0, 0, 0
	for _, e := range r.Entries {
		switch e.Access {
		case "ok":
			ok++
		case "denied":
			denied++
		default:
			unknown++
		}
	}
	switch {
	case denied > 0:
		return fmt.Sprintf("%d/%d 个条目不可访问", denied, len(r.Entries))
	case unknown == len(r.Entries):
		return "条目状态未验证"
	case unknown > 0:
		return fmt.Sprintf("%d 个条目可用，%d 个未验证", ok, unknown)
	default:
		return fmt.Sprintf("%d 个条目全部可访问", ok)
	}
}
