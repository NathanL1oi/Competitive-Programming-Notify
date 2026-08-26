package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRankColors(t *testing.T) {
	cf := map[string]string{
		"newbie":                "#808080",
		"pupil":                 "#008000",
		"specialist":            "#03A89E",
		"expert":                "#0000FF",
		"candidate master":      "#AA00AA",
		"master":                "#FF8C00",
		"grandmaster":           "#FF0000",
		"legendary grandmaster": "#FF0000",
	}
	for rank, want := range cf {
		if got := cfRankColor(rank); got != want {
			t.Errorf("cfRankColor(%q) = %s,期望 %s", rank, got, want)
		}
	}
	ac := []struct {
		rating int
		name   string
		color  string
	}{
		{0, "Gray", "#808080"}, {399, "Gray", "#808080"},
		{400, "Brown", "#804000"}, {799, "Brown", "#804000"},
		{800, "Green", "#008000"}, {1200, "Cyan", "#00C0C0"},
		{1600, "Blue", "#0000FF"}, {2000, "Yellow", "#C0C000"},
		{2400, "Orange", "#FF8000"}, {2800, "Red", "#FF0000"}, {4000, "Red", "#FF0000"},
	}
	for _, c := range ac {
		name, color := acRank(c.rating)
		if name != c.name || color != c.color {
			t.Errorf("acRank(%d) = (%s,%s),期望 (%s,%s)", c.rating, name, color, c.name, c.color)
		}
	}
}

// rating 数值分段取色: 当前/最高 rating 各自独立按数值所在段位着色
func TestRatingColorSegments(t *testing.T) {
	cf := []struct {
		rating int
		color  string
	}{
		{0, "#808080"}, {1199, "#808080"}, // <1200 灰
		{1200, "#008000"}, {1399, "#008000"}, // <1400 绿
		{1400, "#03A89E"}, {1599, "#03A89E"}, // <1600 青
		{1600, "#0000FF"}, {1899, "#0000FF"}, // <1900 蓝
		{1900, "#AA00AA"}, {2099, "#AA00AA"}, // <2100 紫
		{2100, "#FF8C00"}, {2399, "#FF8C00"}, // <2400 橙
		{2400, "#FF0000"}, {3000, "#FF0000"}, {4009, "#FF0000"}, // >=2400 红(3000+ 传奇统一红)
	}
	for _, c := range cf {
		if got := cfRatingColor(c.rating); got != c.color {
			t.Errorf("cfRatingColor(%d) = %s,期望 %s", c.rating, got, c.color)
		}
		if got := ratingColor("codeforces", c.rating); got != c.color {
			t.Errorf("ratingColor(codeforces, %d) = %s,期望 %s", c.rating, got, c.color)
		}
	}
	ac := []struct {
		rating int
		color  string
	}{
		{0, "#808080"}, {399, "#808080"}, // <400 灰
		{400, "#804000"}, {799, "#804000"}, // <800 棕
		{800, "#008000"}, {1199, "#008000"}, // <1200 绿
		{1200, "#00C0C0"}, {1599, "#00C0C0"}, // <1600 青
		{1600, "#0000FF"}, {1999, "#0000FF"}, // <2000 蓝
		{2000, "#C0C000"}, {2399, "#C0C000"}, // <2400 黄
		{2400, "#FF8000"}, {2799, "#FF8000"}, // <2800 橙
		{2800, "#FF0000"}, {4000, "#FF0000"}, // >=2800 红
	}
	for _, c := range ac {
		if got := acRatingColor(c.rating); got != c.color {
			t.Errorf("acRatingColor(%d) = %s,期望 %s", c.rating, got, c.color)
		}
		if got := ratingColor("atcoder", c.rating); got != c.color {
			t.Errorf("ratingColor(atcoder, %d) = %s,期望 %s", c.rating, got, c.color)
		}
	}
}

// 当前 rating 与最高 rating 分属不同段位时,两色必须独立
func TestMaxRankColorIndependent(t *testing.T) {
	// CF: 当前 1300(pupil 绿),最高 1650(expert 蓝)
	cfInfo := cfUserToInfo(cfUser{
		Handle: "mid", Rating: iptr(1300), MaxRating: iptr(1650), Rank: "pupil", MaxRank: "expert",
	})
	if cfInfo.RankColor != "#008000" {
		t.Fatalf("当前 rating 色: %s,期望 #008000", cfInfo.RankColor)
	}
	if cfInfo.MaxRankColor != "#0000FF" {
		t.Fatalf("最高 rating 应独立按 1650 取蓝色,得到 %s", cfInfo.MaxRankColor)
	}
	// CF: 无 max 数据时不着色(QML 端回退 rank_color)
	noMax := cfUserToInfo(cfUser{Handle: "x"})
	if noMax.MaxRankColor != "" {
		t.Fatalf("无 MaxRating 时 MaxRankColor 应为空,得到 %s", noMax.MaxRankColor)
	}
	// AC: 当前 1100(绿),最高 1250(青)
	acInfo := acHistoryToInfo([]acHistoryEntry{
		{NewRating: 1250, ContestName: "A", EndTime: "2024-01-01T21:00:00+09:00"},
		{NewRating: 1100, ContestName: "B", EndTime: "2024-06-01T21:00:00+09:00"},
	})
	if acInfo.RankColor != "#008000" || acInfo.MaxRankColor != "#00C0C0" {
		t.Fatalf("AC 当前/最高应独立取色: 当前 %s,最高 %s", acInfo.RankColor, acInfo.MaxRankColor)
	}
}

func TestACHistoryToInfo(t *testing.T) {
	info := acHistoryToInfo(nil)
	if !info.Fetched || info.Rating != nil || info.Rank != "Unrated" {
		t.Fatalf("空历史应为 Unrated: %+v", info)
	}
	if info.MaxRankColor != "" {
		t.Fatalf("空历史不应有最高段位色: %s", info.MaxRankColor)
	}
	hist := []acHistoryEntry{
		{Place: 100, NewRating: 500, ContestName: "比赛A", EndTime: "2024-01-01T21:00:00+09:00"},
		{Place: 3, NewRating: 1250, ContestNameEn: "Grand Contest", ContestName: "比赛B", EndTime: "2024-06-01T21:00:00+09:00"},
		{Place: 50, NewRating: 1100, ContestName: "比赛C", ContestScreenName: "abc999.contest.atcoder.jp", EndTime: "2024-07-01T21:00:00+09:00"},
	}
	info = acHistoryToInfo(hist)
	if info.Rating == nil || *info.Rating != 1100 {
		t.Fatalf("当前 rating 应取最后一条: %+v", info.Rating)
	}
	if info.MaxRating == nil || *info.MaxRating != 1250 {
		t.Fatalf("最高 rating: %+v", info.MaxRating)
	}
	if info.Rank != "Green" || info.RankColor != "#008000" {
		t.Fatalf("段位: %s %s", info.Rank, info.RankColor)
	}
	if info.MaxRankColor != "#00C0C0" {
		t.Fatalf("最高 rating 1250 应独立取青色,得到 %s", info.MaxRankColor)
	}
	if info.LastContest == nil || info.LastContest.Name != "比赛C" || info.LastContest.Place != 50 {
		t.Fatalf("最近比赛: %+v", info.LastContest)
	}
	if info.LastContest.URL != "https://atcoder.jp/contests/abc999" {
		t.Fatalf("最近比赛链接: %q", info.LastContest.URL)
	}
	if info.LastContest.Time != time.Date(2024, 7, 1, 21, 0, 0, 0, time.FixedZone("JST", 9*3600)).Unix() {
		t.Fatalf("比赛时间解析: %d", info.LastContest.Time)
	}
}

func TestRefreshUserInfosCF(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	sp := t.TempDir() + "/state.json"
	orig := fetchCFUserInfos
	defer func() { fetchCFUserInfos = orig }()

	calls := 0
	fetchCFUserInfos = func(handles []string, _ *config) ([]cfUser, error) {
		calls++
		if len(handles) != 2 {
			t.Fatalf("应批量请求 2 个账号,实际 %v", handles)
		}
		return []cfUser{
			{Handle: "Tourist", Rating: iptr(3530), MaxRating: iptr(4009), Rank: "legendary grandmaster",
				Contribution: iptr(109), FriendOfCount: iptr(90319), TitlePhoto: "https://img/t.jpg"},
			// nobody 不返回 -> 用户不存在
		}, nil
	}
	refreshUserInfos(st, cfg, []string{"tourist", "nobody"}, nil, sp)

	acc := st.Codeforces.Accounts["tourist"]
	if acc == nil || acc.info == nil || !acc.info.Fetched {
		t.Fatal("tourist 用户信息未填充")
	}
	if *acc.info.Rating != 3530 || acc.info.RankColor != "#FF0000" || acc.info.Avatar != "https://img/t.jpg" {
		t.Fatalf("用户信息内容: %+v", acc.info)
	}
	if acc.info.MaxRankColor != "#FF0000" {
		t.Fatalf("最高 rating 4009 应独立取红色,得到 %q", acc.info.MaxRankColor)
	}
	if bad := st.Codeforces.Accounts["nobody"].info; bad == nil || bad.Fetched || bad.Err == "" {
		t.Fatalf("不存在的用户应记录错误: %+v", bad)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}

	// 间隔未到 -> 不再请求
	refreshUserInfos(st, cfg, []string{"tourist", "nobody"}, nil, sp)
	if calls != 1 {
		t.Fatalf("节流失效,calls = %d", calls)
	}

	// 时间回拨到过期 -> 重新请求
	for _, h := range []string{"tourist", "nobody"} {
		st.Codeforces.Accounts[h].LastInfoCheck = time.Now().Unix() - int64(cfg.UserInfoInterval) - 1
	}
	refreshUserInfos(st, cfg, []string{"tourist", "nobody"}, nil, sp)
	if calls != 2 {
		t.Fatalf("过期后应重新请求,calls = %d", calls)
	}
}

func TestRefreshUserInfosACErrorKeepsOld(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	sp := t.TempDir() + "/state.json"
	orig := fetchACHistory
	defer func() { fetchACHistory = orig }()

	fetchACHistory = func(string) ([]acHistoryEntry, error) {
		return []acHistoryEntry{{Place: 1, NewRating: 900, ContestName: "X", EndTime: "2024-01-01T21:00:00+09:00"}}, nil
	}
	refreshUserInfos(st, cfg, nil, []string{"chokudai"}, sp)
	acc := st.Atcoder.Accounts["chokudai"]
	if acc.info == nil || !acc.info.Fetched || *acc.info.Rating != 900 {
		t.Fatalf("首次抓取失败: %+v", acc.info)
	}

	// 过期后抓取出错: 旧数据保留,不被错误覆盖
	fetchACHistory = func(string) ([]acHistoryEntry, error) { return nil, errors.New("boom") }
	acc.LastInfoCheck = 0
	refreshUserInfos(st, cfg, nil, []string{"chokudai"}, sp)
	if acc.info == nil || !acc.info.Fetched || *acc.info.Rating != 900 {
		t.Fatalf("瞬时错误不应清空旧数据: %+v", acc.info)
	}

	// 从未成功过的账号才记录错误
	st2 := newAppState()
	refreshUserInfos(st2, cfg, nil, []string{"ghost"}, sp)
	if info := st2.Atcoder.Accounts["ghost"].info; info == nil || info.Fetched || info.Err == "" {
		t.Fatalf("应记录错误信息: %+v", info)
	}
}

func TestStatusSubSnapshots(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	origCF, origAC := fetchCodeforces, fetchAtcoder
	defer func() { fetchCodeforces, fetchAtcoder = origCF, origAC }()

	s := mkCF(200, "TESTING", nil, nil, nil)
	s.CreationTimeSeconds = 1720000000
	fetchCodeforces = func(string, *config) ([]cfSub, error) { return []cfSub{s}, nil }
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	ls := st.Codeforces.Accounts["tourist"].LastSub
	if ls == nil || ls.ID != 200 || ls.Verdict != "TESTING" || ls.Time != 1720000000 {
		t.Fatalf("CF 最近提交快照: %+v", ls)
	}
	if !strings.Contains(ls.Problem, "1234A") {
		t.Fatalf("题号格式: %s", ls.Problem)
	}
	if disp := ls.toStatusSub("codeforces"); disp.Emoji != "⏳" || disp.ID != "200" {
		t.Fatalf("展示转换: %+v", disp)
	}

	// 评测结束后快照跟着刷新
	s.Verdict = "OK"
	fetchCodeforces = func(string, *config) ([]cfSub, error) { return []cfSub{s}, nil }
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if ls = st.Codeforces.Accounts["tourist"].LastSub; ls.Verdict != "OK" ||
		ls.toStatusSub("codeforces").Label != "Accepted" {
		t.Fatalf("快照应刷新判定: %+v", ls)
	}

	fetchAtcoder = func(string, *config) ([]acSub, error) {
		return []acSub{
			{ID: 9, ProblemID: "abc300_a", Language: "C++", Result: "AC", EpochSecond: 1719000000},
			{ID: 11, ProblemID: "abc310_b", Language: "PyPy3", Result: "WA", EpochSecond: 1719999999},
		}, nil
	}
	pollAtcoder([]string{"chokudai"}, st, n, cfg, sp, false)
	ls = st.Atcoder.Accounts["chokudai"].LastSub
	if ls == nil || ls.ID != 11 || ls.Problem != "abc310_b" || ls.Time != 1719999999 ||
		ls.toStatusSub("atcoder").Label != "Wrong Answer" {
		t.Fatalf("AC 最近提交快照: %+v", ls)
	}

	// 持久化: 重载 state.json 后快照仍在
	st2 := loadState(sp)
	if ls2 := st2.Atcoder.Accounts["chokudai"].LastSub; ls2 == nil || ls2.ID != 11 {
		t.Fatalf("last_sub 应持久化到 state.json: %+v", ls2)
	}
}

func TestEnsureACLastSubs(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	sp := t.TempDir() + "/state.json"
	orig := fetchAtcoderLatest
	defer func() { fetchAtcoderLatest = orig }()
	origBoot := acLastSubBootstrap
	acLastSubBootstrap = map[string]int64{}
	defer func() { acLastSubBootstrap = origBoot }()

	calls := 0
	fetchAtcoderLatest = func(h string) (*acSub, error) {
		calls++
		return &acSub{ID: 47221322, ProblemID: "abc327_a", Language: "C++ 20", Result: "WA", EpochSecond: 1699099720}, nil
	}
	ensureACLastSubs(st, cfg, []string{"NathanL1"}, sp)
	acc := st.Atcoder.Accounts["NathanL1"]
	if acc.LastSub == nil || acc.LastSub.ID != 47221322 || acc.LastSub.Verdict != "WA" {
		t.Fatalf("引导补拉失败: %+v", acc.LastSub)
	}
	if disp := acc.LastSub.toStatusSub("atcoder"); disp.Label != "Wrong Answer" || disp.Emoji != "❌" {
		t.Fatalf("展示转换: %+v", disp)
	}

	// 已有数据后不再请求
	ensureACLastSubs(st, cfg, []string{"NathanL1"}, sp)
	if calls != 1 {
		t.Fatalf("已有 LastSub 不应再请求,calls = %d", calls)
	}

	// 补拉返回空(近一年无提交): 保持空缺
	fetchAtcoderLatest = func(string) (*acSub, error) { calls++; return nil, nil }
	ensureACLastSubs(st, cfg, []string{"emptyuser"}, sp)
	if st.Atcoder.Accounts["emptyuser"].LastSub != nil {
		t.Fatal("空结果不应写入 LastSub")
	}

	// 补拉报错: 也保持空缺
	fetchAtcoderLatest = func(string) (*acSub, error) { calls++; return nil, errors.New("boom") }
	ensureACLastSubs(st, cfg, []string{"erruser"}, sp)
	if st.Atcoder.Accounts["erruser"].LastSub != nil {
		t.Fatal("错误不应写入 LastSub")
	}
}

func TestWriteStatus(t *testing.T) {
	st := newAppState()
	sp := t.TempDir() + "/state.json"
	statusPath := statusPathFor(sp)
	if !strings.HasSuffix(statusPath, "/status.json") {
		t.Fatalf("status 路径: %s", statusPath)
	}

	cfAcc := ensureAccount(&st.Codeforces, "tourist", "codeforces")
	cfAcc.info = &accountInfo{
		Fetched: true, Rating: iptr(3530), MaxRating: iptr(4009),
		Rank: "legendary grandmaster", RankColor: "#FF0000", MaxRankColor: "#FF0000",
		Avatar: "https://img/t.jpg", Contribution: iptr(109), FriendOf: iptr(90319),
		LastContest: &statusContest{Name: "CF Round 1000", Place: 1, Time: 1720000000, URL: "https://codeforces.com/contest/1000"},
	}
	cfAcc.LastSub = &lastSubRecord{ID: 100, Problem: "1234A. Test", Lang: "C++17", Verdict: "OK", Time: 1720000100}
	cfAcc.Pending["101"] = subInfo{Problem: "x"}
	// atcoder 账号只有配置、还没有任何状态(模拟从未抓到提交)
	writeStatus(st, []string{"tourist"}, []string{"chokudai"}, statusPath)

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("status.json 未生成: %v", err)
	}
	var sf statusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("status.json 解析失败: %v", err)
	}
	if sf.Version != 1 || sf.UpdatedAt == 0 || len(sf.Accounts) != 2 {
		t.Fatalf("文件头: %+v", sf)
	}
	a := sf.Accounts[0]
	if a.Platform != "codeforces" || a.Handle != "tourist" || !a.InfoOK ||
		a.Rating == nil || *a.Rating != 3530 || a.RankColor != "#FF0000" ||
		a.MaxRankColor != "#FF0000" ||
		a.LastContest == nil || a.LastContest.Place != 1 ||
		a.LastContest.URL != "https://codeforces.com/contest/1000" ||
		a.LastSubmission == nil || a.LastSubmission.Verdict != "OK" || a.Pending != 1 ||
		a.ProfileURL != "https://codeforces.com/profile/tourist" {
		t.Fatalf("CF 账号条目: %+v", a)
	}
	b := sf.Accounts[1]
	if b.Platform != "atcoder" || b.Handle != "chokudai" || b.InfoOK ||
		b.Rating != nil || b.LastSubmission != nil ||
		b.ProfileURL != "https://atcoder.jp/users/chokudai" {
		t.Fatalf("AC 账号条目: %+v", b)
	}
	// 不含密钥字段
	if strings.Contains(string(data), "api") || strings.Contains(string(data), "secret") {
		t.Fatal("status.json 不应包含任何认证信息")
	}
}

// ---------------------------------------------------------------- v3.1: 判定详情 / 短码 / 链接 / 最近提交列表

func fptr(f float64) *float64 { return &f }

func TestShortVerdict(t *testing.T) {
	cf := map[string]string{
		"OK": "AC", "WRONG_ANSWER": "WA", "TIME_LIMIT_EXCEEDED": "TLE",
		"MEMORY_LIMIT_EXCEEDED": "MLE", "RUNTIME_ERROR": "RE",
		"COMPILATION_ERROR": "CE", "TESTING": "TEST", "CHALLENGED": "HACK",
	}
	for v, want := range cf {
		if got := shortVerdict("codeforces", v); got != want {
			t.Errorf("shortVerdict(cf, %q) = %q,期望 %q", v, got, want)
		}
	}
	// CF 未知判定回退为原始码;AC 的 Result 本身已是短码,原样返回
	if got := shortVerdict("codeforces", "SOMETHING_NEW"); got != "SOMETHING_NEW" {
		t.Errorf("CF 未知判定应回退原码,得到 %q", got)
	}
	if got := shortVerdict("atcoder", "WA"); got != "WA" {
		t.Errorf("AC 短码应原样返回,得到 %q", got)
	}
}

func TestDetailTextCF(t *testing.T) {
	cases := []struct {
		name string
		rec  lastSubRecord
		want string
	}{
		{"AC 带通过数", lastSubRecord{Verdict: "OK", Passed: iptr(42)}, "passed 42 tests"},
		{"AC 无数据", lastSubRecord{Verdict: "OK"}, ""},
		{"WA 失败测试号 = 通过数+1", lastSubRecord{Verdict: "WRONG_ANSWER", Passed: iptr(11)}, "on test 12"},
		{"WA 全灭挂在第 1 个测试", lastSubRecord{Verdict: "WRONG_ANSWER", Passed: iptr(0)}, "on test 1"},
		{"WA pretest 阶段", lastSubRecord{Verdict: "WRONG_ANSWER", Passed: iptr(2), Pretest: true}, "on pretest 3"},
		{"TLE 也定位失败点", lastSubRecord{Verdict: "TIME_LIMIT_EXCEEDED", Passed: iptr(7)}, "on test 8"},
		{"RE 也定位失败点", lastSubRecord{Verdict: "RUNTIME_ERROR", Passed: iptr(1)}, "on test 2"},
		{"CE 无测试点概念", lastSubRecord{Verdict: "COMPILATION_ERROR", Passed: iptr(0)}, ""},
		{"PARTIAL 不算挂在某测试", lastSubRecord{Verdict: "PARTIAL", Passed: iptr(5)}, ""},
		{"HACKED 不适用", lastSubRecord{Verdict: "CHALLENGED", Passed: iptr(30)}, ""},
		{"评测中显示进度", lastSubRecord{Verdict: "TESTING", Passed: iptr(5)}, "passed 5 so far"},
		{"评测刚开始无进度", lastSubRecord{Verdict: "TESTING", Passed: iptr(0)}, ""},
		{"WA 无通过数数据", lastSubRecord{Verdict: "WRONG_ANSWER"}, ""},
	}
	for _, c := range cases {
		if got := c.rec.detailText("codeforces"); got != c.want {
			t.Errorf("%s: detail = %q,期望 %q", c.name, got, c.want)
		}
	}
}

func TestDetailTextAC(t *testing.T) {
	cases := []struct {
		name string
		rec  lastSubRecord
		want string
	}{
		{"AC 显示满分(题目分值)", lastSubRecord{Verdict: "AC", Points: fptr(400)}, "400 pts"},
		{"WA 常规题 0 分不显示", lastSubRecord{Verdict: "WA", Points: fptr(0)}, ""},
		{"WA 部分给分题显示部分分", lastSubRecord{Verdict: "WA", Points: fptr(150)}, "partial 150"},
		{"TLE 部分分", lastSubRecord{Verdict: "TLE", Points: fptr(99.5)}, "partial 99.5"},
		{"评测中不显示", lastSubRecord{Verdict: "WJ", Points: fptr(0)}, ""},
		{"无得分数据", lastSubRecord{Verdict: "AC"}, ""},
		{"小数满分", lastSubRecord{Verdict: "AC", Points: fptr(333.5)}, "333.5 pts"},
	}
	for _, c := range cases {
		if got := c.rec.detailText("atcoder"); got != c.want {
			t.Errorf("%s: detail = %q,期望 %q", c.name, got, c.want)
		}
	}
}

func TestSubmissionURLs(t *testing.T) {
	cf := cfSub{ID: 385643117, ContestID: 2254, Verdict: "OK"}
	if got := cfToLastSub(cf).URL; got != "https://codeforces.com/contest/2254/submission/385643117" {
		t.Fatalf("CF 比赛提交链接: %s", got)
	}
	gym := cfSub{ID: 999, ContestID: 102890, Verdict: "OK"}
	if got := cfToLastSub(gym).URL; got != "https://codeforces.com/gym/102890/submission/999" {
		t.Fatalf("CF gym 提交链接: %s", got)
	}
	ac := acSub{ID: 47221322, ContestID: "abc327", ProblemID: "abc327_a", Result: "AC"}
	if got := acToLastSub(ac).URL; got != "https://atcoder.jp/contests/abc327/submissions/47221322" {
		t.Fatalf("AC 提交链接: %s", got)
	}
	noContest := acSub{ID: 1, ProblemID: "x", Result: "AC"}
	if got := acToLastSub(noContest).URL; got != "" {
		t.Fatalf("缺 contest_id 时链接应为空,得到 %q", got)
	}
}

// 比赛页面链接: CF 用 contestId,AC 用 ContestScreenName;缺失时为空串(QML 禁用点击)
func TestContestURLs(t *testing.T) {
	if got := cfContestURL(2254); got != "https://codeforces.com/contest/2254" {
		t.Fatalf("CF 比赛链接: %s", got)
	}
	if got := cfContestURL(0); got != "" {
		t.Fatalf("无 contestId 时链接应为空,得到 %q", got)
	}
	if got := acContestURL("abc327.contest.atcoder.jp"); got != "https://atcoder.jp/contests/abc327" {
		t.Fatalf("AC 比赛链接(虚拟主机名应剥后缀): %s", got)
	}
	if got := acContestURL("ahc050"); got != "https://atcoder.jp/contests/ahc050" {
		t.Fatalf("AC 比赛链接(已是 slug 应原样): %s", got)
	}
	if got := acContestURL(""); got != "" {
		t.Fatalf("无 screen name 时链接应为空,得到 %q", got)
	}
}

func TestMergeRecentSubs(t *testing.T) {
	mk := func(id int64, verdict string) *lastSubRecord {
		return &lastSubRecord{ID: id, Problem: "p", Lang: "C++", Verdict: verdict}
	}
	// 空 + 空
	if got := mergeRecentSubs(nil, nil, 5); got != nil {
		t.Fatalf("空合并应为 nil: %+v", got)
	}
	// 合并、去重、新数据覆盖旧记录(评测中 -> 出结果)、按 id 降序
	existing := []*lastSubRecord{mk(3, "TESTING"), mk(1, "OK")}
	fresh := []*lastSubRecord{mk(3, "WRONG_ANSWER"), mk(2, "OK")}
	got := mergeRecentSubs(existing, fresh, 5)
	if len(got) != 3 || got[0].ID != 3 || got[1].ID != 2 || got[2].ID != 1 {
		t.Fatalf("合并排序错误: %+v", got)
	}
	if got[0].Verdict != "WRONG_ANSWER" {
		t.Fatalf("新数据应覆盖旧记录: %+v", got[0])
	}
	// 截断到 cap
	many := []*lastSubRecord{mk(10, "OK"), mk(9, "OK"), mk(8, "OK"), mk(7, "OK"), mk(6, "OK"), mk(5, "OK")}
	got = mergeRecentSubs(nil, many, recentSubCap)
	if len(got) != recentSubCap || got[len(got)-1].ID != 6 {
		t.Fatalf("应截断到 %d 条且保留最新: %+v", recentSubCap, got)
	}
}

// 轮询维护最近提交列表,writeStatus 输出 recent_submissions(含详情/短码/链接)
func TestRecentSubsInStatus(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	origCF := fetchCodeforces
	defer func() { fetchCodeforces = origCF }()

	// 7 条提交(id 升序),超过 recentSubCap
	mkSub := func(id int64, verdict string, passed *int) cfSub {
		s := mkCF(id, verdict, passed, nil, nil)
		s.CreationTimeSeconds = 1720000000 + id
		return s
	}
	fetchCodeforces = func(string, *config) ([]cfSub, error) {
		return []cfSub{
			mkSub(7, "TESTING", iptr(4)),
			mkSub(6, "WRONG_ANSWER", iptr(11)),
			mkSub(5, "OK", iptr(30)),
			mkSub(4, "TIME_LIMIT_EXCEEDED", iptr(7)),
			mkSub(3, "OK", iptr(20)),
			mkSub(2, "OK", iptr(15)),
			mkSub(1, "COMPILATION_ERROR", nil),
		}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	acc := st.Codeforces.Accounts["tourist"]
	if len(acc.LastSubs) != recentSubCap {
		t.Fatalf("应保留最近 %d 条,实际 %d", recentSubCap, len(acc.LastSubs))
	}
	if acc.LastSubs[0].ID != 7 || acc.LastSubs[recentSubCap-1].ID != 3 {
		t.Fatalf("列表应新→旧: %+v", acc.LastSubs)
	}

	// 评测中的 #7 出结果(WA on test 5): 重抓后列表内同 id 记录被刷新
	fetchCodeforces = func(string, *config) ([]cfSub, error) {
		s := mkSub(7, "WRONG_ANSWER", iptr(4))
		return []cfSub{s}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if acc.LastSubs[0].Verdict != "WRONG_ANSWER" {
		t.Fatalf("判定应刷新为 WA: %+v", acc.LastSubs[0])
	}

	// 持久化: 重载 state.json 后列表仍在
	st2 := loadState(sp)
	if ls := st2.Codeforces.Accounts["tourist"].LastSubs; len(ls) != recentSubCap {
		t.Fatalf("last_subs 应持久化: %+v", ls)
	}

	// status.json: recent_submissions 带详情/短码/链接,last_submission 保持兼容
	writeStatus(st2, []string{"tourist"}, nil, statusPathFor(sp))
	data, err := os.ReadFile(statusPathFor(sp))
	if err != nil {
		t.Fatal(err)
	}
	var sf statusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatal(err)
	}
	a := sf.Accounts[0]
	if len(a.RecentSubmissions) != recentSubCap {
		t.Fatalf("recent_submissions 条数: %d", len(a.RecentSubmissions))
	}
	top := a.RecentSubmissions[0]
	if top.Short != "WA" || top.Detail != "on test 5" ||
		top.URL != "https://codeforces.com/contest/1234/submission/7" {
		t.Fatalf("首条快照: %+v", top)
	}
	if a.LastSubmission == nil || a.LastSubmission.ID != top.ID {
		t.Fatalf("last_submission 应与列表首条一致: %+v", a.LastSubmission)
	}
	// 旧字段仍在
	if top.Label != "Wrong Answer" || top.Emoji != "❌" || top.Verdict != "WRONG_ANSWER" {
		t.Fatalf("旧字段应保留: %+v", top)
	}
}

// AC: point 得分进入快照与 status.json
func TestACPointsInStatus(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	origAC := fetchAtcoder
	defer func() { fetchAtcoder = origAC }()

	fetchAtcoder = func(string, *config) ([]acSub, error) {
		return []acSub{
			{ID: 9, ContestID: "abc400", ProblemID: "abc400_a", Language: "C++ 23", Result: "WA", Point: fptr(0), EpochSecond: 1719999990},
			{ID: 11, ContestID: "abc400", ProblemID: "abc400_b", Language: "C++ 23", Result: "AC", Point: fptr(300), EpochSecond: 1719999999},
		}, nil
	}
	pollAtcoder([]string{"chokudai"}, st, n, cfg, sp, false)
	acc := st.Atcoder.Accounts["chokudai"]
	if len(acc.LastSubs) != 2 {
		t.Fatalf("应记录 2 条最近提交: %+v", acc.LastSubs)
	}

	writeStatus(st, nil, []string{"chokudai"}, statusPathFor(sp))
	data, _ := os.ReadFile(statusPathFor(sp))
	var sf statusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatal(err)
	}
	subs := sf.Accounts[0].RecentSubmissions
	if len(subs) != 2 || subs[0].ID != "11" {
		t.Fatalf("recent_submissions: %+v", subs)
	}
	if subs[0].Detail != "300 pts" || subs[0].Short != "AC" ||
		subs[0].URL != "https://atcoder.jp/contests/abc400/submissions/11" {
		t.Fatalf("AC 快照: %+v", subs[0])
	}
	if subs[1].Detail != "" {
		t.Fatalf("0 分 WA 不应有详情: %+v", subs[1])
	}
}
