package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// readClipboard 读取系统剪贴板文本（macOS: pbpaste，Windows: Get-Clipboard，Linux: wl-paste/xclip/xsel）
func readClipboard() (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbpaste")
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	default:
		switch {
		case hasBin("wl-paste"):
			cmd = exec.Command("wl-paste", "--no-newline")
		case hasBin("xclip"):
			cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
		case hasBin("xsel"):
			cmd = exec.Command("xsel", "--clipboard", "--output")
		default:
			return "", errors.New("找不到剪贴板工具（Linux 需要 wl-clipboard / xclip / xsel）")
		}
	}

	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("读取剪贴板失败: " + err.Error())
	}
	// 粘贴大段文本时只取第一行非空内容，避免把说明文字一起塞进去
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", nil
}

func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
