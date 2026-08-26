#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
cp-notifier — Codeforces / AtCoder 提交结果桌面通知器(Python 参考实现)

功能与 Go 版(cp-notifier 二进制)完全对齐:
- Codeforces 官方 API(user.status / user.rating),可选 apiKey+apiSig 签名认证;
- AtCoder 提交(kenkoooo.com 第三方 API);
- 多用户名(每个账号独立跟踪)、赛后 rating 变化通知;
- 状态文件与 Go 版格式互通,两个版本可互换运行。

用法:
    ./cp-notifier.py                       # 前台持续监视(Ctrl+C 退出)
    ./cp-notifier.py --once                # 只检查一轮后退出(适合测试)
    ./cp-notifier.py --test                # 发送一条测试通知后退出
    ./cp-notifier.py --no-notify           # 只在终端打印,不弹通知
    ./cp-notifier.py --cf tourist,petr --ac chokudai  # 多用户逗号分隔(写入配置)
"""

import argparse
import hashlib
import hmac
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from datetime import datetime

APP_NAME = "CP Notifier"
VERSION = "3.0.0"
USER_AGENT = f"cp-notifier/{VERSION}"

XDG_CONFIG = os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config"))
XDG_STATE = os.environ.get("XDG_STATE_HOME", os.path.expanduser("~/.local/state"))
if not os.path.isdir(XDG_STATE):
    XDG_STATE = os.environ.get("XDG_CACHE_HOME", os.path.expanduser("~/.cache"))
CONFIG_PATH = os.path.join(XDG_CONFIG, "cp-notifier", "config.json")
STATE_PATH = os.path.join(XDG_STATE, "cp-notifier", "state.json")

DEFAULT_CONFIG = {
    "codeforces_handles": [],      # Codeforces 用户名(可多个)
    "atcoder_handles": [],         # AtCoder 用户名(可多个)
    "codeforces_api_key": "",      # Codeforces API key(可选,授权限流更高)
    "codeforces_api_secret": "",   # Codeforces API secret
    "poll_interval_idle": 15,      # 没有在评测的提交时,轮询间隔(秒)
    "poll_interval_active": 3,     # 有在评测的提交时,轮询间隔(秒)
    "rating_check_interval": 300,  # rating 变化检查间隔(秒),0=关闭
    "critical_on_fail": True,      # 未通过时用 critical 级别通知(更醒目)
}

# 每个平台: label 说明文字, emoji, ok(None=未出结果, True=通过, False=未通过)
CF_VERDICTS = {
    "TESTING":                 ("评测中",                 "⏳", None),
    "OK":                      ("Accepted",               "✅", True),
    "WRONG_ANSWER":            ("Wrong Answer",           "❌", False),
    "TIME_LIMIT_EXCEEDED":     ("Time Limit Exceeded",    "⏱️", False),
    "MEMORY_LIMIT_EXCEEDED":   ("Memory Limit Exceeded",  "🐘", False),
    "RUNTIME_ERROR":           ("Runtime Error",          "💥", False),
    "COMPILATION_ERROR":       ("Compile Error",          "🔧", False),
    "PRESENTATION_ERROR":      ("Presentation Error",     "📄", False),
    "IDLENESS_LIMIT_EXCEEDED": ("Idleness Limit Exceeded", "😴", False),
    "SKIPPED":                 ("Skipped",                "⏭️", False),
    "CHALLENGED":              ("Hacked",                 "🗡️", False),
    "REJECTED":                ("Rejected",               "🚫", False),
    "PARTIAL":                 ("Partial",                "🟡", None),
    "CRASHED":                 ("Crashed",                "💥", False),
    "SECURITY_VIOLATION":      ("Security Violation",     "🛡️", False),
    "FAILED":                  ("Failed",                 "❌", False),
}

AC_VERDICTS = {
    "WJ":  ("评测中",               "⏳", None),
    "WR":  ("等待重判",             "⏳", None),
    "AC":  ("Accepted",             "✅", True),
    "WA":  ("Wrong Answer",         "❌", False),
    "TLE": ("Time Limit Exceeded",  "⏱️", False),
    "MLE": ("Memory Limit Exceeded", "🐘", False),
    "RE":  ("Runtime Error",        "💥", False),
    "CE":  ("Compile Error",        "🔧", False),
    "OLE": ("Output Limit Exceeded", "📤", False),
    "IE":  ("Internal Error",       "⚠️", False),
}
AC_PENDING = {"WJ", "WR", "", None}

PENDING_TIMEOUT = 6 * 3600   # 提交超过 6 小时无结果则放弃跟踪
AC_LOOKBACK = 2 * 3600      # AtCoder 回看窗口(kenkoooo 升序返回、每请求最多 500 条)

# 相同提示的最小重复间隔(秒),避免网络故障时每轮刷屏
_THROTTLE = {}


def log(msg):
    ts = datetime.now().strftime("%H:%M:%S")
    print(f"[{ts}] {msg}", flush=True)


def log_throttled(key, msg, interval=3600):
    now = time.time()
    if now - _THROTTLE.get(key, 0) >= interval:
        _THROTTLE[key] = now
        log(msg)


# ---------------------------------------------------------------- 配置与状态

def load_config(explicit=None):
    """配置来源: --config 参数 > ~/.config/cp-notifier/config.json。
    兼容旧版单用户键(codeforces_handle/atcoder_handle),归一化为 handles 数组。"""
    cfg = dict(DEFAULT_CONFIG)

    def read(path):
        with open(path, encoding="utf-8") as f:
            raw = json.load(f)
        if isinstance(raw.get("codeforces_handles"), list):
            cfg["codeforces_handles"] = [h for h in raw["codeforces_handles"] if str(h).strip()]
        elif raw.get("codeforces_handle"):
            cfg["codeforces_handles"] = [str(raw["codeforces_handle"]).strip()]
        if isinstance(raw.get("atcoder_handles"), list):
            cfg["atcoder_handles"] = [h for h in raw["atcoder_handles"] if str(h).strip()]
        elif raw.get("atcoder_handle"):
            cfg["atcoder_handles"] = [str(raw["atcoder_handle"]).strip()]
        for k in ("codeforces_api_key", "codeforces_api_secret"):
            cfg[k] = str(raw.get(k, cfg[k]))
        for k in ("poll_interval_idle", "poll_interval_active"):
            if isinstance(raw.get(k), int) and raw[k] > 0:
                cfg[k] = raw[k]
        # rating 间隔允许显式填 0(关闭检查)
        if isinstance(raw.get("rating_check_interval"), int) and raw["rating_check_interval"] >= 0:
            cfg["rating_check_interval"] = raw["rating_check_interval"]
        if "critical_on_fail" in raw:
            cfg["critical_on_fail"] = bool(raw["critical_on_fail"])
        return path

    if explicit:
        if not os.path.isfile(explicit):
            save_config(cfg, explicit)
            log(f"已生成默认配置: {explicit}")
            return cfg, explicit
        try:
            return cfg, read(explicit)
        except Exception as e:
            log(f"配置文件读取失败({explicit}): {e}")
            return cfg, explicit

    if os.path.isfile(CONFIG_PATH):
        try:
            return cfg, read(CONFIG_PATH)
        except Exception as e:
            log(f"配置文件读取失败({CONFIG_PATH}): {e}")

    save_config(cfg, CONFIG_PATH)
    log(f"已生成默认配置: {CONFIG_PATH}")
    return cfg, CONFIG_PATH


def save_config(cfg, path):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(cfg, f, ensure_ascii=False, indent=2)
    try:
        os.chmod(path, 0o600)  # 配置可能包含 api secret
    except OSError:
        pass


def new_state():
    return {
        "codeforces": {"accounts": {}},
        "atcoder":   {"accounts": {}},
    }


def new_account(handle):
    return {
        "handle": handle,
        "last_id": 0,
        "pending": {},
        "last_rating_ts": 0,
        "last_rating_check": 0,
    }


def load_state(path=None):
    p = path or STATE_PATH
    if os.path.isfile(p):
        try:
            with open(p, encoding="utf-8") as f:
                st = json.load(f)
            # 旧版(单账号)状态文件没有 accounts 键,当作全新状态
            st.setdefault("codeforces", new_state()["codeforces"])
            st.setdefault("atcoder", new_state()["atcoder"])
            st["codeforces"].setdefault("accounts", {})
            st["atcoder"].setdefault("accounts", {})
            for platform in ("codeforces", "atcoder"):
                for h, acc in st[platform]["accounts"].items():
                    acc.setdefault("handle", h)
                    acc.setdefault("last_id", 0)
                    acc.setdefault("pending", {})
                    acc.setdefault("last_rating_ts", 0)
                    acc.setdefault("last_rating_check", 0)
            return st
        except Exception as e:
            log(f"状态文件损坏,已重新初始化({p}): {e}")
    return new_state()


def save_state(st, path=None):
    p = path or STATE_PATH
    os.makedirs(os.path.dirname(p), exist_ok=True)
    tmp = p + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(st, f, ensure_ascii=False, indent=2)
    os.replace(tmp, p)


def ensure_account(st, platform, handle):
    accounts = st[platform]["accounts"]
    if handle not in accounts:
        accounts[handle] = new_account(handle)
        log(f"{platform}: 新增账号 {handle}")
    return accounts[handle]


def prune_pending(pending):
    now = time.time()
    for sid in list(pending):
        if now - pending[sid].get("seen_at", now) > PENDING_TIMEOUT:
            log(f"放弃等待提交 #{sid}(超过 {PENDING_TIMEOUT // 3600} 小时无结果)")
            del pending[sid]


def split_handles(s):
    return [p.strip() for p in s.split(",") if p.strip()]


# ---------------------------------------------------------------- HTTP 抓取

def http_json(url, tries=3, timeout=15):
    last = None
    for i in range(tries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return json.loads(r.read().decode("utf-8"))
        except Exception as e:
            last = e
            time.sleep(1.5 * (i + 1))
    raise last


def http_get(url, timeout=15):
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode("utf-8")


def cf_signed_params(method, base, key, secret, now=None, rand_str=None):
    """按 Codeforces 官方规则生成带签名参数:
    hash 输入 = rand + "/" + method + "?" + 按字母序排序的 query(含 apiKey/time) + "#" + secret
    apiSig = rand + sha512hex(...) 的前 6 个十六进制字符
    """
    params = dict(base)
    params["apiKey"] = key
    params["time"] = str(int(time.time()) if now is None else now)
    if rand_str is None:
        import random
        rand_str = "".join(random.choice("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
                           for _ in range(6))
    query = "&".join(f"{k}={params[k]}" for k in sorted(params))
    msg = f"{rand_str}/{method}?{query}#{secret}"
    sig = hmac.new(secret.encode(), msg.encode(), hashlib.sha512).hexdigest()[:6]
    params["apiSig"] = rand_str + sig
    return params


def cf_api(method, params, cfg):
    """调用 Codeforces API;配置了 key/secret 时带签名,认证失败回退到无认证。"""
    key, secret = cfg.get("codeforces_api_key", ""), cfg.get("codeforces_api_secret", "")
    if key and secret:
        signed = cf_signed_params(method, params, key, secret)
        url = "https://codeforces.com/api/" + method + "?" + urllib.parse.urlencode(signed)
        try:
            return http_get(url)
        except Exception:
            log_throttled("cf_auth_fallback",
                          "Codeforces 认证请求失败,已回退到无认证请求(请检查 api key/secret 与系统时间)")
    url = "https://codeforces.com/api/" + method + "?" + urllib.parse.urlencode(params)
    return http_get(url)


def fetch_codeforces(handle, cfg):
    """Codeforces 官方 API user.status,返回最近的提交列表"""
    data = json.loads(cf_api("user.status",
                             {"handle": handle, "from": 1, "count": 20}, cfg))
    if data.get("status") != "OK":
        raise RuntimeError(data.get("comment", "Codeforces API 返回异常"))
    return data["result"]


def fetch_codeforces_rating(handle, cfg):
    """Codeforces 官方 API user.rating,返回全部历史 rating 变化"""
    data = json.loads(cf_api("user.rating", {"handle": handle}, cfg))
    if data.get("status") != "OK":
        raise RuntimeError(data.get("comment", "user.rating 返回异常"))
    return data["result"]


def fetch_atcoder(handle, cfg):
    """AtCoder 提交列表(kenkoooo.com 第三方 API)"""
    since = int(time.time()) - AC_LOOKBACK
    url = ("https://kenkoooo.com/atcoder/atcoder-api/v3/user/submissions?"
           + urllib.parse.urlencode({"user": handle, "from_second": since}))
    return http_json(url)


# ---------------------------------------------------------------- 通知

class Notifier:
    def __init__(self, enabled):
        self.enabled = enabled
        self.notify_send = shutil.which("notify-send")
        self._warned = False
        if enabled and not self.notify_send:
            log("警告: 未找到 notify-send,无法弹窗(sudo dnf install libnotify)")
            self._warned = True

    def send(self, title, body, urgency="normal", timeout_ms=8000):
        log(f"📢 {title}")
        if body:
            log("   " + body.replace("\n", "\n   "))
        if not self.enabled or not self.notify_send:
            return False
        cmd = [self.notify_send, "-a", APP_NAME]
        if urgency in ("low", "critical"):
            cmd += ["-u", urgency]
        if timeout_ms:
            cmd += ["-t", str(timeout_ms)]
        cmd += [title, body]
        try:
            r = subprocess.run(cmd, capture_output=True, timeout=10)
            err = r.stderr.decode(errors="replace").strip()
            ok = r.returncode == 0
        except Exception as e:
            err, ok = str(e), False
        if not ok:
            if not self._warned:
                log("警告: 通知发送失败——有正在运行的通知守护进程吗?"
                    "(如 DMS/mako/dunst,可用 `busctl --user status org.freedesktop.Notifications` 检查)")
                if err:
                    log(f"  详情: {err}")
                self._warned = True
            return False
        self._warned = False
        return True


# ---------------------------------------------------------------- 通知内容

def cf_tag(handle, multi):
    return f"Codeforces·{handle}" if multi else "Codeforces"


def ac_tag(handle, multi):
    return f"AtCoder·{handle}" if multi else "AtCoder"


def cf_info(s):
    return {
        "problem": f"{s['contestId']}{s['problem']['index']}. {s['problem']['name']}",
        "lang": s.get("programmingLanguage", "?"),
        "verdict": s.get("verdict") or "TESTING",
        "passedTestCount": s.get("passedTestCount"),
        "timeConsumedMillis": s.get("timeConsumedMillis"),
        "memoryConsumedBytes": s.get("memoryConsumedBytes"),
        "seen_at": time.time(),
    }


def ac_info(s):
    return {
        "problem": s.get("problem_id", "?"),
        "lang": s.get("language", "?"),
        "result": s.get("result") or "WJ",
        "execution_time": s.get("execution_time"),
        "seen_at": time.time(),
    }


def announce_cf_result(tag, sid, info, notifier, cfg):
    v = info["verdict"]
    label, emoji, ok = CF_VERDICTS.get(v, (v, "❔", None))
    urgency = "critical" if (ok is False and cfg.get("critical_on_fail", True)) else "normal"
    lines = [info["problem"], f"语言: {info['lang']}"]
    if v in ("OK", "WRONG_ANSWER", "PARTIAL") and info.get("passedTestCount") is not None:
        lines.append(f"通过 {info['passedTestCount']} 组测试")
    tail = []
    if info.get("timeConsumedMillis") is not None:
        tail.append(f"{info['timeConsumedMillis']} ms")
    if info.get("memoryConsumedBytes") is not None:
        tail.append(f"{info['memoryConsumedBytes'] // 1024} KB")
    if tail:
        lines.append(" · ".join(tail))
    notifier.send(f"[{tag}] #{sid} {emoji} {label}", "\n".join(lines), urgency, 15000)


def announce_ac_result(tag, sid, info, notifier, cfg):
    r = info["result"]
    label, emoji, ok = AC_VERDICTS.get(r, (r, "❔", None))
    urgency = "critical" if (ok is False and cfg.get("critical_on_fail", True)) else "normal"
    lines = [info["problem"], f"语言: {info['lang']}"]
    if info.get("execution_time") is not None:
        lines.append(f"耗时: {info['execution_time']} ms")
    notifier.send(f"[{tag}] #{sid} {emoji} {label}", "\n".join(lines), urgency, 15000)


def cf_rank(rating):
    if rating < 1200: return "Newbie"
    if rating < 1400: return "Pupil"
    if rating < 1600: return "Specialist"
    if rating < 1900: return "Expert"
    if rating < 2100: return "Candidate Master"
    if rating < 2300: return "Master"
    if rating < 2400: return "International Master"
    if rating < 2600: return "Grandmaster"
    if rating < 3000: return "International Grandmaster"
    return "Legendary Grandmaster"


def announce_rating(tag, c, notifier):
    diff = c["newRating"] - c["oldRating"]
    emoji = "🎉" if diff > 0 else ("😢" if diff < 0 else "➖")
    lines = [c["contestName"]]
    if c["oldRating"] == 0 and c["newRating"] == 0:
        lines.append("未评级比赛(不计分)")
    else:
        lines.append(f"{c['oldRating']} → {c['newRating']} ({diff:+d})")
        old_rank, new_rank = cf_rank(c["oldRating"]), cf_rank(c["newRating"])
        if old_rank != new_rank:
            lines.append(f"段位: {old_rank} → {new_rank}")
        else:
            lines.append(f"段位: {new_rank}")
    if c.get("rank"):
        lines.append(f"名次: {c['rank']}")
    notifier.send(f"[{tag}] Rating 更新 {emoji}", "\n".join(lines), "normal", 15000)


# ---------------------------------------------------------------- 各平台轮询

def poll_codeforces(handles, st, notifier, cfg, state_path, verbose=False):
    active = False
    multi = len(handles) > 1
    for h in handles:
        if poll_cf_account(h, multi, st, notifier, cfg, state_path, verbose):
            active = True
    check_cf_ratings(handles, multi, st, notifier, cfg, state_path)
    return active


def poll_cf_account(handle, multi, st, notifier, cfg, state_path, verbose=False):
    try:
        subs = fetch_codeforces(handle, cfg)
    except Exception as e:
        log_throttled(f"cf_err_{handle}", f"Codeforces 抓取失败({handle}): {e}")
        return False
    if verbose:
        log(f"Codeforces({handle}): 获取到 {len(subs)} 条提交记录")
    if not subs:
        log_throttled(f"cf_empty_{handle}",
                      f"Codeforces: 账号 {handle} 没有提交记录,将保持静默直到检测到其第一次提交"
                      "(若用户名有误请检查配置)")
        return False

    acc = ensure_account(st, "codeforces", handle)
    tag = cf_tag(handle, multi)
    prune_pending(acc["pending"])

    if acc["last_id"] == 0:
        top = max(subs, key=lambda s: s["id"])
        acc["last_id"] = top["id"]
        if (top.get("verdict") or "TESTING") == "TESTING":
            acc["pending"][str(top["id"])] = cf_info(top)
            log(f"检测到进行中的提交 #{top['id']},开始跟踪其评测结果")
        log(f"Codeforces({handle}) 已初始化(最新提交 #{acc['last_id']})")
        save_state(st, state_path)
        return bool(acc["pending"])

    old_last = acc["last_id"]
    fresh = sorted((s for s in subs if s["id"] > old_last), key=lambda s: s["id"])
    acc["last_id"] = max(old_last, max((s["id"] for s in subs), default=old_last))
    by_id = {s["id"]: s for s in subs}

    for s in fresh:
        info = cf_info(s)
        v = info["verdict"]
        label, emoji, _ = CF_VERDICTS.get(v, (v, "❔", None))
        notifier.send(
            f"[{tag}] 新提交 #{s['id']} · {emoji} {label}",
            f"{info['problem']}\n语言: {info['lang']}",
            "normal", 8000)
        if v != "TESTING":
            announce_cf_result(tag, str(s["id"]), info, notifier, cfg)
        else:
            acc["pending"][str(s["id"])] = info

    for sid in list(acc["pending"]):
        cur = by_id.get(int(sid))
        if cur is None:
            continue
        v = cur.get("verdict") or "TESTING"
        if v != "TESTING":
            info = acc["pending"][sid]
            info.update({"verdict": v,
                         "passedTestCount": cur.get("passedTestCount"),
                         "timeConsumedMillis": cur.get("timeConsumedMillis"),
                         "memoryConsumedBytes": cur.get("memoryConsumedBytes")})
            announce_cf_result(tag, sid, info, notifier, cfg)
            acc["pending"].pop(sid, None)

    save_state(st, state_path)
    return bool(acc["pending"])


def check_cf_ratings(handles, multi, st, notifier, cfg, state_path):
    interval = cfg.get("rating_check_interval", 0)
    if interval <= 0:
        return
    now = int(time.time())
    for h in handles:
        acc = ensure_account(st, "codeforces", h)
        if now - acc.get("last_rating_check", 0) < interval:
            continue
        acc["last_rating_check"] = now
        save_state(st, state_path)
        try:
            changes = fetch_codeforces_rating(h, cfg)
        except Exception as e:
            log_throttled(f"cf_rating_err_{h}", f"Codeforces rating 抓取失败({h}): {e}")
            continue
        if not changes:
            log_throttled(f"cf_rating_empty_{h}", f"Codeforces: 账号 {h} 暂无 Rated 记录")
            continue

        max_ts = max(c["ratingUpdateTimeSeconds"] for c in changes)
        if acc.get("last_rating_ts", 0) == 0:
            acc["last_rating_ts"] = max_ts
            log(f"Codeforces({h}) rating 已初始化(最近更新于 "
                f"{datetime.fromtimestamp(max_ts).strftime('%Y-%m-%d %H:%M')})")
            save_state(st, state_path)
            continue

        fresh = sorted((c for c in changes if c["ratingUpdateTimeSeconds"] > acc["last_rating_ts"]),
                       key=lambda c: c["ratingUpdateTimeSeconds"])
        for c in fresh:
            announce_rating(cf_tag(h, multi), c, notifier)
            if c["ratingUpdateTimeSeconds"] > acc["last_rating_ts"]:
                acc["last_rating_ts"] = c["ratingUpdateTimeSeconds"]
        save_state(st, state_path)


def poll_atcoder(handles, st, notifier, cfg, state_path, verbose=False):
    active = False
    multi = len(handles) > 1
    for h in handles:
        if poll_ac_account(h, multi, st, notifier, cfg, state_path, verbose):
            active = True
    return active


def poll_ac_account(handle, multi, st, notifier, cfg, state_path, verbose=False):
    try:
        subs = fetch_atcoder(handle, cfg)
    except Exception as e:
        log_throttled(f"ac_err_{handle}", f"AtCoder 抓取失败({handle}): {e}")
        return False
    if verbose:
        log(f"AtCoder({handle}): 获取到 {len(subs)} 条提交记录")
    if not subs:
        log_throttled(f"ac_empty_{handle}",
                      f"AtCoder: 账号 {handle} 最近 {AC_LOOKBACK // 3600} 小时没有提交记录,"
                      "将保持静默直到检测到其下一次提交(若用户名有误请检查配置)")
        return False

    acc = ensure_account(st, "atcoder", handle)
    tag = ac_tag(handle, multi)
    prune_pending(acc["pending"])

    if acc["last_id"] == 0:
        top = max(subs, key=lambda s: s["id"])
        acc["last_id"] = top["id"]
        if (top.get("result") or "WJ") in AC_PENDING:
            acc["pending"][str(top["id"])] = ac_info(top)
            log(f"检测到进行中的提交 #{top['id']},开始跟踪其评测结果")
        log(f"AtCoder({handle}) 已初始化(最新提交 #{acc['last_id']})")
        save_state(st, state_path)
        return bool(acc["pending"])

    old_last = acc["last_id"]
    fresh = sorted((s for s in subs if s["id"] > old_last), key=lambda s: s["id"])
    acc["last_id"] = max(old_last, max((s["id"] for s in subs), default=old_last))
    by_id = {s["id"]: s for s in subs}

    for s in fresh:
        info = ac_info(s)
        r = info["result"]
        label, emoji, _ = AC_VERDICTS.get(r, (r, "❔", None))
        notifier.send(
            f"[{tag}] 新提交 #{s['id']} · {emoji} {label}",
            f"{info['problem']}\n语言: {info['lang']}",
            "normal", 8000)
        if r not in AC_PENDING:
            announce_ac_result(tag, str(s["id"]), info, notifier, cfg)
        else:
            acc["pending"][str(s["id"])] = info

    for sid in list(acc["pending"]):
        cur = by_id.get(int(sid))
        if cur is None:
            continue
        r = cur.get("result") or "WJ"
        if r not in AC_PENDING:
            info = acc["pending"][sid]
            info.update({"result": r, "execution_time": cur.get("execution_time")})
            announce_ac_result(tag, sid, info, notifier, cfg)
            acc["pending"].pop(sid, None)

    save_state(st, state_path)
    return bool(acc["pending"])


# ---------------------------------------------------------------- 主流程

def main():
    ap = argparse.ArgumentParser(
        description="Codeforces / AtCoder 提交结果桌面通知器")
    ap.add_argument("--config", help="指定配置文件路径")
    ap.add_argument("--state", help="指定状态文件路径(默认 ~/.local/state/cp-notifier/state.json)")
    ap.add_argument("--codeforces-handle", "--cf", dest="cf",
                    help="Codeforces 用户名,多个用逗号分隔(写入配置)")
    ap.add_argument("--atcoder-handle", "--ac", dest="ac",
                    help="AtCoder 用户名,多个用逗号分隔(写入配置)")
    ap.add_argument("--once", action="store_true", help="只检查一轮后退出")
    ap.add_argument("--test", action="store_true", help="发送一条测试通知后退出")
    ap.add_argument("--no-notify", action="store_true", help="不弹通知,只在终端打印")
    ap.add_argument("--verbose", "-v", action="store_true", help="显示每次轮询的详细信息")
    args = ap.parse_args()

    cfg, cfg_path = load_config(args.config)

    if args.cf:
        cfg["codeforces_handles"] = split_handles(args.cf)
    if args.ac:
        cfg["atcoder_handles"] = split_handles(args.ac)
    if args.cf or args.ac:
        save_config(cfg, cfg_path)
        log(f"用户名已写入配置: {cfg_path}")

    notifier = Notifier(enabled=not args.no_notify)

    if args.test:
        notifier.send("✅ 测试通知",
                      "cp-notifier 工作正常\n如果你能看到这条消息,说明通知链路已打通",
                      "normal", 6000)
        return

    cf_handles = cfg["codeforces_handles"]
    ac_handles = cfg["atcoder_handles"]
    if not cf_handles and not ac_handles:
        print(f"尚未配置任何用户名,请编辑 {cfg_path} 填入 codeforces_handles / atcoder_handles(数组),"
              f"\n或使用 --cf/--ac 参数指定(多个用逗号分隔)。", file=sys.stderr)
        sys.exit(1)

    if args.verbose:
        log(f"配置文件: {cfg_path}")
        log(f"轮询间隔: 空闲 {cfg['poll_interval_idle']}s / 评测中 {cfg['poll_interval_active']}s"
            f" · rating 检查 {cfg['rating_check_interval']}s")

    st = load_state(args.state)
    state_path = args.state or STATE_PATH
    mode = "  [单次检查]" if args.once else "  [持续监视,Ctrl+C 退出]"
    log(f"cp-notifier v{VERSION} 启动  "
        f"Codeforces={','.join(cf_handles) or '未配置'}  "
        f"AtCoder={','.join(ac_handles) or '未配置'}{mode}")

    while True:
        active = False
        if cf_handles:
            active = poll_codeforces(cf_handles, st, notifier, cfg, state_path, args.verbose) or active
        if ac_handles:
            active = poll_atcoder(ac_handles, st, notifier, cfg, state_path, args.verbose) or active
        if args.once:
            if not active and not args.verbose:
                log("本轮检查完成,没有新提交或待出结果")
            break
        time.sleep(cfg["poll_interval_active"] if active else cfg["poll_interval_idle"])


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print("\n已退出。")
