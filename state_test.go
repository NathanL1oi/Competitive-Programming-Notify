package main

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

type fakeMsg struct{ title, body, urgency string }

type fakeSender struct{ msgs []fakeMsg }

func (f *fakeSender) send(title, body, urgency string, timeoutMs int) bool {
	f.msgs = append(f.msgs, fakeMsg{title, body, urgency})
	return true
}

func iptr(i int) *int     { return &i }
func lptr(i int64) *int64 { return &i }

func mkCF(id int64, verdict string, pts *int, ms *int64, kb *int64) cfSub {
	s := cfSub{ID: id, ContestID: 1234, Verdict: verdict, ProgrammingLanguage: "C++17"}
	s.Problem.Index, s.Problem.Name = "A", "Test Problem"
	s.PassedTestCount, s.TimeConsumedMillis, s.MemoryConsumedBytes = pts, ms, kb
	return s
}

// 签名算法与 Codeforces 官方规则的一致性(测试向量由独立 Python 实现算出)
func TestCFSignature(t *testing.T) {
	base := url.Values{"handle": {"tourist"}, "from": {"1"}, "count": {"20"}}
	signed := cfSignedParams("user.status", base, "key123", "deadbeef", 1234567890, "abc123")
	if got := signed.Get("apiSig"); got != "abc123878cbd" {
		t.Fatalf("user.status 签名不符,得到 %s", got)
	}
	base2 := url.Values{"handle": {"tourist"}}
	signed2 := cfSignedParams("user.rating", base2, "key123", "deadbeef", 1234567890, "abc123")
	if got := signed2.Get("apiSig"); got != "abc1239e3ba3" {
		t.Fatalf("user.rating 签名不符,得到 %s", got)
	}
	// 原参数不能被修改
	if base.Get("apiKey") != "" {
		t.Fatal("cfSignedParams 不应修改传入参数")
	}
}

func TestCodeforcesFlow(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	orig := fetchCodeforces
	defer func() { fetchCodeforces = orig }()

	// 启动时已有提交在评测中 -> 静默跟踪,结果出来后通知
	fetchCodeforces = func(string, *config) ([]cfSub, error) { return []cfSub{mkCF(100, "TESTING", nil, nil, nil)}, nil }
	if active := pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false); !active {
		t.Fatal("启动时应跟踪进行中的提交")
	}
	if len(n.msgs) != 0 {
		t.Fatalf("初始化必须静默,实际 %v", n.msgs)
	}

	fetchCodeforces = func(string, *config) ([]cfSub, error) { return []cfSub{mkCF(100, "TESTING", nil, nil, nil)}, nil }
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 0 {
		t.Fatal("TESTING 期间应保持静默")
	}

	fetchCodeforces = func(string, *config) ([]cfSub, error) {
		return []cfSub{mkCF(100, "OK", iptr(42), lptr(156), lptr(4096*1024))}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 1 || !strings.Contains(n.msgs[0].title, "✅ Accepted") || n.msgs[0].urgency != "normal" {
		t.Fatalf("期望 normal 级 AC 通知,实际 %+v", n.msgs)
	}
	if !strings.Contains(n.msgs[0].body, "通过 42 组测试") || !strings.Contains(n.msgs[0].body, "4096 KB") {
		t.Fatalf("通知正文: %s", n.msgs[0].body)
	}
	acc := st.Codeforces.Accounts["tourist"]
	if len(acc.Pending) != 0 {
		t.Fatal("AC 后 pending 应为空")
	}

	// 新提交 -> 提交通知 + 跟踪 -> WA 出 critical 结果
	fetchCodeforces = func(string, *config) ([]cfSub, error) {
		return []cfSub{mkCF(101, "TESTING", nil, nil, nil), mkCF(100, "OK", iptr(42), nil, nil)}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 2 || !strings.Contains(n.msgs[1].title, "新提交") {
		t.Fatalf("期望新提交通知,实际 %+v", n.msgs)
	}
	if len(acc.Pending) != 1 {
		t.Fatal("应跟踪提交 101")
	}

	fetchCodeforces = func(string, *config) ([]cfSub, error) {
		return []cfSub{mkCF(101, "WRONG_ANSWER", iptr(3), lptr(62), lptr(256*1024)), mkCF(100, "OK", iptr(42), nil, nil)}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 3 || !strings.Contains(n.msgs[2].title, "❌ Wrong Answer") || n.msgs[2].urgency != "critical" {
		t.Fatalf("期望 critical 级 WA 通知,实际 %+v", n.msgs)
	}
	if len(acc.Pending) != 0 {
		t.Fatal("出结果后 pending 应为空")
	}

	// critical_on_fail=false 时未通过用 normal
	cfg.CriticalOnFail = false
	fetchCodeforces = func(string, *config) ([]cfSub, error) { return []cfSub{mkCF(102, "WA", nil, nil, nil)}, nil }
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	last := n.msgs[len(n.msgs)-1]
	if last.urgency != "normal" {
		t.Fatalf("关闭 critical_on_fail 后应为 normal,实际 %s", last.urgency)
	}
}

func TestMultiAccountIndependence(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	orig := fetchCodeforces
	defer func() { fetchCodeforces = orig }()

	// 两个账号各自初始化
	fetchCodeforces = func(h string, _ *config) ([]cfSub, error) {
		if h == "alice" {
			return []cfSub{mkCF(500, "OK", nil, nil, nil)}, nil
		}
		return []cfSub{mkCF(900, "OK", nil, nil, nil)}, nil
	}
	pollCodeforces([]string{"alice", "bob"}, st, n, cfg, sp, false)
	alice, bob := st.Codeforces.Accounts["alice"], st.Codeforces.Accounts["bob"]
	if alice == nil || bob == nil || alice.LastID != 500 || bob.LastID != 900 {
		t.Fatalf("多账号状态相互独立,实际 %+v", st.Codeforces.Accounts)
	}
	if len(n.msgs) != 0 {
		t.Fatalf("初始化应静默: %+v", n.msgs)
	}

	// 只有 bob 有新提交 -> 只有 bob 收到通知且标题带用户名
	fetchCodeforces = func(h string, _ *config) ([]cfSub, error) {
		if h == "alice" {
			return []cfSub{mkCF(500, "OK", nil, nil, nil)}, nil
		}
		return []cfSub{mkCF(901, "WA", nil, nil, nil), mkCF(900, "OK", nil, nil, nil)}, nil
	}
	pollCodeforces([]string{"alice", "bob"}, st, n, cfg, sp, false)
	if len(n.msgs) != 2 {
		t.Fatalf("期望 bob 的提交通知 + 结果通知,实际 %+v", n.msgs)
	}
	for _, m := range n.msgs {
		if !strings.Contains(m.title, "Codeforces·bob") {
			t.Fatalf("多账号通知应带用户名: %s", m.title)
		}
	}
	if alice.LastID != 500 || bob.LastID != 901 {
		t.Fatalf("账号状态各自更新: alice=%d bob=%d", alice.LastID, bob.LastID)
	}
}

func TestRatingFlow(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	orig := fetchCodeforces
	origRating := fetchCodeforcesRating
	defer func() {
		fetchCodeforces = orig
		fetchCodeforcesRating = origRating
	}()

	fetchCodeforces = func(string, *config) ([]cfSub, error) { return []cfSub{mkCF(1, "OK", nil, nil, nil)}, nil }

	// 首次检查: 静默记录最新 rating,不通知
	fetchCodeforcesRating = func(string, *config) ([]ratingChange, error) {
		return []ratingChange{
			{ContestName: "Round #1", RatingUpdateTimeSeconds: 1000, OldRating: 1400, NewRating: 1522, Rank: 120},
			{ContestName: "Round #2", RatingUpdateTimeSeconds: 2000, OldRating: 1522, NewRating: 1473, Rank: 800},
		}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	acc := st.Codeforces.Accounts["tourist"]
	if acc.LastRatingTS != 2000 {
		t.Fatalf("首次检查应静默记录最新 rating 时间,实际 %d", acc.LastRatingTS)
	}
	if len(n.msgs) != 0 {
		t.Fatalf("首次 rating 检查应静默: %+v", n.msgs)
	}

	// 间隔未到: 即使有新变化也不检查
	fetchCodeforcesRating = func(string, *config) ([]ratingChange, error) {
		return []ratingChange{{ContestName: "Round #3", RatingUpdateTimeSeconds: 3000, OldRating: 1473, NewRating: 1600, Rank: 50}}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 0 {
		t.Fatalf("间隔未到不应触发检查: %+v", n.msgs)
	}

	// 手动把上次检查时间拨回过去,触发检查 -> 晋级通知
	acc.LastRatingCheck = 0
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 1 || !strings.Contains(n.msgs[0].title, "Rating 更新 🎉") {
		t.Fatalf("期望晋级通知,实际 %+v", n.msgs)
	}
	body := n.msgs[0].body
	if !strings.Contains(body, "1473 → 1600 (+127)") || !strings.Contains(body, "Specialist → Expert") {
		t.Fatalf("rating 通知正文: %s", body)
	}
	if acc.LastRatingTS != 3000 {
		t.Fatalf("last_rating_ts 应更新为 3000,实际 %d", acc.LastRatingTS)
	}

	// 掉分通知
	n.msgs = nil
	acc.LastRatingCheck = 0
	fetchCodeforcesRating = func(string, *config) ([]ratingChange, error) {
		return []ratingChange{{ContestName: "Round #4", RatingUpdateTimeSeconds: 4000, OldRating: 1600, NewRating: 1500, Rank: 999}}, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 1 || !strings.Contains(n.msgs[0].title, "Rating 更新 😢") ||
		!strings.Contains(n.msgs[0].body, "1600 → 1500 (-100)") {
		t.Fatalf("期望掉分通知,实际 %+v", n.msgs)
	}

	// rating_check_interval=0 时关闭检查
	cfg.RatingCheckInterval = 0
	n.msgs = nil
	acc.LastRatingCheck = 0
	fetchCodeforcesRating = func(string, *config) ([]ratingChange, error) {
		t.Fatal("关闭后不应再调用 rating API")
		return nil, nil
	}
	pollCodeforces([]string{"tourist"}, st, n, cfg, sp, false)
	if len(n.msgs) != 0 {
		t.Fatalf("关闭后不应有通知: %+v", n.msgs)
	}
}

func TestAtcoderFlow(t *testing.T) {
	st := newAppState()
	cfg := defaultConfig()
	n := &fakeSender{}
	sp := t.TempDir() + "/state.json"
	orig := fetchAtcoder
	defer func() { fetchAtcoder = orig }()

	mkAC := func(id int64, result string, execMs *int64) acSub {
		return acSub{ID: id, ProblemID: "abc999_a", Language: "C++ 23", Result: result, ExecutionTime: execMs}
	}

	// WJ -> 静默跟踪 -> AC 通知
	fetchAtcoder = func(string, *config) ([]acSub, error) { return []acSub{mkAC(7, "WJ", nil)}, nil }
	if active := pollAtcoder([]string{"chokudai"}, st, n, cfg, sp, false); !active {
		t.Fatal("启动时应跟踪 WJ 提交")
	}
	if len(n.msgs) != 0 {
		t.Fatalf("初始化必须静默,实际 %v", n.msgs)
	}

	fetchAtcoder = func(string, *config) ([]acSub, error) { return []acSub{mkAC(7, "AC", lptr(12))}, nil }
	pollAtcoder([]string{"chokudai"}, st, n, cfg, sp, false)
	if len(n.msgs) != 1 || !strings.Contains(n.msgs[0].title, "✅ Accepted") || !strings.Contains(n.msgs[0].body, "耗时: 12 ms") {
		t.Fatalf("期望 AC 通知,实际 %+v", n.msgs)
	}
	if len(st.Atcoder.Accounts["chokudai"].Pending) != 0 {
		t.Fatal("AC 后 pending 应为空")
	}

	// 新提交直接出 TLE -> 提交通知 + critical 结果
	fetchAtcoder = func(string, *config) ([]acSub, error) { return []acSub{mkAC(8, "TLE", lptr(2200))}, nil }
	pollAtcoder([]string{"chokudai"}, st, n, cfg, sp, false)
	if len(n.msgs) != 3 || !strings.Contains(n.msgs[1].title, "新提交") || n.msgs[2].urgency != "critical" {
		t.Fatalf("期望 TLE critical 通知,实际 %+v", n.msgs)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sp := dir + "/state.json"
	st := newAppState()
	st.Codeforces.Accounts["tourist"] = &accountState{
		Handle:  "tourist",
		LastID:  12345,
		Pending: map[string]subInfo{"12345": {Problem: "1234A. Test", Lang: "C++17", Verdict: "TESTING", SeenAt: 1700000000}},
	}

	saveState(st, sp)
	got := loadState(sp)
	acc, ok := got.Codeforces.Accounts["tourist"]
	if !ok || acc.LastID != 12345 {
		t.Fatalf("状态往返失败: %+v", got.Codeforces)
	}
	info, ok := acc.Pending["12345"]
	if !ok || info.Verdict != "TESTING" || info.SeenAt != 1700000000 {
		t.Fatalf("pending 往返失败: %+v", acc.Pending)
	}
}

// 旧版(单账号)状态文件应被安全忽略,不崩溃、不误报
func TestLoadLegacyStateIgnored(t *testing.T) {
	legacy := `{
  "codeforces": {"handle": "tourist", "last_id": 383013765, "pending": {}},
  "atcoder": {"handle": null, "last_id": 0, "pending": {}}
}`
	dir := t.TempDir()
	sp := dir + "/state.json"
	if err := os.WriteFile(sp, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	st := loadState(sp)
	if len(st.Codeforces.Accounts) != 0 || len(st.Atcoder.Accounts) != 0 {
		t.Fatalf("旧状态应被忽略: %+v", st)
	}
}

// 旧版配置(单用户字符串键)应归一化为 handles 数组
func TestLoadLegacyConfig(t *testing.T) {
	legacy := `{"codeforces_handle": "tourist", "atcoder_handle": "chokudai", "poll_interval_idle": 7}`
	dir := t.TempDir()
	cp := dir + "/config.json"
	if err := os.WriteFile(cp, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig(cp)
	if len(cfg.CodeforcesHandles) != 1 || cfg.CodeforcesHandles[0] != "tourist" ||
		len(cfg.AtcoderHandles) != 1 || cfg.AtcoderHandles[0] != "chokudai" {
		t.Fatalf("旧配置归一化失败: %+v", cfg)
	}
	if cfg.PollIdle != 7 || cfg.RatingCheckInterval != 300 || !cfg.CriticalOnFail {
		t.Fatalf("默认值合并失败: %+v", cfg)
	}
}

func TestRatingIntervalZeroDisables(t *testing.T) {
	dir := t.TempDir()
	cp := dir + "/config.json"
	if err := os.WriteFile(cp, []byte(`{"codeforces_handles": ["tourist"], "rating_check_interval": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig(cp)
	if cfg.RatingCheckInterval != 0 {
		t.Fatalf("显式填 0 应关闭 rating 检查,实际 %d", cfg.RatingCheckInterval)
	}
}

func TestSplitHandles(t *testing.T) {
	got := splitHandles(" a, b,,c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("splitHandles 失败: %+v", got)
	}
}
