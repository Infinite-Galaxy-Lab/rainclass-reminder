#!/usr/bin/env bash
# 跨平台交叉编译脚本。
#
# 用法：bash deploy/build.sh
# 产物：dist/ 目录下的各平台二进制。

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo dev)}"
# 版本号注入两处：cli（version 子命令 / 面板显示）和 notify（webhook 的 User-Agent）。
# 只注入 cli 会导致 notify.Version 恒为 "dev"。
LDFLAGS="-s -w \
  -X github.com/2963609262/ykt-reminder/internal/cli.Version=${VERSION} \
  -X github.com/2963609262/ykt-reminder/internal/notify.Version=${VERSION}"
OUT="dist"

mkdir -p "$OUT"

# 纯 Go 编译，关闭 cgo 以保证静态链接与跨平台。
export CGO_ENABLED=0

# 守门检查：任何 cgo 依赖都说明"纯 Go"承诺被破坏，直接失败。
# 放在编译前，报错比编出一个动态链接的二进制更容易发现。
if go list -deps ./... 2>/dev/null | grep -q "runtime/cgo"; then
  echo "错误：检测到 runtime/cgo 被引入，项目要求 100% 纯 Go。" >&2
  echo "请检查新增依赖是否依赖 cgo。" >&2
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

  name="ykt-reminder-${os}-${arch}"
  if [ "$os" = "windows" ]; then
    name="${name}.exe"
  fi

  echo "编译 ${os}/${arch} → ${OUT}/${name}"
  GOOS="$os" GOARCH="$arch" go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUT/$name" \
    ./cmd/ykt-reminder
done

echo
echo "完成。产物："
ls -lh "$OUT"
