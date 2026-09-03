#!/usr/bin/env bash
set -euo pipefail
REPO=kkx999/Xs5
API="https://api.github.com/repos/$REPO/releases/latest"
ARCH_RAW=$(uname -m)
case "$ARCH_RAW" in
  x86_64|amd64) ARCH=amd64;;
  aarch64|arm64) ARCH=arm64;;
  *) echo "暂不支持架构: $ARCH_RAW"; exit 1;;
esac
[[ $EUID -eq 0 ]] || { echo '请使用 root 运行'; exit 1; }
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo '正在获取 Xs5 最新版本...'
JSON=$(curl -fsSL --retry 3 "$API" || true)
TAG=$(printf '%s' "$JSON" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
if [[ -n "$TAG" ]]; then
  FILE="xs5-${TAG}-linux-${ARCH}.tar.gz"
  BASE="https://github.com/$REPO/releases/download/$TAG"
  echo "最新版本: $TAG ($ARCH)"
  curl -fL --retry 3 "$BASE/$FILE" -o "$TMP/$FILE"
  if curl -fL --retry 2 "$BASE/SHA256SUMS" -o "$TMP/SHA256SUMS" 2>/dev/null; then
    (cd "$TMP" && grep "  $FILE\$" SHA256SUMS | sha256sum -c -)
  fi
  mkdir -p "$TMP/pkg"
  tar -xzf "$TMP/$FILE" -C "$TMP/pkg"
  cd "$TMP/pkg"
  bash install.sh
else
  echo '暂未检测到 Release，改用 main 源码安装。'
  curl -fL --retry 3 "https://github.com/$REPO/archive/refs/heads/main.tar.gz" -o "$TMP/source.tar.gz"
  tar -xzf "$TMP/source.tar.gz" -C "$TMP"
  cd "$TMP/Xs5-main"
  bash install.sh
fi
