package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func orDash(s string) string {
	if s == "" {
		return "未配置"
	}
	return s
}

// splitHandles 把逗号分隔的用户名拆成数组(去空白、去空项)
func splitHandles(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	var (
		cfgPath   string
		statePath string
		cfFlag    string
		acFlag    string
		once      bool
		test      bool
		noNotify  bool
		verbose   bool
	)
	flag.StringVar(&cfgPath, "config", "", "指定配置文件路径")
	flag.StringVar(&statePath, "state", "", "指定状态文件路径(默认 ~/.local/state/cp-notifier/state.json)")
	flag.StringVar(&cfFlag, "codeforces-handle", "", "Codeforces 用户名,多个用逗号分隔(写入配置)")
	flag.StringVar(&cfFlag, "cf", "", "Codeforces 用户名,多个用逗号分隔(写入配置)")
	flag.StringVar(&acFlag, "atcoder-handle", "", "AtCoder 用户名,多个用逗号分隔(写入配置)")
	flag.StringVar(&acFlag, "ac", "", "AtCoder 用户名,多个用逗号分隔(写入配置)")
	flag.BoolVar(&once, "once", false, "只检查一轮后退出")
	flag.BoolVar(&test, "test", false, "发送一条测试通知后退出")
	flag.BoolVar(&noNotify, "no-notify", false, "不弹通知,只在终端打印")
	flag.BoolVar(&verbose, "verbose", false, "显示每次轮询的详细信息")
	flag.BoolVar(&verbose, "v", false, "显示每次轮询的详细信息")
	flag.Parse()

	cfg, loadedPath := loadConfig(cfgPath)

	// --cf/--ac 支持逗号分隔的多用户名,并覆盖写入配置
	if cfFlag != "" {
		cfg.CodeforcesHandles = splitHandles(cfFlag)
	}
	if acFlag != "" {
		cfg.AtcoderHandles = splitHandles(acFlag)
	}
	if cfFlag != "" || acFlag != "" {
		saveConfig(cfg, loadedPath)
		logf("用户名已写入配置: " + loadedPath)
	}

	n := newNotifier(!noNotify)

	if test {
		n.send("✅ 测试通知",
			"cp-notifier 工作正常\n如果你能看到这条消息,说明通知链路已打通",
			"normal", 6000)
		return
	}

	cfHandles := cfg.CodeforcesHandles
	acHandles := cfg.AtcoderHandles
	if len(cfHandles) == 0 && len(acHandles) == 0 {
		// 正常退出(非 0 会触发 systemd Restart=on-failure 反复重启);
		// 填好用户名后 systemctl --user restart cp-notifier 即可。
		fmt.Fprintf(os.Stderr,
			"尚未配置任何用户名,退出待命。请编辑 %s 填入 codeforces_handles / atcoder_handles(数组),\n或使用 --cf/--ac 参数指定(多个用逗号分隔),然后 systemctl --user restart cp-notifier。\n",
			loadedPath)
		return
	}

	if verbose {
		logf("配置文件: " + loadedPath)
		logf(fmt.Sprintf("轮询间隔: 空闲 %ds / 评测中 %ds · rating 检查 %ds",
			cfg.PollIdle, cfg.PollActive, cfg.RatingCheckInterval))
	}

	st := loadState(statePath)
	sp := statePath
	if sp == "" {
		_, sp = xdgPaths()
	}
	statusPath := statusPathFor(sp)

	mode := "  [持续监视,Ctrl+C 退出]"
	if once {
		mode = "  [单次检查]"
	}
	logf(fmt.Sprintf("cp-notifier v%s 启动  Codeforces=%s  AtCoder=%s%s",
		version, orDash(strings.Join(cfHandles, ",")), orDash(strings.Join(acHandles, ",")), mode))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		active := false
		if len(cfHandles) > 0 {
			active = pollCodeforces(cfHandles, st, n, cfg, sp, verbose) || active
		}
		if len(acHandles) > 0 {
			active = pollAtcoder(acHandles, st, n, cfg, sp, verbose) || active
		}
		// 状态栏用户卡片: 补全 AC 最近提交、节流刷新用户信息,并写出 status.json
		ensureACLastSubs(st, cfg, acHandles, sp)
		refreshUserInfos(st, cfg, cfHandles, acHandles, sp)
		writeStatus(st, cfHandles, acHandles, statusPath)
		if once {
			if !active && !verbose {
				logf("本轮检查完成,没有新提交或待出结果")
			}
			return
		}
		delay := time.Duration(cfg.PollActive) * time.Second
		if !active {
			delay = time.Duration(cfg.PollIdle) * time.Second
		}
		select {
		case <-ctx.Done():
			fmt.Println("\n已退出。")
			return
		case <-time.After(delay):
		}
	}
}
