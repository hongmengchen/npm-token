package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedSkill(t *testing.T) {
	if !strings.HasPrefix(embeddedSkill, "---\n") {
		t.Fatal("SKILL.md 缺少 frontmatter")
	}
	if !strings.Contains(embeddedSkill, "name: npm-token") {
		t.Fatal("SKILL.md 的 name 不是 npm-token")
	}
	if !strings.Contains(embeddedSkill, "\ndescription: ") {
		t.Fatal("SKILL.md 缺少 description")
	}
	if !strings.Contains(embeddedSkill, "不要回显 token") {
		t.Fatal("SKILL.md 缺少安全约束")
	}
}

func TestInstallSkill(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	path := filepath.Join(root, "npm-token", "SKILL.md")

	if skillInstalled(root) {
		t.Fatal("还没装就报告已安装")
	}

	got, changed, fresh, err := installSkill(root)
	if err != nil {
		t.Fatalf("首次安装失败: %v", err)
	}
	if got != path {
		t.Fatalf("路径不对: %s", got)
	}
	if !changed || !fresh {
		t.Fatalf("首次安装应 changed+fresh，得到 changed=%v fresh=%v", changed, fresh)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != embeddedSkill {
		t.Fatalf("写入内容不对: %v", err)
	}
	if !skillInstalled(root) {
		t.Fatal("装完却检测不到")
	}

	if _, changed, fresh, err := installSkill(root); err != nil || changed || fresh {
		t.Fatalf("重复安装应无变化，得到 changed=%v fresh=%v err=%v", changed, fresh, err)
	}

	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, changed, fresh, err = installSkill(root)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if !changed || fresh {
		t.Fatalf("更新应 changed 且非 fresh，得到 changed=%v fresh=%v", changed, fresh)
	}
	if data, _ := os.ReadFile(path); string(data) != embeddedSkill {
		t.Fatal("更新后内容没变")
	}
}

func TestSkillInstalledDefaultMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if skillInstalled("") {
		t.Fatal("空的 HOME 里不应报告已安装")
	}
}
