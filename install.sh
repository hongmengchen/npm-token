#!/bin/sh
# npm-token 安装脚本（macOS / Linux）
#
#   curl -fsSL https://raw.githubusercontent.com/hongmengchen/npm-token/main/install.sh | sh
#
# 可选参数：
#   -v, --version v0.1.0   装指定版本（默认最新）
#   -b, --bindir DIR       装到指定目录（默认 ~/.local/bin；root 下默认 /usr/local/bin）
set -eu

REPO="hongmengchen/npm-token"
BIN="npm-token"
VERSION=""
BIN_DIR=""

die() { printf '错误: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<EOF
npm-token 安装脚本

用法:
  sh install.sh [-v vX.Y.Z] [-b 安装目录]
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    -v|--version)
      [ $# -ge 2 ] || die "--version 缺少参数"
      VERSION="$2"; shift 2 ;;
    -b|--bindir)
      [ $# -ge 2 ] || die "--bindir 缺少参数"
      BIN_DIR="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "不认识的参数: $1" ;;
  esac
done

# ---- 系统与架构 ----
# Git Bash / MSYS / Cygwin 里 uname -s 是 MINGW64_NT-… 这类值，按 Windows 处理
os=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  mingw*|msys*|cygwin*|windows*) os=windows ;;
  *) die "不支持的系统: ${os:-未知}（可去 https://github.com/$REPO/releases 手动下载）" ;;
esac

machine=$(uname -m 2>/dev/null)
case "$machine" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "不支持的 CPU 架构: ${machine:-未知}" ;;
esac

asset="${BIN}_${os}_${arch}"
[ "$os" = "windows" ] && asset="${asset}.exe"

if [ -n "$VERSION" ]; then
  case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
  base="https://github.com/$REPO/releases/download/$VERSION"
  label="$VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
  label="最新版"
fi

# ---- 下载工具 ----
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "需要 curl 或 wget"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t npm-token)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf '下载 %s (%s %s)…\n' "$BIN" "$os" "$arch"
fetch "$base/$asset" "$tmp/$asset" || die "下载失败: $base/$asset"

# ---- 校验（有 checksums.txt 就校验，没有就跳过）----
if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  want=$(awk -v f="$asset" '$2==f {print $1; exit}' "$tmp/checksums.txt")
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    else
      got=""
    fi
    if [ -n "$got" ] && [ "$got" != "$want" ]; then
      die "校验失败：下载到的文件与 checksums.txt 不一致"
    fi
  fi
fi

# ---- 安装位置 ----
if [ -z "$BIN_DIR" ]; then
  if [ "$(id -u)" = "0" ]; then
    BIN_DIR="/usr/local/bin"
  else
    BIN_DIR="$HOME/.local/bin"
  fi
fi
mkdir -p "$BIN_DIR" 2>/dev/null || die "无法创建目录 $BIN_DIR"
[ -w "$BIN_DIR" ] || die "目录不可写: $BIN_DIR（用 -b 换一个，或用 sudo 重跑）"

target="$BIN_DIR/$BIN"
[ "$os" = "windows" ] && target="${target}.exe"
if [ -e "$target" ] && [ ! -w "$target" ]; then
  die "$target 不可写，请用 sudo 或换一个 -b 目录"
fi

chmod 755 "$tmp/$asset"
mv -f "$tmp/$asset" "$target"

printf '已安装: %s\n' "$target"
"$target" version || true

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    printf '\n%s 不在 PATH 里，把它加进去（重开终端后生效）：\n' "$BIN_DIR"
    if [ "$os" = "windows" ]; then
      printf '  echo '\''export PATH="%s:$PATH"'\'' >> ~/.bashrc\n' "$BIN_DIR"
      printf '（想在 PowerShell / CMD 里也能用，建议改用 install.ps1）\n'
    else
      printf '  echo '\''export PATH="%s:$PATH"'\'' >> ~/.zshrc\n' "$BIN_DIR"
    fi
    ;;
esac

printf '\n运行 %s 打开 TUI。\n' "$BIN"
