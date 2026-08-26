package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const appName = "CP Notifier"
const version = "3.1.0"
const userAgent = "cp-notifier/" + version

const (
	// 一个提交超过这个时长仍无结果就放弃跟踪
	pendingTimeout = 6 * time.Hour
	// AtCoder API 回看窗口。kenkoooo 按 epoch_second 升序返回且每请求最多 500 条,
	// 窗口太大会导致活跃用户的新提交被截断,2 小时足够覆盖一次比赛和短暂离线。
	acLookback = 2 * time.Hour
	cfCount    = 20
	// 状态栏卡片展示的最近提交条数(每账号)
	recentSubCap = 5
)

type config struct {
	CodeforcesHandles   []string `json:"codeforces_handles"` // Codeforces 用户名(可多个)
	AtcoderHandles      []string `json:"atcoder_handles"`    // AtCoder 用户名(可多个)
	CodeforcesAPIKey    string   `json:"codeforces_api_key"`
	CodeforcesAPISecret string   `json:"codeforces_api_secret"`
	PollIdle            int      `json:"poll_interval_idle"`    // 空闲轮询间隔(秒)
	PollActive          int      `json:"poll_interval_active"`  // 有提交在评测时的轮询间隔(秒)
	RatingCheckInterval int      `json:"rating_check_interval"` // rating 变化检查间隔(秒),0=关闭
	UserInfoInterval    int      `json:"userinfo_interval"`     // 用户信息(rating/头像等)刷新间隔(秒),0=关闭
	CriticalOnFail      bool     `json:"critical_on_fail"`      // 未通过时用 critical 级别通知
}

func defaultConfig() *config {
	return &config{
		PollIdle:            15,
		PollActive:          3,
		RatingCheckInterval: 300,
		UserInfoInterval:    300,
		CriticalOnFail:      true,
	}
}

func xdgPaths() (string, string) {
	home, _ := os.UserHomeDir()
	cfgBase := os.Getenv("XDG_CONFIG_HOME")
	if cfgBase == "" {
		cfgBase = filepath.Join(home, ".config")
	}
	stateBase := os.Getenv("XDG_STATE_HOME")
	if stateBase == "" {
		stateBase = filepath.Join(home, ".local", "state")
	}
	if fi, err := os.Stat(stateBase); err != nil || !fi.IsDir() {
		if c := os.Getenv("XDG_CACHE_HOME"); c != "" {
			stateBase = c
		} else {
			stateBase = filepath.Join(home, ".cache")
		}
	}
	return filepath.Join(cfgBase, "cp-notifier", "config.json"),
		filepath.Join(stateBase, "cp-notifier", "state.json")
}

// 兼容旧版单用户配置: 若只有 codeforces_handle / atcoder_handle(字符串),
// 归一化为 handles 数组。
type rawConfig struct {
	CodeforcesHandle    string   `json:"codeforces_handle"`
	AtcoderHandle       string   `json:"atcoder_handle"`
	CodeforcesHandles   []string `json:"codeforces_handles"`
	AtcoderHandles      []string `json:"atcoder_handles"`
	CodeforcesAPIKey    string   `json:"codeforces_api_key"`
	CodeforcesAPISecret string   `json:"codeforces_api_secret"`
	PollIdle            int      `json:"poll_interval_idle"`
	PollActive          int      `json:"poll_interval_active"`
	// 指针以区分"未配置"与"显式填 0 关闭"
	RatingCheckInterval *int `json:"rating_check_interval"`
	UserInfoInterval    *int `json:"userinfo_interval"`
	CriticalOnFail      bool `json:"critical_on_fail"`
}

func normalizeConfig(r *rawConfig) *config {
	cfg := defaultConfig()
	if len(r.CodeforcesHandles) > 0 {
		cfg.CodeforcesHandles = r.CodeforcesHandles
	} else if r.CodeforcesHandle != "" {
		cfg.CodeforcesHandles = []string{r.CodeforcesHandle}
	}
	if len(r.AtcoderHandles) > 0 {
		cfg.AtcoderHandles = r.AtcoderHandles
	} else if r.AtcoderHandle != "" {
		cfg.AtcoderHandles = []string{r.AtcoderHandle}
	}
	cfg.CodeforcesAPIKey = r.CodeforcesAPIKey
	cfg.CodeforcesAPISecret = r.CodeforcesAPISecret
	if r.PollIdle > 0 {
		cfg.PollIdle = r.PollIdle
	}
	if r.PollActive > 0 {
		cfg.PollActive = r.PollActive
	}
	if r.RatingCheckInterval != nil {
		cfg.RatingCheckInterval = *r.RatingCheckInterval // 允许 0(关闭)
	}
	if r.UserInfoInterval != nil {
		cfg.UserInfoInterval = *r.UserInfoInterval // 允许 0(关闭)
	}
	cfg.CriticalOnFail = r.CriticalOnFail
	return cfg
}

// 配置来源: --config 参数 > ~/.config/cp-notifier/config.json。
// 显式指定的 --config 不存在时会在该处生成默认配置;
// 否则只在 XDG 位置查找,不存在则生成默认配置(保证前台运行与 systemd 服务用同一份配置)。
func loadConfig(explicit string) (*config, string) {
	cfg := defaultConfig()

	if explicit != "" {
		if _, err := os.Stat(explicit); os.IsNotExist(err) {
			saveConfig(cfg, explicit)
			logf("已生成默认配置: " + explicit)
			return cfg, explicit
		}
		if err := readConfig(explicit, cfg); err != nil {
			logf(fmt.Sprintf("配置文件读取失败(%s): %v", explicit, err))
		}
		return cfg, explicit
	}

	cfgFile, _ := xdgPaths()
	if _, err := os.Stat(cfgFile); err == nil {
		if err := readConfig(cfgFile, cfg); err != nil {
			logf(fmt.Sprintf("配置文件读取失败(%s): %v", cfgFile, err))
		}
	} else {
		saveConfig(cfg, cfgFile)
		logf("已生成默认配置: " + cfgFile)
	}
	return cfg, cfgFile
}

// 读取并归一化(旧版单用户键兼容);JSON 缺失的键保留默认值。
func readConfig(path string, cfg *config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r := &rawConfig{
		PollIdle:            cfg.PollIdle,
		PollActive:          cfg.PollActive,
		RatingCheckInterval: &cfg.RatingCheckInterval,
		UserInfoInterval:    &cfg.UserInfoInterval,
		CriticalOnFail:      cfg.CriticalOnFail,
	}
	if err := json.Unmarshal(data, r); err != nil {
		return err
	}
	*cfg = *normalizeConfig(r)
	return nil
}

// 配置文件可能包含 api secret,权限收紧为 0600
func saveConfig(cfg *config, path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// ---------------------------------------------------------------- 状态

// 正在跟踪的提交信息;与 Python 版 cp-notifier.py 的 state.json 格式完全兼容
type subInfo struct {
	Problem            string  `json:"problem"`
	Lang               string  `json:"lang"`
	Verdict            string  `json:"verdict,omitempty"`
	Result             string  `json:"result,omitempty"`
	PassedTestCount    *int    `json:"passedTestCount,omitempty"`
	TimeConsumedMillis *int64  `json:"timeConsumedMillis,omitempty"`
	MemoryConsumedB    *int64  `json:"memoryConsumedBytes,omitempty"`
	ExecutionTime      *int64  `json:"execution_time,omitempty"`
	SeenAt             float64 `json:"seen_at"`
}

// 每个账号一份跟踪状态;rating 状态只对 Codeforces 有意义
type accountState struct {
	Handle          string             `json:"handle"`
	LastID          int64              `json:"last_id"`
	Pending         map[string]subInfo `json:"pending"`
	LastRatingTS    int64              `json:"last_rating_ts,omitempty"`    // 已见过的最后一条 rating 变化
	LastRatingCheck int64              `json:"last_rating_check,omitempty"` // 上次检查 rating 的时间戳
	LastInfoCheck   int64              `json:"last_info_check,omitempty"`   // 上次刷新用户信息的时间戳
	LastSub         *lastSubRecord     `json:"last_sub,omitempty"`          // 最近一次提交的持久化快照(状态栏卡片数据)
	LastSubs        []*lastSubRecord   `json:"last_subs,omitempty"`         // 最近若干条提交快照(新→旧,最多 recentSubCap 条)

	// 运行时缓存(不写入 state.json;状态栏用户卡片的数据来源)
	info *accountInfo // 用户信息(rating/段位/头像等),由 refreshUserInfos 填充
}

type platformState struct {
	Accounts map[string]*accountState `json:"accounts"`
}

type appState struct {
	Codeforces platformState `json:"codeforces"`
	Atcoder    platformState `json:"atcoder"`
}

func newAppState() *appState {
	return &appState{
		Codeforces: platformState{Accounts: map[string]*accountState{}},
		Atcoder:    platformState{Accounts: map[string]*accountState{}},
	}
}

func loadState(path string) *appState {
	if path == "" {
		_, path = xdgPaths()
	}
	st := newAppState()
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, st); err != nil {
			logf(fmt.Sprintf("状态文件损坏,已重新初始化(%s): %v", path, err))
			st = newAppState()
		}
	}
	// 旧版(单账号)状态文件没有 accounts 键,直接当作全新状态
	if st.Codeforces.Accounts == nil {
		st.Codeforces.Accounts = map[string]*accountState{}
	}
	if st.Atcoder.Accounts == nil {
		st.Atcoder.Accounts = map[string]*accountState{}
	}
	for k, acc := range st.Codeforces.Accounts {
		if acc == nil {
			st.Codeforces.Accounts[k] = &accountState{Handle: k}
		}
		if acc.Pending == nil {
			acc.Pending = map[string]subInfo{}
		}
	}
	for k, acc := range st.Atcoder.Accounts {
		if acc == nil {
			st.Atcoder.Accounts[k] = &accountState{Handle: k}
		}
		if acc.Pending == nil {
			acc.Pending = map[string]subInfo{}
		}
	}
	return st
}

func saveState(st *appState, path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// 为新账号初始化跟踪状态(不删除旧账号,支持多账号并存)。
// 返回 map 中的指针,调用方的修改会直接写入状态。
func ensureAccount(ps *platformState, handle, platform string) *accountState {
	acc, ok := ps.Accounts[handle]
	if !ok {
		acc = &accountState{Handle: handle, Pending: map[string]subInfo{}}
		ps.Accounts[handle] = acc
		logf(fmt.Sprintf("%s: 新增账号 %s", platform, handle))
	}
	return acc
}

func prunePending(pending map[string]subInfo) {
	now := time.Now()
	for sid, info := range pending {
		if now.Sub(time.Unix(int64(info.SeenAt), 0)) > pendingTimeout {
			logf(fmt.Sprintf("放弃等待提交 #%s(超过 %d 小时无结果)", sid, int(pendingTimeout/time.Hour)))
			delete(pending, sid)
		}
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
