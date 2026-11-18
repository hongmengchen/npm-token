package main

import (
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.4.0", "0.3.9", true},
		{"v0.4.0", "0.4.0", false},
		{"0.3.9", "0.4.0", false},
		{"1.0", "0.9.9", true},
		{"0.10.0", "0.9.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.a, c.b); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSelfUpdateDone(t *testing.T) {
	m := newTuiModel("x")
	m.state = tuiWorking
	m.latest = "9.9.9"

	updated, _ := m.Update(selfUpdateDoneMsg{version: "9.9.9", changed: true})
	got := updated.(tuiModel)
	if got.state != tuiList {
		t.Errorf("升级完成后应回到列表，state = %v", got.state)
	}
	if got.updatedTo != "9.9.9" || got.canUpdate() {
		t.Errorf("升级完成后状态不对: updatedTo=%q canUpdate=%v", got.updatedTo, got.canUpdate())
	}

	m.state = tuiWorking
	updated, _ = m.Update(selfUpdateDoneMsg{err: errUpdateBoom{}})
	got = updated.(tuiModel)
	if got.state != tuiList || !strings.Contains(got.note, "更新失败") {
		t.Errorf("失败后应回列表并提示: state=%v note=%q", got.state, got.note)
	}
}

type errUpdateBoom struct{}

func (errUpdateBoom) Error() string { return "boom" }

func TestUpdateStatus(t *testing.T) {
	m := newTuiModel("x")
	if !strings.Contains(m.updateStatus(), "检查更新中") {
		t.Fatalf("初始状态 = %q", m.updateStatus())
	}

	m.latest = "9.9.9"
	if !m.canUpdate() {
		t.Fatal("最新版更新时应当可以升级")
	}
	if got := m.updateStatus(); !strings.Contains(got, "按 U 升级") {
		t.Errorf("可升级状态 = %q", got)
	}

	m.updatedTo = "9.9.9"
	if m.canUpdate() {
		t.Error("升级完不应再提示升级")
	}
	if got := m.updateStatus(); !strings.Contains(got, "重开生效") {
		t.Errorf("升级后状态 = %q", got)
	}
}
