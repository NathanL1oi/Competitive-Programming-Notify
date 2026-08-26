#!/usr/bin/env bash
# 构建静态二进制 cp-notifier(CGO 关闭,单文件可移植)
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o cp-notifier .
echo "✅ 已构建: $DIR/cp-notifier"
