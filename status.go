package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 本文件实现"用户信息卡片"的数据面:
//   - 定期刷新每个配置账号的用户信息(rating / 段位 / 头像等),
//     CF 用官方 user.info(批量),AtCoder 用官方页面的 history/json;
//   - 每轮轮询把展示快照写入 status.json(与 state.json 同目录),
//     供 DMS(Dank Material Shell / quickshell)状态栏插件读取。
//
// 用户信息只缓存在内存(accountState.info,不写入 state.json),
// 刷新时间戳持久化在 accountState.LastInfoCheck,重启后按间隔正常节流。

// ---------------------------------------------------------------- 数据模型

// accountInfo 是单个账号用户信息的运行时缓存
type accountInfo struct {
	Fetched      bool   // 是否至少成功获取过一次
	Err          string // 最近一次确定性失败的原因(如用户不存在)
	Rating       *int   // nil = 无 Rated 记录
	MaxRating    *int
	Rank         string         // 段位名(CF 官方小写串 / AtCoder 颜色名)
	RankColor    string         // 当前 rating 段位颜色 hex,供 QML 直接着色
	MaxRankColor string         // 最高 rating 段位颜色 hex(按 max 数值独立分段),供 QML 直接着色
	Avatar       string         // 头像 URL(仅 CF 有)
	Contribution *int           // 仅 CF
	FriendOf     *int           // 仅 CF:被多少人加为好友
	LastOnline   int64          // 仅 CF
	LastContest  *statusContest // 最近一场 rated 比赛(可从对应 API 免费获得时填充)
}

// statusSub 是一次提交的展示快照(状态栏卡片上的"最近提交")
type statusSub struct {
	ID      string `json:"id"`
	Problem string `json:"problem"`
	Lang    string `json:"lang"`
	Verdict string `json:"verdict"` // 原始判定码(CF) / Result(AC)
	Label   string `json:"label"`   // 判定说明
	Emoji   string `json:"emoji"`
	Time    int64  `json:"time"` // 提交时间(unix 秒),0 = 未知
	// 以下为增强字段(v3.1 新增;旧版守护进程不写,QML 端有 fallback)
	Short  string `json:"short,omitempty"`  // 短判定码(徽标用): WA / AC / TLE...
	Detail string `json:"detail,omitempty"` // 判定详情: "on test 12" / "on pretest 3" / "400 pts" / "partial 150"
	URL    string `json:"url,omitempty"`    // 提交页面链接(点击卡片打开)
}

type statusContest struct {
	Name  string `json:"name"`
	Place int    `json:"place,omitempty"`
	Time  int64  `json:"time,omitempty"`
	URL   string `json:"url,omitempty"` // 比赛页面链接(v3.2 新增;旧数据没有时 QML 端禁用点击)
}

// statusAccount 是 status.json 中单个账号的完整展示数据
type statusAccount struct {
	Platform       string         `json:"platform"` // codeforces / atcoder
	Handle         string         `json:"handle"`
	ProfileURL     string         `json:"profile_url"`
	InfoOK         bool           `json:"info_ok"` // 用户信息是否可用
	InfoError      string         `json:"info_error,omitempty"`
	Rating         *int           `json:"rating,omitempty"`
	MaxRating      *int           `json:"max_rating,omitempty"`
	Rank           string         `json:"rank,omitempty"`
	RankColor      string         `json:"rank_color,omitempty"`
	MaxRankColor   string         `json:"max_rank_color,omitempty"` // 最高 rating 的分段色(独立于当前 rank_color)
	Avatar         string         `json:"avatar,omitempty"`
	Contribution   *int           `json:"contribution,omitempty"`
	FriendOf       *int           `json:"friend_of_count,omitempty"`
	LastOnline     int64          `json:"last_online,omitempty"`
	LastContest    *statusContest `json:"last_contest,omitempty"`
	LastSubmission *statusSub     `json:"last_submission,omitempty"`
	// 最近若干条提交(新→旧,最多 recentSubCap 条;v3.1 新增)。
	// 与 last_submission 同源,旧消费者可继续只用 last_submission。
	RecentSubmissions []*statusSub `json:"recent_submissions,omitempty"`
	Pending           int          `json:"pending"` // 正在评测中的提交数
}

type statusFile struct {
	Version   int             `json:"version"`
	UpdatedAt int64           `json:"updated_at"`
	Accounts  []statusAccount `json:"accounts"`
}

// statusPathFor 返回与 state.json 同目录的 status.json 路径
func statusPathFor(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "status.json")
}

// ---------------------------------------------------------------- 段位颜色

// cfRankColor 按 CF 官方段位名(小写)给出色值,与网站配色一致
func cfRankColor(rank string) string {
	switch strings.ToLower(rank) {
	case "pupil":
		return "#008000"
	case "specialist":
		return "#03A89E"
	case "expert":
		return "#0000FF"
	case "candidate master":
		return "#AA00AA"
	case "master", "international master":
		return "#FF8C00"
	case "grandmaster", "international grandmaster", "legendary grandmaster":
		return "#FF0000"
	default: // newbie / 未知
		return "#808080"
	}
}

// acRank 按 AtCoder rating 给出段位色名与色值,与网站配色一致
func acRank(r int) (string, string) {
	switch {
	case r < 400:
		return "Gray", "#808080"
	case r < 800:
		return "Brown", "#804000"
	case r < 1200:
		return "Green", "#008000"
	case r < 1600:
		return "Cyan", "#00C0C0"
	case r < 2000:
		return "Blue", "#0000FF"
	case r < 2400:
		return "Yellow", "#C0C000"
	case r < 2800:
		return "Orange", "#FF8000"
	default:
		return "Red", "#FF0000"
	}
}

// cfRatingColor 按 CF rating 数值所在分段给出色值,分段与官方段位分界一致
// (<1200 灰 / <1400 绿 / <1600 青 / <1900 蓝 / <2100 紫 / <2400 橙 / >=2400 红,
// 3000+ 传奇统一按红处理),色值与 cfRankColor 相同。
// 用于最高 rating 等着色:max 段位名未必可得,按数值分段更可靠。
func cfRatingColor(r int) string {
	switch {
	case r < 1200:
		return "#808080"
	case r < 1400:
		return "#008000"
	case r < 1600:
		return "#03A89E"
	case r < 1900:
		return "#0000FF"
	case r < 2100:
		return "#AA00AA"
	case r < 2400:
		return "#FF8C00"
	default:
		return "#FF0000"
	}
}

// acRatingColor 按 AtCoder rating 数值给出色值(acRank 的色值部分)
func acRatingColor(r int) string {
	_, c := acRank(r)
	return c
}

// ratingColor 按平台与 rating 数值给出分段色(当前/最高 rating 各自独立调用)
func ratingColor(platform string, r int) string {
	if platform == "codeforces" {
		return cfRatingColor(r)
	}
	return acRatingColor(r)
}

// ---------------------------------------------------------------- 转换

func cfUserToInfo(u cfUser) *accountInfo {
	info := &accountInfo{
		Fetched:      true,
		Rating:       u.Rating,
		MaxRating:    u.MaxRating,
		Contribution: u.Contribution,
		FriendOf:     u.FriendOfCount,
		LastOnline:   u.LastOnlineTimeSeconds,
	}
	if u.Rank != "" {
		info.Rank = u.Rank
		info.RankColor = cfRankColor(u.Rank)
	} else {
		info.Rank = "Unrated"
		info.RankColor = "#808080"
	}
	if u.MaxRating != nil {
		info.MaxRankColor = cfRatingColor(*u.MaxRating)
	}
	info.Avatar = u.TitlePhoto
	if info.Avatar == "" {
		info.Avatar = u.Avatar
	}
	return info
}

func acHistoryToInfo(hist []acHistoryEntry) *accountInfo {
	info := &accountInfo{Fetched: true, Rank: "Unrated", RankColor: "#808080"}
	if len(hist) == 0 {
		return info
	}
	last := hist[len(hist)-1]
	rating := last.NewRating
	maxRating := 0
	for _, e := range hist {
		if e.NewRating > maxRating {
			maxRating = e.NewRating
		}
	}
	info.Rating = &rating
	info.MaxRating = &maxRating
	info.Rank, info.RankColor = acRank(rating)
	info.MaxRankColor = acRatingColor(maxRating) // 最高 rating 独立按 max 数值分段
	name := last.ContestNameEn
	if name == "" {
		name = last.ContestName
	}
	info.LastContest = &statusContest{
		Name:  name,
		Place: last.Place,
		Time:  parseACTime(last.EndTime),
		URL:   acContestURL(last.ContestScreenName),
	}
	return info
}

func parseACTime(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// lastSubRecord 是持久化到 state.json 的最近提交快照。只存原始数据;
// 展示用的 label/emoji/short/detail 每次由判定表与原始字段现算,
// 避免陈旧文案沉淀在状态文件里。
// Python 版读写 state.json 时会原样保留该字段(纯字典往返)。
type lastSubRecord struct {
	ID      int64  `json:"id"`
	Problem string `json:"problem"`
	Lang    string `json:"lang"`
	Verdict string `json:"verdict"` // CF 判定码 / AC Result
	Time    int64  `json:"time"`    // 提交时间(unix 秒),0 = 未知
	// 以下为增强字段(v3.1 新增;旧状态文件没有这些键,零值即"未知")
	URL     string   `json:"url,omitempty"`     // 提交页面链接
	Passed  *int     `json:"passed,omitempty"`  // CF: 通过的测试数(失败测试号 = passed+1)
	Pretest bool     `json:"pretest,omitempty"` // CF: 评测发生在 PRETESTS 阶段(失败点标注为 pretest)
	Points  *float64 `json:"points,omitempty"`  // AC: 得分(kenkoooo point;部分给分题反映部分通过)
}

// toStatusSub 加上展示字段(label/emoji/short/detail),nil 安全
func (r *lastSubRecord) toStatusSub(platform string) *statusSub {
	if r == nil {
		return nil
	}
	table := acVerdicts
	if platform == "codeforces" {
		table = cfVerdicts
	}
	vi := verdictOf(table, r.Verdict)
	return &statusSub{
		ID:      fmt.Sprintf("%d", r.ID),
		Problem: r.Problem,
		Lang:    r.Lang,
		Verdict: r.Verdict,
		Label:   vi.Label,
		Emoji:   vi.Emoji,
		Time:    r.Time,
		Short:   shortVerdict(platform, r.Verdict),
		Detail:  r.detailText(platform),
		URL:     r.URL,
	}
}

// detailText 从原始字段生成判定详情文字(每次现算,不持久化):
//   - CF 未通过: "on test N"(pretest 阶段为 "on pretest N"),N = 通过数+1
//   - CF Accepted: "passed N tests"(数据可得时)
//   - CF 评测中: "passed N so far"(有进度时)
//   - AC Accepted: "N pts"(即该题满分,展示题目分值)
//   - AC 未通过但有得分(部分给分题): "partial N"
//
// AtCoder 的取舍说明: 官方逐测试点端点
// (atcoder.jp/contests/{c}/submissions/{id}/status/json) 需要登录 cookie
// (匿名访问 302 跳转登录页),不可用;kenkoooo 的 point 是唯一公开可行的
// 进度信号,常规题 WA 时恒为 0(无法显示逐测试点进度),部分给分题才有意义。
func (r *lastSubRecord) detailText(platform string) string {
	if platform == "codeforces" {
		switch {
		case r.Verdict == "OK":
			if r.Passed != nil {
				return fmt.Sprintf("passed %d tests", *r.Passed)
			}
		case r.Verdict == "TESTING":
			if r.Passed != nil && *r.Passed > 0 {
				return fmt.Sprintf("passed %d so far", *r.Passed)
			}
		case cfFailsOnTest(r.Verdict) && r.Passed != nil:
			if r.Pretest {
				return fmt.Sprintf("on pretest %d", *r.Passed+1)
			}
			return fmt.Sprintf("on test %d", *r.Passed+1)
		}
		return ""
	}
	// atcoder
	if r.Points == nil || acIsPending(r.Verdict) {
		return ""
	}
	if r.Verdict == "AC" {
		return formatPoints(*r.Points) + " pts"
	}
	if *r.Points > 0 {
		return "partial " + formatPoints(*r.Points)
	}
	return ""
}

// formatPoints 得分格式化: 整数值不带小数点(kenkoooo 返回的是 float)
func formatPoints(p float64) string {
	if p == float64(int64(p)) {
		return fmt.Sprintf("%d", int64(p))
	}
	return fmt.Sprintf("%.1f", p)
}

// cfSubmissionURL 生成 CF 提交页面链接(gym 题目 contestId >= 100000)
func cfSubmissionURL(contestID, subID int64) string {
	if contestID >= 100000 {
		return fmt.Sprintf("https://codeforces.com/gym/%d/submission/%d", contestID, subID)
	}
	return fmt.Sprintf("https://codeforces.com/contest/%d/submission/%d", contestID, subID)
}

func acSubmissionURL(contestID string, subID int64) string {
	if contestID == "" {
		return ""
	}
	return fmt.Sprintf("https://atcoder.jp/contests/%s/submissions/%d", contestID, subID)
}

// cfContestURL 生成 CF 比赛页面链接(user.rating 只含 rated 常规比赛,无 gym)
func cfContestURL(contestID int64) string {
	if contestID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://codeforces.com/contest/%d", contestID)
}

// acContestURL 生成 AtCoder 比赛页面链接。
// history/json 的 ContestScreenName 是完整虚拟主机名(如 "abc471.contest.atcoder.jp"),
// 比赛 slug 需剥掉 ".contest.atcoder.jp" 后缀;旧式数据本身可能就是 slug,剥不动则原样用。
func acContestURL(screenName string) string {
	slug := strings.TrimSuffix(screenName, ".contest.atcoder.jp")
	if slug == "" {
		return ""
	}
	return "https://atcoder.jp/contests/" + slug
}

func cfToLastSub(s cfSub) *lastSubRecord {
	v := s.Verdict
	if v == "" {
		v = "TESTING"
	}
	return &lastSubRecord{
		ID:      s.ID,
		Problem: fmt.Sprintf("%d%s. %s", s.ContestID, s.Problem.Index, s.Problem.Name),
		Lang:    orDefault(s.ProgrammingLanguage, "?"),
		Verdict: v,
		Time:    s.CreationTimeSeconds,
		URL:     cfSubmissionURL(s.ContestID, s.ID),
		Passed:  s.PassedTestCount,
		Pretest: s.Testset == "PRETESTS",
	}
}

func acToLastSub(s acSub) *lastSubRecord {
	r := s.Result
	if r == "" {
		r = "WJ"
	}
	return &lastSubRecord{
		ID:      s.ID,
		Problem: orDefault(s.ProblemID, "?"),
		Lang:    orDefault(s.Language, "?"),
		Verdict: r,
		Time:    s.EpochSecond,
		URL:     acSubmissionURL(s.ContestID, s.ID),
		Points:  s.Point,
	}
}

// mergeRecentSubs 合并"已记忆的最近提交"与"本轮抓到的最新数据":
// 按提交 id 去重,新数据覆盖旧记录(评测中 → 出结果时判定/得分会刷新),
// 按 id 降序(新→旧)并截断到 cap 条。nil 安全。
func mergeRecentSubs(existing []*lastSubRecord, fresh []*lastSubRecord, cap int) []*lastSubRecord {
	if len(existing) == 0 && len(fresh) == 0 {
		return nil
	}
	byID := make(map[int64]*lastSubRecord, len(existing)+len(fresh))
	for _, r := range existing {
		if r != nil {
			byID[r.ID] = r
		}
	}
	for _, r := range fresh {
		if r != nil {
			byID[r.ID] = r // 新数据覆盖同 id 旧记录
		}
	}
	out := make([]*lastSubRecord, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > cap {
		out = out[:cap]
	}
	return out
}

func profileURL(platform, handle string) string {
	if platform == "codeforces" {
		return "https://codeforces.com/profile/" + handle
	}
	return "https://atcoder.jp/users/" + handle
}

// ---------------------------------------------------------------- 用户信息刷新

// refreshUserInfos 按 userinfo_interval 节流刷新所有配置账号的用户信息。
// CF 一次批量请求;AC 每账号一次请求。瞬时错误保留旧数据,
// 确定性失败(如无此用户)在从未成功过时记入 info.Err。
func refreshUserInfos(st *appState, cfg *config, cfHandles, acHandles []string, statePath string) {
	if cfg.UserInfoInterval <= 0 {
		return
	}
	now := time.Now().Unix()
	ivl := int64(cfg.UserInfoInterval)

	// ---- Codeforces(批量)
	var due []string
	for _, h := range cfHandles {
		acc := ensureAccount(&st.Codeforces, h, "codeforces")
		if acc.info == nil || now-acc.LastInfoCheck >= ivl {
			due = append(due, h)
		}
	}
	if len(due) > 0 {
		users, err := fetchCFUserInfos(due, cfg)
		if err != nil {
			logThrottled("cf_userinfo_err", fmt.Sprintf("Codeforces 用户信息抓取失败: %v", err), time.Hour)
		} else {
			got := map[string]bool{}
			for _, u := range users {
				got[strings.ToLower(u.Handle)] = true
				for _, h := range cfHandles {
					if strings.EqualFold(h, u.Handle) {
						acc := ensureAccount(&st.Codeforces, h, "codeforces")
						prev := acc.info
						acc.info = cfUserToInfo(u)
						if prev != nil && prev.LastContest != nil {
							acc.info.LastContest = prev.LastContest // 由 rating 检查填充,保留
						}
						acc.LastInfoCheck = now
					}
				}
			}
			// 请求成功但某账号没有返回:视为确定性失败(通常是用户名不存在)
			for _, h := range due {
				acc := ensureAccount(&st.Codeforces, h, "codeforces")
				acc.LastInfoCheck = now
				if !got[strings.ToLower(h)] && (acc.info == nil || !acc.info.Fetched) {
					acc.info = &accountInfo{Err: "user not found or no data returned"}
				}
			}
			saveState(st, statePath)
		}
	}

	// ---- AtCoder(逐账号)
	for _, h := range acHandles {
		acc := ensureAccount(&st.Atcoder, h, "atcoder")
		if acc.info != nil && now-acc.LastInfoCheck < ivl {
			continue
		}
		hist, err := fetchACHistory(h)
		if err != nil {
			logThrottled("ac_userinfo_err_"+h, fmt.Sprintf("AtCoder 用户信息抓取失败(%s): %v", h, err), time.Hour)
			acc.LastInfoCheck = now
			if acc.info == nil || !acc.info.Fetched {
				acc.info = &accountInfo{Err: "fetch failed (bad handle or network)"}
			}
			saveState(st, statePath)
			continue
		}
		prev := acc.info
		acc.info = acHistoryToInfo(hist)
		if prev != nil && acc.info != nil && acc.info.LastContest == nil {
			acc.info.LastContest = prev.LastContest // 理论上不会发生,防御一下
		}
		acc.LastInfoCheck = now
		saveState(st, statePath)
	}
}

// ---------------------------------------------------------------- status.json 输出

func buildStatusAccount(platform, handle string, acc *accountState) statusAccount {
	sa := statusAccount{
		Platform:   platform,
		Handle:     handle,
		ProfileURL: profileURL(platform, handle),
	}
	if acc == nil {
		return sa
	}
	sa.Pending = len(acc.Pending)
	sa.LastSubmission = acc.LastSub.toStatusSub(platform)
	for _, r := range acc.LastSubs {
		if s := r.toStatusSub(platform); s != nil {
			sa.RecentSubmissions = append(sa.RecentSubmissions, s)
		}
	}
	if acc.info != nil {
		sa.InfoOK = acc.info.Fetched
		sa.InfoError = acc.info.Err
		sa.Rating = acc.info.Rating
		sa.MaxRating = acc.info.MaxRating
		sa.Rank = acc.info.Rank
		sa.RankColor = acc.info.RankColor
		sa.MaxRankColor = acc.info.MaxRankColor
		sa.Avatar = acc.info.Avatar
		sa.Contribution = acc.info.Contribution
		sa.FriendOf = acc.info.FriendOf
		sa.LastOnline = acc.info.LastOnline
		sa.LastContest = acc.info.LastContest
	}
	return sa
}

// ---------------------------------------------------------------- AtCoder 最近提交引导

// AtCoder 轮询只有 2 小时回看窗:窗口之外拿不到任何提交,卡片上的
// "最近提交"会一直空缺。这里在缺失时从 kenkoooo 补拉一次全局最新提交
// 并持久化,之后由日常轮询保持新鲜。CF 的 user.status 无时间窗,不需要。
var acLastSubBootstrap = map[string]int64{} // handle -> 上次尝试时间戳(仅内存)

func ensureACLastSubs(st *appState, cfg *config, acHandles []string, statePath string) {
	now := time.Now().Unix()
	ivl := int64(cfg.UserInfoInterval)
	if ivl <= 0 {
		ivl = 300
	}
	changed := false
	for _, h := range acHandles {
		acc := ensureAccount(&st.Atcoder, h, "atcoder")
		if acc.LastSub != nil {
			continue // 已有数据,日常轮询自会刷新
		}
		if now-acLastSubBootstrap[h] < ivl {
			continue
		}
		acLastSubBootstrap[h] = now
		sub, err := fetchAtcoderLatest(h)
		if err != nil {
			logThrottled("ac_lastsub_err_"+h, fmt.Sprintf("AtCoder 最近提交补拉失败(%s): %v", h, err), time.Hour)
			continue
		}
		if sub == nil {
			continue // 近一年无提交,保持空缺,按间隔重试
		}
		acc.LastSub = acToLastSub(*sub)
		acc.LastSubs = mergeRecentSubs(acc.LastSubs, []*lastSubRecord{acc.LastSub}, recentSubCap)
		logf(fmt.Sprintf("AtCoder(%s) 已补全最近提交 #%d", h, sub.ID))
		changed = true
	}
	if changed {
		saveState(st, statePath)
	}
}

// writeStatus 把当前所有账号的展示快照原子写入 status.json。
// 不含任何密钥,权限 0644,供同机 quickshell 插件直接读取。
func writeStatus(st *appState, cfHandles, acHandles []string, path string) {
	sf := statusFile{
		Version:   1,
		UpdatedAt: time.Now().Unix(),
		Accounts:  []statusAccount{},
	}
	for _, h := range cfHandles {
		sf.Accounts = append(sf.Accounts, buildStatusAccount("codeforces", h, st.Codeforces.Accounts[h]))
	}
	for _, h := range acHandles {
		sf.Accounts = append(sf.Accounts, buildStatusAccount("atcoder", h, st.Atcoder.Accounts[h]))
	}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}
