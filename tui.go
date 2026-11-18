package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	lAccent = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
	lDim    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#7C8794"}
	lText   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E5E7EB"}
	lGood   = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	lBad    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	lWarn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	lLine   = lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#333A45"}
)

var (
	tuiTitle   = lipgloss.NewStyle().Bold(true).Foreground(lAccent)
	tuiDim     = lipgloss.NewStyle().Foreground(lDim)
	tuiText    = lipgloss.NewStyle().Foreground(lText)
	tuiGood    = lipgloss.NewStyle().Foreground(lGood).Bold(true)
	tuiBad     = lipgloss.NewStyle().Foreground(lBad)
	tuiWarn    = lipgloss.NewStyle().Foreground(lWarn)
	tuiAccent  = lipgloss.NewStyle().Foreground(lAccent)
	tuiKey     = lipgloss.NewStyle().Foreground(lAccent).Bold(true)
	tuiLabel   = lipgloss.NewStyle().Foreground(lDim).Width(9)
	tuiCardOn  = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lAccent).PaddingLeft(1)
	tuiCardOff = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lLine).PaddingLeft(1)
)

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

func newTuiModel(path string) tuiModel {
	ti := textinput.New()
	ti.Placeholder = "粘贴新的 token"
	ti.CharLimit = 512
	ti.Width = 60

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = tuiAccent

	return tuiModel{path: path, state: tuiLoading, input: ti, spin: sp}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, tuiLoadCmd(m.path))
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
	return tuiTitle.Render("npm-token") + tuiDim.Render("  检查中") + "\n\n" +
		"  " + m.spin.View() + "  " + tuiDim.Render("正在问 registry 这些 token 的状态…")
}

func (m tuiModel) viewList() string {
	var b strings.Builder
	b.WriteString(tuiTitle.Render("npm-token") + tuiDim.Render("  .npmrc 里的 registry token"))
	b.WriteString("\n")

	authCount := 0
	for _, it := range m.items {
		authCount += len(it.Entries)
	}
	b.WriteString(tuiDim.Render(fmt.Sprintf("  %s · %d 条认证 · %d 个 token", displayPath(m.path), authCount, len(m.items))))
	b.WriteString("\n\n")

	for i, it := range m.items {
		b.WriteString(m.renderCard(it, i == m.cursor))
		b.WriteString("\n")
	}
	if len(m.items) == 0 {
		b.WriteString("  " + tuiWarn.Render("没有可用的 token 条目") + "\n")
	}

	if m.note != "" {
		b.WriteString("\n  " + tuiAccent.Render(m.note) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(tuiHints("↑↓", "选择", "enter", "换 token", "r", "重新检查", "q", "退出"))
	return b.String()
}

func (m tuiModel) renderCard(it tokenItem, selected bool) string {
	w := m.contentWidth()
	label, style := tuiStatus(it.Rep)

	head := tuiText.Render(it.Rep.Masked) + "   " + tuiDim.Render(kindLabel(it.Rep.Kind)) + "   " + style.Render(label)

	meta := append([]string{}, it.Rep.Hosts...)
	meta = append(meta, it.Rep.Details...)
	metaLine := tuiDim.Render(truncate(strings.Join(meta, " · "), w))

	last := []string{}
	if line := expiryLine(it.Rep); line != "" {
		last = append(last, line)
	}
	last = append(last, entrySummary(it.Rep))

	body := head + "\n" + metaLine + "\n" + tuiDim.Render(truncate(strings.Join(last, " · "), w))
	if selected {
		return tuiCardOn.Render(body)
	}
	return tuiCardOff.Render(body)
}

func (m tuiModel) viewInput() string {
	var b strings.Builder
	b.WriteString(tuiTitle.Render("npm-token") + tuiDim.Render("  换新 token"))
	b.WriteString("\n\n")
	b.WriteString(m.selectedSummary())
	b.WriteString("\n\n")
	b.WriteString("  " + tuiDim.Render("新 token") + "\n")
	b.WriteString("  " + m.input.View() + "\n\n")
	if m.note != "" {
		b.WriteString("  " + tuiAccent.Render(m.note) + "\n\n")
	} else {
		b.WriteString("  " + tuiDim.Render("粘贴后回车；也可以按 ctrl+y 从剪贴板读取") + "\n\n")
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
	b.WriteString(tuiTitle.Render("npm-token") + tuiDim.Render("  确认替换"))
	b.WriteString("\n\n")
	b.WriteString("  " + tuiLabel.Render("文件") + tuiText.Render(displayPath(m.path)) + "\n")
	b.WriteString("  " + tuiLabel.Render("主机") + tuiText.Render(strings.Join(it.Rep.Hosts, ", ")) + "\n")
	b.WriteString("  " + tuiLabel.Render("旧") + tuiDim.Render(it.Rep.Masked) + "\n")
	b.WriteString("  " + tuiLabel.Render("新") + tuiAccent.Render(maskToken(tok)) + "\n")
	b.WriteString("  " + tuiLabel.Render("范围") + tuiText.Render(fmt.Sprintf("%d 条认证配置", writable)) + "\n")
	if skipped := len(it.Entries) - writable; skipped > 0 {
		b.WriteString("  " + tuiLabel.Render("") + tuiDim.Render(fmt.Sprintf("另有 %d 条用环境变量，会跳过", skipped)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  " + tuiDim.Render("会先备份成 "+displayPath(m.path)+".bak.<时间戳>，写入后联网复验") + "\n")
	b.WriteString("\n")
	b.WriteString(tuiHints("enter", "写入并验证", "esc", "返回"))
	return b.String()
}

func (m tuiModel) viewWorking() string {
	return tuiTitle.Render("npm-token") + tuiDim.Render("  处理中") + "\n\n" +
		"  " + m.spin.View() + "  " + tuiDim.Render("正在写入并验证新 token…")
}

func (m tuiModel) viewResult() string {
	var head string
	switch {
	case m.restored:
		head = tuiBad.Render("✗ 新 token 被拒绝，已自动还原")
	case m.resultOK:
		head = tuiGood.Render("✓ 已更新")
	case m.wrote && m.failed == "":
		head = tuiWarn.Render("! 已写入，但没能确认状态")
	case m.wrote:
		head = tuiWarn.Render("! 已写入，但验证未通过")
	default:
		head = tuiBad.Render("✗ 替换失败")
	}

	writable := 0
	for _, e := range m.selected.Entries {
		if e.EnvName == "" {
			writable++
		}
	}

	var b strings.Builder
	b.WriteString(tuiTitle.Render("npm-token") + "  " + head)
	b.WriteString("\n\n")
	b.WriteString("  " + tuiLabel.Render("文件") + tuiText.Render(displayPath(m.path)) + "\n")
	b.WriteString("  " + tuiLabel.Render("条目") + tuiText.Render(fmt.Sprintf("%d 条", writable)) + "\n")
	if m.skipped > 0 {
		b.WriteString("  " + tuiLabel.Render("") + tuiDim.Render(fmt.Sprintf("跳过 %d 条环境变量条目", m.skipped)) + "\n")
	}
	if m.backup != "" {
		b.WriteString("  " + tuiLabel.Render("备份") + tuiDim.Render(displayPath(m.backup)) + "\n")
	}
	if m.failed != "" {
		b.WriteString("  " + tuiLabel.Render("问题") + tuiBad.Render(m.failed) + "\n")
	} else if m.restored {
		reason := firstNonEmpty(m.verify.Error, m.verify.ExpiresIn)
		if reason != "" {
			b.WriteString("  " + tuiLabel.Render("原因") + tuiBad.Render(reason) + "\n")
		}
		b.WriteString("  " + tuiLabel.Render("") + tuiDim.Render("文件已恢复成原来的 token，可以换个新值再试") + "\n")
	} else if m.verify.Masked != "" {
		label := "验证"
		style := tuiGood
		if m.verify.Valid == nil || !*m.verify.Valid {
			label, style = "问题", tuiWarn
		}
		b.WriteString("  " + tuiLabel.Render(label) + style.Render(firstNonEmpty(m.verify.ExpiresIn, m.verify.Error, "状态未知")) + "\n")
		if m.backup != "" {
			b.WriteString("\n  " + tuiDim.Render("要还原: cp "+displayPath(m.backup)+" "+displayPath(m.path)) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(tuiHints("任意键", "返回列表"))
	return b.String()
}

func (m tuiModel) selectedSummary() string {
	label, style := tuiStatus(m.selected.Rep)
	var b strings.Builder
	b.WriteString("  " + tuiLabel.Render("文件") + tuiText.Render(displayPath(m.path)) + "\n")
	b.WriteString("  " + tuiLabel.Render("token") + tuiText.Render(m.selected.Rep.Masked) +
		"   " + tuiDim.Render(kindLabel(m.selected.Rep.Kind)) + "   " + style.Render(label) + "\n")
	b.WriteString("  " + tuiLabel.Render("主机") + tuiText.Render(strings.Join(m.selected.Rep.Hosts, ", ")) + "\n")
	b.WriteString("  " + tuiLabel.Render("条目") + tuiDim.Render(entrySummary(m.selected.Rep)))
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
		parts = append(parts, tuiKey.Render(pairs[i])+tuiDim.Render(" "+pairs[i+1]))
	}
	return "  " + strings.Join(parts, tuiDim.Render("  ·  "))
}

func tuiStatus(r tokenReport) (string, lipgloss.Style) {
	switch {
	case r.Revoked:
		return "✗ 已撤销", tuiBad
	case r.Expired:
		return "✗ 已过期", tuiBad
	case r.Valid != nil && !*r.Valid:
		return "✗ 无效", tuiBad
	case r.Valid != nil && *r.Valid && strings.Contains(r.ExpiresIn, "该换了"):
		return "! 快到期了", tuiWarn
	case r.Valid != nil && *r.Valid:
		return "✓ 有效", tuiGood
	default:
		return "? 判断不了", tuiWarn
	}
}

func truncate(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
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
