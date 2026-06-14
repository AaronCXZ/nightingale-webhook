#!/usr/bin/env bash
set -euo pipefail

echo "=== 环境检查 ==="

# Go
if command -v go &>/dev/null; then
  echo "GO_VERSION=$(go version | awk '{print $3}')"
else
  echo "MISSING: go"
  exit 1
fi

# 编译检查
echo "--- go build ---"
go build -o /dev/null ./cmd/webhook
echo "OK"

# 测试
echo "--- go test ---"
go test ./internal/... -count=1
echo "OK"

# 配置文件
if [ -f config.yaml ]; then
  echo "config.yaml: 存在"
else
  echo "config.yaml: 缺失（可复制 config.example.yaml 后编辑）"
fi

echo "=== 全部通过 ==="
