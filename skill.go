package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// agent skill 随二进制走：装好/升级好程序，agent 就能读到和当前版本配套的用法说明
//
//go:embed all:.claude/skills/npm-token/SKILL.md
var embeddedSkill string

// skillRoot 是 agent 扫描的全局目录，默认 ~/.claude/skills
func skillRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

func skillFile(root string) string {
	return filepath.Join(root, "npm-token", "SKILL.md")
}

// installSkill 写入或更新 skill。
// changed 表示这次真的写了文件；fresh 表示之前没有、是首次安装
func installSkill(root string) (path string, changed, fresh bool, err error) {
	if root == "" {
		if root, err = skillRoot(); err != nil {
			return "", false, false, err
		}
	}
	path = skillFile(root)

	old, readErr := os.ReadFile(path)
	if readErr == nil && string(old) == embeddedSkill {
		return path, false, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, false, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(embeddedSkill), 0o644); err != nil {
		return path, false, false, fmt.Errorf("写入失败: %w", err)
	}
	return path, true, readErr != nil, nil
}

func skillInstalled(root string) bool {
	if root == "" {
		r, err := skillRoot()
		if err != nil {
			return false
		}
		root = r
	}
	_, err := os.Stat(skillFile(root))
	return err == nil
}

// runSkill 实现 `npm-token skill`：默认写入/更新，--print 只输出内容
func runSkill(args []string) int {
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `用法: npm-token skill [选项]

把 agent skill 写进全局目录（默认 ~/.claude/skills/npm-token/SKILL.md），
已存在旧版本就覆盖成当前程序自带的版本。

选项:
  -dir <dir>   skills 根目录（默认 ~/.claude/skills）
  -print       只把 SKILL.md 打到 stdout，不写文件
  -quiet       成功时不打印
`)
	}
	dir := fs.String("dir", "", "skills 根目录（默认 ~/.claude/skills）")
	printOnly := fs.Bool("print", false, "只输出内容，不写文件")
	quiet := fs.Bool("quiet", false, "成功时不打印")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *printOnly {
		fmt.Print(embeddedSkill)
		return 0
	}

	path, changed, fresh, err := installSkill(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		return 1
	}
	if *quiet {
		return 0
	}
	switch {
	case fresh:
		fmt.Printf("skill 已安装: %s\n", displayPath(path))
	case changed:
		fmt.Printf("skill 已更新: %s\n", displayPath(path))
	default:
		fmt.Printf("skill 已是最新: %s\n", displayPath(path))
	}
	fmt.Println("agent 在新会话里会读到它（内容跟当前程序版本一致，升级 npm-token 时会一起刷新）")
	return 0
}
