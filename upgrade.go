package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const repoSlug = "hongmengchen/npm-token"

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []releaseAsset `json:"assets"`
}

// runUpgrade 实现 `npm-token upgrade`
func runUpgrade(args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "只检查是否有新版本，不下载")
	force := fs.Bool("force", false, "版本相同也重新安装")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "参数不对: "+err.Error())
		return 2
	}

	fmt.Printf("当前版本: %s\n", version)
	rel, err := fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "检查更新失败: %v\n", err)
		fmt.Fprintf(os.Stderr, "可以手动安装: curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | sh\n", repoSlug)
		return 2
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	fmt.Printf("最新版本: %s\n", latest)

	if !*force && !isNewer(latest, version) {
		fmt.Println("已是最新版本。")
		return 0
	}
	if *check {
		fmt.Printf("有新版本可用: %s，运行 npm-token upgrade 更新\n", rel.TagName)
		return 0
	}

	exe, err := applyRelease(rel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "更新失败: %v\n", err)
		return 2
	}
	fmt.Printf("已更新到 %s（%s）\n", rel.TagName, exe)
	if runtime.GOOS == "windows" {
		fmt.Println("新开的终端会使用新版本；当前窗口里的命令需要重开或重新安装。")
	}
	return 0
}

// fetchLatestRelease 先走 releases/latest 的跳转（不吃 API 限流），失败再回落到 API
func fetchLatestRelease() (*releaseInfo, error) {
	if rel, err := fetchLatestViaRedirect(); err == nil {
		return rel, nil
	}
	return fetchLatestViaAPI()
}

func fetchLatestViaRedirect() (*releaseInfo, error) {
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 只要 Location，不跟过去
		},
	}
	req, err := http.NewRequest(http.MethodGet, "https://github.com/"+repoSlug+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "npm-token/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return nil, fmt.Errorf("releases/latest 返回 %s", resp.Status)
	}
	loc, err := resp.Location()
	if err != nil {
		return nil, err
	}
	tag := strings.TrimSpace(path.Base(strings.TrimRight(loc.Path, "/")))
	if !strings.HasPrefix(tag, "v") {
		return nil, fmt.Errorf("没能从跳转里解析出版本号: %s", loc.String())
	}

	base := "https://github.com/" + repoSlug + "/releases/download/" + tag + "/"
	return &releaseInfo{
		TagName: tag,
		HTMLURL: "https://github.com/" + repoSlug + "/releases/tag/" + tag,
		Assets: []releaseAsset{
			{Name: assetName(runtime.GOOS, runtime.GOARCH), URL: base + assetName(runtime.GOOS, runtime.GOARCH)},
			{Name: "checksums.txt", URL: base + "checksums.txt"},
		},
	}, nil
}

func fetchLatestViaAPI() (*releaseInfo, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repoSlug+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "npm-token/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub API 限流了，稍后再试")
	default:
		return nil, fmt.Errorf("GitHub 返回 %s", resp.Status)
	}

	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, errors.New("没有解析到版本号")
	}
	return &rel, nil
}

func assetName(goos, goarch string) string {
	name := fmt.Sprintf("npm-token_%s_%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// releaseAssetURL 找出当前平台对应二进制的下载地址
func releaseAssetURL(rel *releaseInfo) (string, error) {
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		if a.Name == asset {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("这个平台没有对应的二进制（%s），请到 %s 手动下载", asset, rel.HTMLURL)
}

// releaseChecksum 从 release 的 checksums.txt 里取当前平台二进制的 SHA-256
func releaseChecksum(rel *releaseInfo) (string, error) {
	var url string
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			url = a.URL
			break
		}
	}
	if url == "" {
		return "", fmt.Errorf("这个 release 没有 checksums.txt，无法校验下载（见 %s）", rel.HTMLURL)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "npm-token/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 checksums.txt 失败: %s", resp.Status)
	}

	asset := assetName(runtime.GOOS, runtime.GOARCH)
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("读 checksums.txt 失败: %w", err)
	}
	return "", fmt.Errorf("checksums.txt 里没有 %s", asset)
}

// currentExecutable 是当前正在运行的程序路径（跟到真实文件，别替换了软链接）
func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("找不到当前可执行文件: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// applyRelease 校验并安装 release 里当前平台的二进制，顺带刷新已装的 agent skill
func applyRelease(rel *releaseInfo) (string, error) {
	url, err := releaseAssetURL(rel)
	if err != nil {
		return "", err
	}
	want, err := releaseChecksum(rel)
	if err != nil {
		return "", err
	}
	exe, err := currentExecutable()
	if err != nil {
		return "", err
	}

	tmp, err := downloadBinary(url, filepath.Dir(exe))
	if err != nil {
		return "", err
	}
	if err := verifyChecksum(tmp, want); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := installBinary(tmp, exe); err != nil {
		return "", err
	}
	refreshSkillAfterUpgrade(exe)
	return exe, nil
}

// downloadBinary 下载到 dir 下的临时文件
func downloadBinary(url, dir string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "npm-token/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: %s", resp.Status)
	}

	tmp, err := os.CreateTemp(dir, ".npm-token-update-*")
	if err != nil {
		return "", fmt.Errorf("无法在 %s 写入（试试 sudo npm-token upgrade）: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	n, err := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if err != nil {
		cleanup()
		return "", fmt.Errorf("下载中断: %w", err)
	}
	if closeErr != nil {
		cleanup()
		return "", closeErr
	}
	if n < 100000 {
		cleanup()
		return "", fmt.Errorf("下载的文件只有 %d 字节，可能不是二进制", n)
	}
	return tmpName, nil
}

func verifyChecksum(file, want string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("下载的文件校验不符（期望 %s，实际 %s）", want, got)
	}
	return nil
}

// installBinary 把临时文件原子换到正式位置
func installBinary(tmpName, exe string) error {
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := os.Chmod(tmpName, 0o755); err != nil {
		cleanup()
		return err
	}

	if err := os.Rename(tmpName, exe); err != nil {
		// Windows 上正在运行的 exe 不能直接覆盖：先挪开旧的
		old := exe + ".old"
		_ = os.Remove(old)
		if renameErr := os.Rename(exe, old); renameErr == nil {
			if err2 := os.Rename(tmpName, exe); err2 == nil {
				_ = os.Remove(old)
				return nil
			}
			_ = os.Rename(old, exe) // 回滚
		}
		cleanup()
		return fmt.Errorf("无法覆盖 %s（权限不足？可试 sudo npm-token upgrade，或用安装脚本重装）: %w", exe, err)
	}
	return nil
}

// isNewer 比较点分版本号：isNewer("0.4.0", "0.3.9") == true
func isNewer(a, b string) bool {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var na, nb int
		if i < len(pa) {
			na, _ = strconv.Atoi(strings.TrimSpace(pa[i]))
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(strings.TrimSpace(pb[i]))
		}
		if na != nb {
			return na > nb
		}
	}
	return false
}
