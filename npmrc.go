package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// authEntry 是 .npmrc 里一行 `//host/path/:_authToken=值`
type authEntry struct {
	File      string
	LineNo    int    // 0 起
	Key       string // //host/path/:_authToken
	Prefix    string // //host/path/
	Host      string
	ProjectID string // GitLab 风格 URL 里 /projects/<id>/ 的 id
	Param     string // _authToken / _auth
	RawValue  string // 文件里的原样（可能是 ${VAR}）
	Value     string // 展开后的真实值
	EnvName   string // 非空说明值来自环境变量
}

// Registry 是该条目对应的 registry 地址（补上 https:）
func (e authEntry) Registry() string { return "https://" + strings.TrimPrefix(e.Prefix, "//") }

// registryLine 是 `registry=` / `@scope:registry=` 这类映射
type registryLine struct {
	Scope  string // 空表示默认 registry
	URL    string
	LineNo int
}

type npmrcFile struct {
	Path       string
	Lines      []string
	Auth       []authEntry
	Registries []registryLine
}

// defaultNpmrcPath 是用户级 .npmrc
func defaultNpmrcPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".npmrc"), nil
}

func parseNpmrc(path string) (*npmrcFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &npmrcFile{Path: path, Lines: strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")}

	for i, raw := range f.Lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)

		switch {
		case key == "registry":
			f.Registries = append(f.Registries, registryLine{URL: strings.TrimSpace(strings.Trim(val, `"'`)), LineNo: i})
		case strings.HasPrefix(key, "@") && strings.HasSuffix(key, ":registry"):
			scope := strings.TrimSuffix(strings.TrimPrefix(key, "@"), ":registry")
			f.Registries = append(f.Registries, registryLine{Scope: scope, URL: strings.TrimSpace(strings.Trim(val, `"'`)), LineNo: i})
		case strings.HasPrefix(key, "//") && strings.Contains(key, ":"):
			idx := strings.LastIndex(key, ":")
			prefix, param := key[:idx], key[idx+1:]
			if param != "_authToken" && param != "_auth" {
				continue // username / email / password 之类，跟 token 状态无关
			}
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			value, env := resolveValue(val)
			f.Auth = append(f.Auth, authEntry{
				File:      path,
				LineNo:    i,
				Key:       key,
				Prefix:    prefix,
				Host:      hostOf(prefix),
				ProjectID: projectIDOf(prefix),
				Param:     param,
				RawValue:  val,
				Value:     value,
				EnvName:   env,
			})
		}
	}
	return f, nil
}

// resolveValue 支持 ${NPM_TOKEN} 这种环境变量写法
func resolveValue(v string) (value, envName string) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "${"), "}"))
		if name != "" && !strings.ContainsAny(name, "${}") {
			return os.Getenv(name), name
		}
	}
	return strings.Trim(v, `"'`), ""
}

func hostOf(prefix string) string {
	p := strings.TrimPrefix(prefix, "//")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

// projectIDOf 从 /api/v4/projects/<id>/packages/npm/ 里取出 id
func projectIDOf(prefix string) string {
	const marker = "/projects/"
	i := strings.Index(prefix, marker)
	if i < 0 {
		return ""
	}
	rest := prefix[i+len(marker):]
	j := strings.IndexByte(rest, '/')
	if j < 0 {
		j = len(rest)
	}
	id := rest[:j]
	for _, c := range id {
		if c < '0' || c > '9' {
			return "" // 也可能是 path 形式，暂不处理
		}
	}
	return id
}

// rewriteAuth 把指定行替换成新 token，保留其余内容不动；先备份再原子写
func rewriteAuth(path string, targets []authEntry, token string) (backup string, err error) {
	f, err := parseNpmrc(path)
	if err != nil {
		return "", err
	}
	changed := make([]string, len(f.Lines))
	copy(changed, f.Lines)
	for _, t := range targets {
		if t.File != path || t.LineNo >= len(changed) {
			continue
		}
		orig := changed[t.LineNo]
		i := strings.Index(orig, "=")
		if i < 0 {
			continue
		}
		changed[t.LineNo] = orig[:i+1] + token
	}

	backup = fmt.Sprintf("%s.bak.%s", path, timestamp())
	if err := copyFile(path, backup); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	if err := writeFileAtomic(path, strings.Join(changed, "\n")); err != nil {
		return backup, fmt.Errorf("写入失败: %w", err)
	}
	return backup, nil
}
