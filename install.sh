#!/usr/bin/env bash
# 把 cline-channel 插件安装到 CPA。
#
# 用法：
#   ./install.sh --container cli-proxy-api              # CPA 跑在 Docker 里
#   ./install.sh --plugin-dir /opt/cpa/plugins          # CPA 直接跑在主机上
#   ./install.sh --container cpa --container-path /app/plugins/cline-channel.so
#
# 已存在的旧版本会先备份成 .bak-<时间戳>，方便回滚。
set -euo pipefail

CONTAINER=""
PLUGIN_DIR=""
CONTAINER_PATH="/CLIProxyAPI/plugins/cline-channel.so"
SOURCE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/dist/cline-channel.so"

while [ $# -gt 0 ]; do
  case "$1" in
    --container)       CONTAINER="${2:-}"; shift 2 ;;
    --plugin-dir)      PLUGIN_DIR="${2:-}"; shift 2 ;;
    --container-path)  CONTAINER_PATH="${2:-}"; shift 2 ;;
    --source)          SOURCE="${2:-}"; shift 2 ;;
    -h|--help)         sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "未知参数：$1" >&2; exit 1 ;;
  esac
done

[ -f "$SOURCE" ] || { echo "找不到插件文件：$SOURCE（先运行 ./build.ps1，或使用仓库自带的 dist/cline-channel.so）" >&2; exit 1; }
if [ -z "$CONTAINER" ] && [ -z "$PLUGIN_DIR" ]; then
  echo "请指定 --container（Docker 部署）或 --plugin-dir（主机部署）之一。" >&2
  exit 1
fi

STAMP="$(date +%Y%m%d-%H%M%S)"

if [ -n "$CONTAINER" ]; then
  echo "→ 安装到容器 $CONTAINER : $CONTAINER_PATH"
  docker exec "$CONTAINER" sh -c "if [ -f '$CONTAINER_PATH' ]; then cp '$CONTAINER_PATH' '$CONTAINER_PATH.bak-$STAMP'; fi" || true
  docker cp "$SOURCE" "${CONTAINER}:${CONTAINER_PATH}"
  echo "→ 重启 CPA 使其加载新插件"
  docker restart "$CONTAINER" >/dev/null
else
  [ -d "$PLUGIN_DIR" ] || { echo "插件目录不存在：$PLUGIN_DIR" >&2; exit 1; }
  TARGET="$PLUGIN_DIR/cline-channel.so"
  if [ -f "$TARGET" ]; then
    cp "$TARGET" "$TARGET.bak-$STAMP"
    echo "→ 已备份旧版本：cline-channel.so.bak-$STAMP"
  fi
  cp "$SOURCE" "$TARGET"
  echo "→ 已安装到 $TARGET"
  echo "→ 请重启 CPA 使其加载新插件"
fi

cat <<'EOF'

完成。接下来：
  1) 确认 CPA 的 config.yaml 里 plugins.enabled = true 且 configs.cline-channel.enabled = true
  2) 打开面板填写 Cline API Key：
     http://<CPA 地址>/v0/resource/plugins/cline-channel/panel
  3) 建议同时设置 disable-cooling: true（见 README「重要」一节）
EOF
