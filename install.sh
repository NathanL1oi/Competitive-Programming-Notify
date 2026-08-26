#!/usr/bin/env bash
# 安装 cp-notifier(Go 静态二进制)到 ~/.local/bin 并注册 systemd 用户服务(开机自启)
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$DIR/cp-notifier"

# 二进制缺失或源码更新过时,则重新构建(仅构建时需要 Go 工具链)
if [[ ! -x "$BIN" ]] || [[ -n "$(find "$DIR" -maxdepth 1 -name '*.go' -newer "$BIN")" ]]; then
    echo "正在构建静态二进制…"
    "$DIR/build.sh"
fi

install -Dm755 "$BIN" "$HOME/.local/bin/cp-notifier"
install -Dm644 "$DIR/cp-notifier.service" "$HOME/.config/systemd/user/cp-notifier.service"

# 配置文件: 若 ~/.config/cp-notifier/config.json 不存在则复制模板
if [[ ! -f "$HOME/.config/cp-notifier/config.json" ]]; then
    install -Dm644 "$DIR/config.json" "$HOME/.config/cp-notifier/config.json"
fi

systemctl --user daemon-reload
systemctl --user enable --now cp-notifier

# ---- DMS(Dank Material Shell)状态栏插件: 用户信息卡片 ----
# 软链本仓库的 quickshell-plugin 到 DMS 插件目录,启用并加入 DankBar 中部。
# 可用 CPN_SKIP_DMS=1 ./install.sh 跳过此步。
DMS_CFG="${XDG_CONFIG_HOME:-$HOME/.config}/DankMaterialShell"
PLUGIN_ID="cpUserCard"
if [[ "${CPN_SKIP_DMS:-0}" != "1" && -d "$DMS_CFG" ]]; then
    mkdir -p "$DMS_CFG/plugins"
    ln -sfn "$DIR/quickshell-plugin" "$DMS_CFG/plugins/$PLUGIN_ID"
    if command -v python3 >/dev/null; then
        DMS_CFG="$DMS_CFG" PLUGIN_ID="$PLUGIN_ID" python3 - <<'PYEOF'
import json, os, sys
cfg = os.environ["DMS_CFG"]; pid = os.environ["PLUGIN_ID"]

# 启用插件
ps_path = os.path.join(cfg, "plugin_settings.json")
try:
    ps = json.load(open(ps_path))
except Exception:
    ps = {}
ps.setdefault(pid, {})["enabled"] = True
json.dump(ps, open(ps_path, "w"), indent=2)

# 加入 DankBar 中部(跟在 weather 后面;已存在则不动)
s_path = os.path.join(cfg, "settings.json")
try:
    s = json.load(open(s_path))
    bars = s.get("barConfigs") or []
    if bars:
        cw = bars[0].setdefault("centerWidgets", [])
        def wid(w): return w if isinstance(w, str) else w.get("id")
        if pid not in [wid(w) for w in cw]:
            idx = next((i for i, w in enumerate(cw) if wid(w) == "weather"), len(cw) - 1)
            cw.insert(idx + 1, {"id": pid, "enabled": True})
        json.dump(s, open(s_path, "w"), indent=2)
except Exception as e:
    print("   ⚠️  自动加入 DankBar 失败(%s),请在 设置 → DankBar 布局 手动添加 %s" % (e, pid))
PYEOF
    fi
    # DMS 正在运行则立即生效;否则下次启动 DMS 时自动加载
    if command -v dms >/dev/null && dms ipc call plugins status "$PLUGIN_ID" >/dev/null 2>&1; then
        dms ipc call plugins enable "$PLUGIN_ID" >/dev/null 2>&1 || true
        echo "   ✅ DMS 插件 $PLUGIN_ID 已启用(DankBar 中部,天气旁边)"
    else
        echo "   ✅ DMS 插件已安装;DMS 重启后在 设置 → 插件 中启用 $PLUGIN_ID 并加入 DankBar"
    fi
fi

echo "✅ 安装完成。"
echo "   1. 编辑 ~/.config/cp-notifier/config.json 填入你的用户名,然后:"
echo "      systemctl --user restart cp-notifier"
echo "   2. 查看运行状态/日志:"
echo "      systemctl --user status cp-notifier"
echo "      journalctl --user -u cp-notifier -f"
echo "   3. DankBar 上的 CP 卡片点击可弹出用户信息;右键强制刷新"
