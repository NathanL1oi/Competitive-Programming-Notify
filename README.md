# cp-notifier — 算法竞赛提交结果通知器

面向 **Fedora + Niri** 的 Codeforces / AtCoder 提交结果桌面通知工具,Go 实现,编译为**单个静态二进制**。

提交代码之后不必再反复刷新网页:检测到新提交、评测结果出炉时,都会以系统通知弹窗形式送达。

```
📢 [Codeforces] 新提交 #383123456 · ⏳ 评测中
   2004A. Problem Name
   语言: C++17

📢 [Codeforces] #383123456 ❌ Wrong Answer     ← 未通过时 critical 级别,更醒目
   2004A. Problem Name
   语言: C++17
   通过 3 组测试
   62 ms · 256 KB

📢 [Codeforces·小号] Rating 更新 🎉           ← 赛后 rating 变化,多账号时标题带用户名
   Codeforces Round 1000 (Div. 2)
   1473 → 1522 (+49)
   段位: Specialist → Expert
```

## 工作原理

| 平台 | 数据源 | 说明 |
|---|---|---|
| Codeforces | 官方 API `user.status` / `user.rating` | 可选 apiKey+apiSig 签名认证(授权限流更高) |
| AtCoder | 第三方 API `kenkoooo.com/atcoder/atcoder-api/v3` | 官方无公开提交 API;数据有约几秒到十几秒延迟 |

- 空闲时每 **15 秒**轮询一次;有提交在评测中时自动加速到 **3 秒**(可在配置中调整)。
- 通过递增的提交 id 追踪新提交;提交在评测期间每轮检查状态,出结果立即通知。
- **多用户名**:配置数组即可,每个账号独立跟踪,通知标题自动带用户名区分。
- **赛后 rating 通知**:默认每 5 分钟检查一次 `user.rating`,比赛结算后弹窗告知涨跌与段位变化。
- **DankBar 用户信息卡片**:每轮把账号展示快照写入 `status.json`,附带的 DMS/quickshell 插件在状态栏显示 rating,点击弹出用户信息卡片(见下文)。
- 某个提交超过 6 小时仍无结果则自动放弃跟踪(如 CF 判题卡住)。
- 换账号 / 修改用户名时,对应平台的状态自动重置,不会漏报。
- 常驻内存实测约 **12.5 MB**(Python 版约 27 MB)。

## 安装

```bash
# 1. notify-send(Fedora 一般已随 libnotify 安装,可先跳过)
command -v notify-send || sudo dnf install libnotify

# 2. 安装本工具到 ~/.local/bin 并注册 systemd 用户服务(开机自启)
./install.sh
```

> 本机**无需安装通知守护进程**:DMS(Dank Material Shell,quickshell)已通过
> `dms.service` 持有 `org.freedesktop.Notifications` 并提供通知中心,notify-send
> 直接对接它。检查链路:`busctl --user status org.freedesktop.Notifications`。
> 若换到没有 DMS 的机器,才需要 mako/dunst(`sudo dnf install mako`)。

> 仓库内已附带编译好的静态二进制 `cp-notifier`;只有修改源码后才需要 Go 工具链(`go build`,见 `build.sh`)。运行时**不依赖** Go、Python 或任何解释器。

## 配置

编辑 **`~/.config/cp-notifier/config.json`**(安装脚本会把本目录的模板复制过去;程序只认这一个位置):

```json
{
  "codeforces_handles": ["你的CF用户名", "小号或朋友的用户名"],
  "atcoder_handles": ["你的AtCoder用户名"],
  "codeforces_api_key": "",
  "codeforces_api_secret": "",
  "poll_interval_idle": 15,
  "poll_interval_active": 3,
  "rating_check_interval": 300,
  "userinfo_interval": 300,
  "critical_on_fail": true
}
```

| 键 | 说明 |
|---|---|
| `codeforces_handles` / `atcoder_handles` | 用户名**数组**,可多个;旧版单字符串键仍兼容 |
| `codeforces_api_key` / `codeforces_api_secret` | 可选。在 <https://codeforces.com/settings/api> 生成,填上后所有 CF 请求带 apiKey+apiSig 签名,授权限流额度更高。留空则用无认证模式(轮询频率低,通常够用) |
| `poll_interval_idle` / `poll_interval_active` | 空闲 / 有评测中提交时的轮询间隔(秒) |
| `rating_check_interval` | 赛后 rating 检查间隔(秒),`0` 表示关闭 |
| `userinfo_interval` | 用户信息(rating/段位/头像,供状态栏卡片)刷新间隔(秒),`0` 表示关闭 |
| `critical_on_fail` | 未通过时用 critical 级别通知 |

> - AtCoder 用户名**区分大小写**;也可以不编辑文件,直接
>   `cp-notifier --cf 用户名1,用户名2 --ac 用户名` 写入配置(逗号分隔)。
> - 配置文件包含 api secret,程序保存时会自动收紧为 `0600` 权限。
> - **使用 API key 时系统时间必须准确**(签名带时间戳),确保 NTP 已同步:`timedatectl status`。

改完配置后重启服务:

```bash
systemctl --user restart cp-notifier
```

## 验证

```bash
cp-notifier --test                              # 弹一条测试通知
systemctl --user status cp-notifier             # 服务状态
journalctl --user -u cp-notifier -f             # 实时日志
```

## 运行方式

### A. systemd 用户服务(推荐,install.sh 已配好)

开机自动运行,无需开终端。服务单元做了两处与本机通知架构的适配:

1. `Wants=dms.service` + `After=dms.service`:DMS 是 `Type=dbus` 单元,
   `After=` 会等到 `org.freedesktop.Notifications` 服务名被占用后才启动本服务,
   避免开机瞬间的通知被丢弃;
2. `Environment="DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%U/bus"`:
   用户级 systemd 服务默认没有 DBus 会话地址,必须显式指定,
   notify-send 才能连上 DMS 持有的通知服务。

### B. 前台运行(调试用)

```bash
~/.local/bin/cp-notifier            # Ctrl+C 退出
~/.local/bin/cp-notifier --once -v  # 只检查一轮
```

### C. 交给 niri 管理(不用 systemd 时)

本机 DMS 已由 `dms.service` 管理,无需再自启;只需在
`~/.config/niri/config.kdl` 中加入:

```kdl
spawn-at-startup "cp-notifier"
```

(若换用 mako 的机器,则加 `spawn-at-startup "mako"`。)

## 通知样式(DMS)

通知弹窗样式、超时、critical 醒目度等由 DMS 的 quickshell 配置
(`/usr/share/quickshell/dms`,你的 `Mod+N` 通知中心)决定,本工具只按标准
freedesktop 通知协议发送:`-a "CP Notifier"` 应用名、`-u critical` 紧急级别、
`-t` 超时(新提交 8s / 结果 15s)。若需调整样式或想按应用区分,在 DMS 配置中
按 app_name `CP Notifier` 或 urgency 匹配即可。

> 使用 mako 的机器可参考:`~/.config/mako/config` 中
> `font=Noto Sans CJK SC 11`、`default-timeout=8000`、
> `[urgency=critical] border-color=#ff5555`。

## 状态栏用户信息卡片(DMS / quickshell 插件)

仓库自带 DMS(Dank Material Shell)插件 **`cpUserCard`**(`quickshell-plugin/` 目录),
在 DankBar 上显示当前 rating(按段位着色),点击弹出**用户信息卡片**:
头像、用户名、段位、rating / 最高 rating、贡献与粉丝(CF)、最近一场比赛、
最近 5 条提交(判定徽标 + 失败测试点 / 得分详情,点击直达提交页面)、评测中提交数、
主页直达链接。右键 pill 强制刷新。

架构:**守护进程写数据文件,插件只负责读和显示**,不在 QML 里直接调 API
(避免重复限流逻辑,多账号 / API key 签名等能力直接复用):

```
cp-notifier(轮询+通知)
   │  每轮写入(原子替换,不含密钥)
   ▼
~/.local/state/cp-notifier/status.json
   │  FileView 监听变更,自动热更新
   ▼
DankBar pill:  CF 1553        ← 段位色;评测中时显示 ⏳N
点击 → 用户信息卡片(每个账号一张)
```

- `install.sh` 会自动完成:软链 `quickshell-plugin/` → `~/.config/DankMaterialShell/plugins/cpUserCard`、
  启用插件、把 widget 加到 DankBar 中部(天气旁边);DMS 正在运行时通过 `dms ipc` 即时生效。
  跳过此步:`CPN_SKIP_DMS=1 ./install.sh`。
- 手动安装:把 `quickshell-plugin/` 软链或复制到 `~/.config/DankMaterialShell/plugins/cpUserCard`,
  然后在 设置 → 插件 中启用 **CP User Card**,再到 DankBar 布局把 `cpUserCard` 加入想要的分区。
- 插件设置项(设置 → 插件 → CP User Card):status.json 路径覆盖、状态栏显示哪个账号
  (多账号时)、是否显示用户名、是否显示 CF/AC 平台徽标。
- 用户信息每 `userinfo_interval` 秒刷新一次(默认 300,`0` 关闭后卡片只显示提交数据);
  CF 用官方 `user.info` 批量接口,AtCoder 用官方页面 `users/{handle}/history/json`。
- AtCoder 的"最近提交"不受轮询 2 小时回看窗限制:缺失时会从 kenkoooo 按一年窗口
  向后翻页补拉一次全局最新提交,并持久化到 `state.json`(`last_sub` 字段,Python 版会原样保留),
  之后由日常轮询保持新鲜。卡片上每条提交用判定色徽标表示结果(绿色 AC / 红色 WA /
  橙色 TLE / 主题色评测中),辅以判定详情:CF 显示失败测试点(如 "on test 12",
  pretest 阶段为 "on pretest 3");AtCoder 显示得分(AC 时为该题满分,如 "400 分";
  部分给分题未 AC 时显示 "部分分 N")。
  注:AtCoder 官方的逐测试点接口(`submissions/{id}/status/json`)需要登录 cookie,
  无法匿名调用,故进度信息采用 kenkoooo 返回的 `point` 得分。
- 插件目录是通过软链安装的,改 `quickshell-plugin/*.qml` 后在 设置 → 插件 里重载
  (或 `dms ipc call plugins reload cpUserCard`)即可看到效果。
- Python 参考实现(`cp-notifier.py`)不含 status.json 输出,此功能为 Go 版独占。

`status.json` 结构(供其他消费者参考):

```json
{
  "version": 1,
  "updated_at": 1786834851,
  "accounts": [
    {
      "platform": "codeforces", "handle": "Nathan.Li",
      "profile_url": "https://codeforces.com/profile/Nathan.Li",
      "info_ok": true, "rating": 1553, "max_rating": 1751,
      "rank": "specialist", "rank_color": "#03A89E",
      "avatar": "https://userpic.codeforces.org/...",
      "contribution": 0, "friend_of_count": 21, "last_online": 1786834757,
      "last_contest":  { "name": "...", "place": 2, "time": 1784222100 },
      "last_submission": { "id": "385643117", "problem": "2254E. Chronostasis",
                           "lang": "C++23", "verdict": "OK", "label": "Accepted",
                           "emoji": "✅", "time": 1785859167,
                           "short": "AC", "detail": "passed 43 tests",
                           "url": "https://codeforces.com/contest/2254/submission/385643117" },
      "recent_submissions": [
        { "id": "385643117", "problem": "2254E. Chronostasis", "lang": "C++23",
          "verdict": "OK", "label": "Accepted", "emoji": "✅", "time": 1785859167,
          "short": "AC", "detail": "passed 43 tests",
          "url": "https://codeforces.com/contest/2254/submission/385643117" },
        { "id": "385640001", "problem": "2254D. Example", "lang": "C++23",
          "verdict": "WRONG_ANSWER", "label": "Wrong Answer", "emoji": "❌", "time": 1785858000,
          "short": "WA", "detail": "on test 12",
          "url": "https://codeforces.com/contest/2254/submission/385640001" }
      ],
      "pending": 0
    }
  ]
}
```

提交条目字段说明:

| 字段 | 说明 |
|---|---|
| `id` / `problem` / `lang` / `verdict` / `label` / `emoji` / `time` | 既有字段,含义不变 |
| `short` | 短判定码(AC / WA / TLE ...),供徽标显示;CF 由长判定码映射,AC 原样 |
| `detail` | 判定详情,可能缺省:CF 为 "on test N" / "on pretest N" / "passed N tests" / "passed N so far"(评测中);AC 为 "N 分"(AC 满分)或 "部分分 N"(部分给分) |
| `url` | 提交页面链接(CF 自动区分 contest/gym),可能缺省 |
| `recent_submissions` | 最近最多 5 条提交(新→旧),与 `last_submission` 同源;旧消费者可继续只用 `last_submission` |

## 命令行参数

| 参数 | 说明 |
|---|---|
| `--once` | 只检查一轮后退出(测试用) |
| `--test` | 发送一条测试通知后退出 |
| `--no-notify` | 不弹通知,只在终端打印 |
| `--cf NAME` / `--ac NAME` | 指定 Codeforces / AtCoder 用户名并写入配置(多个用逗号分隔) |
| `--config PATH` | 指定配置文件路径 |
| `--state PATH` | 指定状态文件路径(默认 `~/.local/state/cp-notifier/state.json`) |
| `-v` / `--verbose` | 显示每次轮询的详细信息 |

## 常见问题

**1. 终端日志正常但没有任何弹窗**

先确认通知守护进程在线(本机是 DMS):

```bash
busctl --user status org.freedesktop.Notifications
# 本机输出: PID=3503 Comm=qs, Unit=dms.service
systemctl --user status dms.service
```

- 若 `busctl` 报错或没有持有者,说明 DMS 没起来:`systemctl --user restart dms.service`;
- 若持有者是 mako/dunst(其他机器),用 `pgrep -a mako` 检查对应进程;
- 用 `cp-notifier --test` 可以直接看到 notify-send 的报错详情(它失败时日志会提示)。

**2. systemd 服务运行正常但收不到通知**

大概率是 DBus 会话地址问题。若你的 uid 不是 1000,把
`~/.config/systemd/user/cp-notifier.service` 里的 `%U` 改成实际 uid
(如 `unix:path=/run/user/1001/bus`),然后
`systemctl --user daemon-reload && systemctl --user restart cp-notifier`。
服务单元已配置 `After=dms.service`,正常情况下会等 DMS 就绪后才启动。

**3. AtCoder 一直没反应**

- 数据源(kenkoooo)本身有几秒到十几秒延迟,属正常现象;
- 用户名大小写必须与 AtCoder 完全一致;
- 若启动后最近 2 小时没有提交,程序会保持静默(日志每小时提示一次),下一次提交时自动开始跟踪。

**4. 配了 API key 后反而请求失败 / 日志提示"认证请求失败"**

- **系统时间不准确**:签名带时间戳,Codeforces 会校验。先 `timedatectl status` 确认 NTP 已同步;
- key/secret 复制时多带了空白字符:重新粘贴,注意只复制对应字段本身;
- 认证失败时程序会自动回退到无认证请求(每小时提示一次),功能不受影响,修好配置后自动恢复。

**5. 赛后没收到 rating 通知**

- rating 默认每 5 分钟检查一次(`rating_check_interval`),比赛结算本身可能延迟几分钟到几小时;
- 启动时已有的历史 rating 只做静默基线,只通知**启动后新产生**的变化;
- 未参与过 Rated 比赛的账号,日志会每小时提示一次"暂无 Rated 记录";
- 设 `rating_check_interval: 0` 可完全关闭该功能。

**6. 启动 cp-notifier 之前提交的、正在评测中的代码会通知吗?**

会。首次初始化时若发现最新提交仍在评测中,会静默接管并跟踪其最终结果(只是没有"新提交"那条弹窗)。

**7. 卸载**

```bash
systemctl --user disable --now cp-notifier
rm ~/.local/bin/cp-notifier ~/.config/systemd/user/cp-notifier.service
# DMS 插件: 在 设置 → 插件 中禁用 CP User Card,然后
rm ~/.config/DankMaterialShell/plugins/cpUserCard   # 只是软链,不影响仓库
# 可选:rm -r ~/.config/cp-notifier ~/.local/state/cp-notifier
```

## 开发与构建

```bash
./build.sh        # CGO_ENABLED=0 构建静态二进制(约 6.4 MB,strip 后)
go test ./...     # 状态机单元测试(含与 Python 版状态文件兼容性测试)
```

源码结构:`main.go`(参数/主循环)、`poll.go`(两平台轮询与 rating)、`fetch.go`(API 抓取与 CF 签名)、
`notify.go`(notify-send)、`config.go`(配置与状态)、`verdicts.go`(判定表)、
`status.go`(用户信息抓取与 status.json 输出)。

## 文件清单

| 文件 | 说明 |
|---|---|
| `cp-notifier` | 编译好的静态二进制(Go,交付物) |
| `cp-notifier.py` | Python 参考实现(状态/配置文件格式与 Go 版互通,可互换运行;不含 status.json 输出) |
| `*.go` / `go.mod` | Go 源码(`status_test.go` 覆盖用户卡片数据面) |
| `quickshell-plugin/` | DMS 状态栏插件(用户信息卡片),install.sh 软链安装 |
| `config.json` | 配置模板(install.sh 复制到 `~/.config/cp-notifier/`) |
| `cp-notifier.service` | systemd 用户服务单元 |
| `install.sh` | 安装到 `~/.local/bin`、启用服务并安装 DMS 插件(必要时自动重新构建) |
| `build.sh` | 重新构建静态二进制 |
