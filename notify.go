package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// 相同提示的最小重复间隔,避免网络故障时每轮刷屏
var throttle = map[string]time.Time{}

func logf(msg string) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
}

func logThrottled(key, msg string, interval time.Duration) {
	if time.Since(throttle[key]) >= interval {
		throttle[key] = time.Now()
		logf(msg)
	}
}

type sender interface {
	send(title, body, urgency string, timeoutMs int) bool
}

type notifier struct {
	enabled    bool
	notifySend string
	warned     bool
}

func newNotifier(enabled bool) *notifier {
	n := &notifier{enabled: enabled}
	if p, err := exec.LookPath("notify-send"); err == nil {
		n.notifySend = p
	}
	if enabled && n.notifySend == "" {
		logf("警告: 未找到 notify-send,无法弹窗(sudo dnf install libnotify)")
		n.warned = true
	}
	return n
}

func (n *notifier) send(title, body, urgency string, timeoutMs int) bool {
	logf("📢 " + title)
	for _, line := range strings.Split(body, "\n") {
		if line != "" {
			logf("   " + line)
		}
	}
	if !n.enabled || n.notifySend == "" {
		return false
	}
	args := []string{"-a", appName}
	if urgency == "low" || urgency == "critical" {
		args = append(args, "-u", urgency)
	}
	if timeoutMs > 0 {
		args = append(args, "-t", strconv.Itoa(timeoutMs))
	}
	args = append(args, title, body)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, n.notifySend, args...).CombinedOutput()
	if err != nil {
		if !n.warned {
			logf("警告: 通知发送失败——有正在运行的通知守护进程吗?" +
				"(如 DMS/mako/dunst,可用 `busctl --user status org.freedesktop.Notifications` 检查)")
			if s := strings.TrimSpace(string(out)); s != "" {
				logf("  详情: " + s)
			}
			n.warned = true
		}
		return false
	}
	n.warned = false
	return true
}
