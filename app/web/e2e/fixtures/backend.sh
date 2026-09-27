#!/usr/bin/env bash
# 端到端测试用后端：每次运行使用全新的数据目录（安装由 global-setup 通过安装向导接口完成）。
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
data="${E2E_DATA_DIR:-$here/../.data}"
rm -rf "$data"
mkdir -p "$data"
cd "$here/../../../../server"
exec env KNOWFORGE_DATA="$data" GIN_MODE=release go run . -port "${E2E_API_PORT:-6989}"
