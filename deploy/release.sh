#!/usr/bin/env bash
# 发布版本：为当前版本打 v 标签并推送，触发 Release 工作流构建产物并创建 GitHub Release。
# Release Note 优先取自 CHANGELOG.md 中该版本的条目，缺失时回退到提交历史（见 .github/workflows/release.yml）。
#
# 用法：
#   deploy/release.sh                       # 发布 handler_setup.go 中的当前版本
#   deploy/release.sh 2026.0.1              # 指定发布版本号
#   deploy/release.sh --next 2026.1.0       # 指定发布后开启的下一个版本号
#   deploy/release.sh 2026.0.1 --next 2026.1.0
#   deploy/release.sh --allow-dirty         # 工作区有未提交改动时仍允许发布（标签不含这些改动）
# 发布成功后自动开启下一版本（默认 patch +1）并提交为 chore: start version X（只提交版本文件，不推送）。
set -euo pipefail
cd "$(dirname "$0")/.."

RELEASE_BRANCH="dev"
SETUP_GO="server/internal/app/handler_setup.go"

VERSION=""
NEXT=""
ALLOW_DIRTY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --next)
      [ -n "${2:-}" ] || { echo "--next 需要版本号参数" >&2; exit 1; }
      NEXT="$2"
      shift 2
      ;;
    --next=*)
      NEXT="${1#--next=}"
      shift
      ;;
    -*)
      echo "未知选项：$1" >&2
      exit 1
      ;;
    *)
      [ -z "$VERSION" ] || { echo "发布版本号只能指定一次（收到：$1）" >&2; exit 1; }
      VERSION="$1"
      shift
      ;;
  esac
done

VERSION="${VERSION:-$(grep -oE 'var Version = "[0-9.]+"' "$SETUP_GO" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)}"
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "无法确定版本号（收到：${VERSION}）" >&2
  exit 1
fi
TAG="v${VERSION}"

if [ -z "$NEXT" ]; then
  IFS='.' read -r ny nm np <<<"$VERSION"
  NEXT="${ny}.${nm}.$((np + 1))"
fi
if ! [[ "$NEXT" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "下一版本号格式应为 YYYY.MAJOR.PATCH（收到：${NEXT}）" >&2
  exit 1
fi
if [ "$NEXT" = "$VERSION" ]; then
  echo "下一版本号（$NEXT）不能与发布版本相同" >&2
  exit 1
fi

# ── 预检 ──
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [ "$BRANCH" != "$RELEASE_BRANCH" ]; then
  echo "当前分支为 ${BRANCH}，请切换到默认分支 ${RELEASE_BRANCH} 再发布" >&2
  exit 1
fi
if [ "$ALLOW_DIRTY" -eq 1 ]; then
  echo "⚠️  --allow-dirty：忽略未提交改动继续发布，标签不包含这些改动" >&2
elif [ -n "$(git status --porcelain)" ]; then
  echo "工作区有未提交改动，请先提交后再发布（或使用 --allow-dirty 跳过该检查）：" >&2
  git status --short
  exit 1
fi
git fetch --quiet origin "$RELEASE_BRANCH"
if [ "$(git rev-parse HEAD)" != "$(git rev-parse "origin/$RELEASE_BRANCH")" ]; then
  echo "本地 $RELEASE_BRANCH 与 origin 不一致，请先 git push origin $RELEASE_BRANCH" >&2
  exit 1
fi
if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "标签 $TAG 已存在，请先 deploy/new-version.sh 开启新版本" >&2
  exit 1
fi

# ── 版本一致性预检：各版本载体应与发布版本一致，避免产物版本漂移 ──
check_version() {
  local label="$1" file="$2" pattern="$3" got
  got="$(grep -Eom1 "$pattern" "$file" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  if [ "$got" != "$VERSION" ]; then
    echo "版本不一致：$label（$file）为 ${got:-未找到}，应为 $VERSION" >&2
    echo "请先运行 deploy/new-version.sh 统一版本号" >&2
    exit 1
  fi
}
check_version "Go var Version" "$SETUP_GO" 'var Version = "[0-9.]+"'
check_version "web/package.json" "app/web/package.json" '"version": "[0-9.]+"'
check_version "desktop/package.json" "app/desktop/package.json" '"version": "[0-9.]+"'
check_version "tauri.conf.json" "app/desktop/src-tauri/tauri.conf.json" '"version": "[0-9.]+"'
check_version "Cargo.toml" "app/desktop/src-tauri/Cargo.toml" '^version = "[0-9.]+"'

# ── CHANGELOG 预检：Release Note 取自 CHANGELOG.md 中该版本的条目 ──
NOTES="$(awk -v ver="$VERSION" '
  $0 ~ ("^## \\[" ver "\\]") { grab = 1; next }
  grab && /^## \[/ { exit }
  grab { print }
' CHANGELOG.md)"
if [ -z "$(printf '%s' "$NOTES" | tr -d '[:space:]')" ] || printf '%s' "$NOTES" | grep -q '待补充'; then
  echo "⚠️  CHANGELOG.md 尚无 ${VERSION} 的有效条目（或仍为「待补充」），Release Note 将回退到提交历史。" >&2
  echo "    建议先运行 deploy/new-version.sh 生成草稿，整理 CHANGELOG.md 后再发布。" >&2
fi

echo "将发布 ${TAG}（版本 ${VERSION}）到 $(git remote get-url origin)，发布后自动开启下一版本 ${NEXT}"
read -r -p "确认发布？(y/N) " ans
case "$ans" in
  y | Y) ;;
  *) echo "已取消"; exit 0 ;;
esac

git tag -a "$TAG" -m "Release $VERSION"
git push origin "$TAG"

echo "✅ 已推送标签 ${TAG}，Release 工作流开始构建并发布。"

# ── 开启下一版本：统一 bump 各处版本号并转正 CHANGELOG，并自动提交（只提交这些版本文件，不推送） ──
deploy/new-version.sh "$NEXT"
VERSION_FILES=(
  "$SETUP_GO" CHANGELOG.md app/web/package.json app/desktop/package.json
  app/desktop/src-tauri/tauri.conf.json app/desktop/src-tauri/Cargo.toml
)
git add -- "${VERSION_FILES[@]}"
if git diff --cached --quiet; then
  echo "版本文件没有变化，跳过提交。"
else
  git commit --quiet -m "chore: start version ${NEXT}" -- "${VERSION_FILES[@]}"
  echo "✅ 已提交下一版本 ${NEXT}（chore: start version ${NEXT}），确认后执行：git push origin ${RELEASE_BRANCH}"
fi
if command -v gh >/dev/null 2>&1; then
  echo "查看进度：gh run watch"
  gh run list --workflow=Release --limit=1 2>/dev/null || true
else
  echo "在 GitHub Actions 页面查看 Release 工作流进度。"
fi
