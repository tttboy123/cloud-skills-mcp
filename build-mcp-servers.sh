#!/bin/bash
# 一键编译 6 个 MCP server
# Phase 1 实现后会用

set -e

CLOUDS=(
  "tencent"
  "alicloud"
  "google"
  "aws"
  "azure"
  "baidu"
)

mkdir -p ~/bin

for cloud in "${CLOUDS[@]}"; do
  echo "🔨 Building ${cloud}-mcp..."
  go build -o ~/bin/${cloud}-mcp ./cmd/${cloud}-mcp/ 2>/dev/null || echo "   ⏭  ${cloud}-mcp 还没实现, skip"
done

echo ""
echo "✅ 编译完成. 已实现的 daemon 在 ~/bin/"
ls -la ~/bin/*-mcp 2>/dev/null || echo "   (还没有)"
