#!/usr/bin/env bash
# 跨平台交叉编译。用法：bash deploy/build.sh
# 产物：dist/ 目录下的各平台二进制。

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo dev)}"
BIN="rainclass-reminder"
# 版本号注入 cli（version 子命令与面板显示）和 notify（webhook 的 User-Agent）。
LDFLAGS="-s -w \
  -X github.com/infinite-galaxy/rainclass-reminder/internal/cli.Version=${VERSION} \
  -X github.com/infinite-galaxy/rainclass-reminder/internal/notify.Version=${VERSION}"
OUT="dist"

mkdir -p "$OUT"

# 纯 Go 编译，关闭 cgo 以保证静态链接与跨平台。
export CGO_ENABLED=0

# 守门检查：任何 cgo 依赖都说明"纯 Go"承诺被破坏，直接失败。
if go list -deps ./... 2>/dev/null | grep -q "runtime/cgo"; then
  echo "错误：检测到 runtime/cgo 被引入，项目要求 100% 纯 Go。" >&2
  exit 1
fi

targets=(
  "windows amd64"
  "windows arm64"
  "linux   amd64"
  "linux   arm64"
  "darwin  amd64"
  "darwin  arm64"
)

for t in "${targets[@]}"; do
  set -- $t
  os="$1"; arch="$2"

  name="${BIN}-${os}-${arch}"
  if [ "$os" = "windows" ]; then
    name="${name}.exe"
  fi

  echo "编译 ${os}/${arch} → ${OUT}/${name}"
  GOOS="$os" GOARCH="$arch" go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUT/$name" \
    "./cmd/${BIN}"
done

echo
echo "完成。产物："
ls -lh "$OUT"
