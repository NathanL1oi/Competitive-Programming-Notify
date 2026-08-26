package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func cfToInfo(s cfSub) subInfo {
	v := s.Verdict
	if v == "" {
		v = "TESTING"
	}
	return subInfo{
		Problem:            fmt.Sprintf("%d%s. %s", s.ContestID, s.Problem.Index, s.Problem.Name),
		Lang:               orDefault(s.ProgrammingLanguage, "?"),
		Verdict:            v,
		PassedTestCount:    s.PassedTestCount,
		TimeConsumedMillis: s.TimeConsumedMillis,
		MemoryConsumedB:    s.MemoryConsumedBytes,
		SeenAt:             float64(time.Now().Unix()),
	}
}

func acToInfo(s acSub) subInfo {
	r := s.Result
	if r == "" {
		r = "WJ"
	}
	return subInfo{
		Problem:       orDefault(s.ProblemID, "?"),
		Lang:          orDefault(s.Language, "?"),
		Result:        r,
		ExecutionTime: s.ExecutionTime,
		SeenAt:        float64(time.Now().Unix()),
	}
}

// 多个账号时在通知标题里带上用户名,便于区分
func cfTag(handle string, multi bool) string {
	if multi {
		return "Codeforces·" + handle
	}
	return "Codeforces"
}

func acTag(handle string, multi bool) string {
	if multi {
		return "AtCoder·" + handle
	}
	return "AtCoder"
}

func announceCFResult(tag string, sid string, info subInfo, n sender, cfg *config) {
	vi := verdictOf(cfVerdicts, info.Verdict)
	urgency := "normal"
	if vi.OK != nil && !*vi.OK && cfg.CriticalOnFail {
		urgency = "critical"
	}
	lines := []string{info.Problem, "语言: " + info.Lang}
	if (info.Verdict == "OK" || info.Verdict == "WRONG_ANSWER" || info.Verdict == "PARTIAL") && info.PassedTestCount != nil {
		lines = append(lines, fmt.Sprintf("通过 %d 组测试", *info.PassedTestCount))
	}
	var tail []string
	if info.TimeConsumedMillis != nil {
		tail = append(tail, fmt.Sprintf("%d ms", *info.TimeConsumedMillis))
	}
	if info.MemoryConsumedB != nil {
		tail = append(tail, fmt.Sprintf("%d KB", *info.MemoryConsumedB/1024))
	}
	if len(tail) > 0 {
		lines = append(lines, strings.Join(tail, " · "))
	}
	n.send(fmt.Sprintf("[%s] #%s %s %s", tag, sid, vi.Emoji, vi.Label), strings.Join(lines, "\n"), urgency, 15000)
}

func announceACResult(tag string, sid string, info subInfo, n sender, cfg *config) {
	vi := verdictOf(acVerdicts, info.Result)
	urgency := "normal"
	if vi.OK != nil && !*vi.OK && cfg.CriticalOnFail {
		urgency = "critical"
	}
	lines := []string{info.Problem, "语言: " + info.Lang}
	if info.ExecutionTime != nil {
		lines = append(lines, fmt.Sprintf("耗时: %d ms", *info.ExecutionTime))
	}
	n.send(fmt.Sprintf("[%s] #%s %s %s", tag, sid, vi.Emoji, vi.Label), strings.Join(lines, "\n"), urgency, 15000)
}

// ---------------------------------------------------------------- Codeforces

// pollCodeforces 轮询所有配置的 CF 账号,并做 rating 变化检查。
// 返回是否有提交仍在评测中(用于决定轮询间隔)。
func pollCodeforces(handles []string, st *appState, n sender, cfg *config, statePath string, verbose bool) bool {
	active := false
	multi := len(handles) > 1
	for _, h := range handles {
		if pollCFAccount(h, multi, st, n, cfg, statePath, verbose) {
			active = true
		}
	}
	checkCFRatings(handles, multi, st, n, cfg, statePath)
	return active
}

func pollCFAccount(handle string, multi bool, st *appState, n sender, cfg *config, statePath string, verbose bool) bool {
	subs, err := fetchCodeforces(handle, cfg)
	if err != nil {
		logThrottled("cf_err_"+handle, fmt.Sprintf("Codeforces 抓取失败(%s): %v", handle, err), time.Hour)
		return false
	}
	if verbose {
		logf(fmt.Sprintf("Codeforces(%s): 获取到 %d 条提交记录", handle, len(subs)))
	}
	if len(subs) == 0 {
		// 通常是新账号还没有任何提交,或用户名写错(API 同样返回空)
		logThrottled("cf_empty_"+handle,
			fmt.Sprintf("Codeforces: 账号 %s 没有提交记录,将保持静默直到检测到其第一次提交(若用户名有误请检查配置)", handle),
			time.Hour)
		return false
	}

	acc := ensureAccount(&st.Codeforces, handle, "codeforces")
	tag := cfTag(handle, multi)
	prunePending(acc.Pending)

	// 状态栏卡片数据: 记录最新提交快照(覆盖式,随评测进度刷新判定),
	// 并维护最近 N 条提交列表(卡片的历史提交区)
	{
		var top cfSub
		for _, s := range subs {
			if s.ID > top.ID {
				top = s
			}
		}
		if rec := cfToLastSub(top); acc.LastSub == nil || rec.ID >= acc.LastSub.ID {
			acc.LastSub = rec
		}
		fresh := make([]*lastSubRecord, 0, len(subs))
		for _, s := range subs {
			fresh = append(fresh, cfToLastSub(s))
		}
		acc.LastSubs = mergeRecentSubs(acc.LastSubs, fresh, recentSubCap)
	}

	// 首次运行: 静默定位到最新提交;若它仍在评测中则跟踪其最终结果
	if acc.LastID == 0 {
		var top cfSub
		for _, s := range subs {
			if s.ID > top.ID {
				top = s
			}
		}
		acc.LastID = top.ID
		if cfToInfo(top).Verdict == "TESTING" {
			acc.Pending[fmt.Sprintf("%d", top.ID)] = cfToInfo(top)
			logf(fmt.Sprintf("检测到进行中的提交 #%d,开始跟踪其评测结果", top.ID))
		}
		logf(fmt.Sprintf("Codeforces(%s) 已初始化(最新提交 #%d)", handle, acc.LastID))
		saveState(st, statePath)
		return len(acc.Pending) > 0
	}

	oldLast := acc.LastID
	var fresh []cfSub
	byID := map[int64]cfSub{}
	for _, s := range subs {
		byID[s.ID] = s
		if s.ID > oldLast {
			fresh = append(fresh, s)
		}
		if s.ID > acc.LastID {
			acc.LastID = s.ID
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].ID < fresh[j].ID })

	for _, s := range fresh {
		info := cfToInfo(s)
		vi := verdictOf(cfVerdicts, info.Verdict)
		n.send(
			fmt.Sprintf("[%s] 新提交 #%d · %s %s", tag, s.ID, vi.Emoji, vi.Label),
			info.Problem+"\n语言: "+info.Lang,
			"normal", 8000)
		if info.Verdict != "TESTING" {
			announceCFResult(tag, fmt.Sprintf("%d", s.ID), info, n, cfg)
		} else {
			acc.Pending[fmt.Sprintf("%d", s.ID)] = info
		}
	}

	// 检查在评测中的提交是否已有结果
	for sid, info := range acc.Pending {
		var id int64
		if _, err := fmt.Sscanf(sid, "%d", &id); err != nil {
			continue
		}
		cur, ok := byID[id]
		if !ok {
			continue
		}
		v := cur.Verdict
		if v == "" {
			v = "TESTING"
		}
		if v != "TESTING" {
			info.Verdict = v
			info.PassedTestCount = cur.PassedTestCount
			info.TimeConsumedMillis = cur.TimeConsumedMillis
			info.MemoryConsumedB = cur.MemoryConsumedBytes
			announceCFResult(tag, sid, info, n, cfg)
			delete(acc.Pending, sid)
		}
	}

	saveState(st, statePath)
	return len(acc.Pending) > 0
}

// ---------------------------------------------------------------- CF rating

func cfRank(r int) string {
	switch {
	case r < 1200:
		return "Newbie"
	case r < 1400:
		return "Pupil"
	case r < 1600:
		return "Specialist"
	case r < 1900:
		return "Expert"
	case r < 2100:
		return "Candidate Master"
	case r < 2300:
		return "Master"
	case r < 2400:
		return "International Master"
	case r < 2600:
		return "Grandmaster"
	case r < 3000:
		return "International Grandmaster"
	default:
		return "Legendary Grandmaster"
	}
}

func checkCFRatings(handles []string, multi bool, st *appState, n sender, cfg *config, statePath string) {
	if cfg.RatingCheckInterval <= 0 {
		return // 配置为 0 时关闭 rating 检查
	}
	now := time.Now().Unix()
	for _, h := range handles {
		acc := ensureAccount(&st.Codeforces, h, "codeforces")
		if now-acc.LastRatingCheck < int64(cfg.RatingCheckInterval) {
			continue
		}
		acc.LastRatingCheck = now
		saveState(st, statePath)

		changes, err := fetchCodeforcesRating(h, cfg)
		if err != nil {
			logThrottled("cf_rating_err_"+h, fmt.Sprintf("Codeforces rating 抓取失败(%s): %v", h, err), time.Hour)
			continue
		}
		if len(changes) == 0 {
			logThrottled("cf_rating_empty_"+h, fmt.Sprintf("Codeforces: 账号 %s 暂无 Rated 记录", h), time.Hour)
			continue
		}

		var maxTS int64
		var latest ratingChange
		for _, c := range changes {
			if c.RatingUpdateTimeSeconds > maxTS {
				maxTS = c.RatingUpdateTimeSeconds
				latest = c
			}
		}
		// 状态栏卡片数据: 最近一场 rated 比赛(数据来自同一次请求,免费)
		if acc.info == nil {
			acc.info = &accountInfo{}
		}
		acc.info.LastContest = &statusContest{
			Name:  latest.ContestName,
			Place: latest.Rank,
			Time:  latest.RatingUpdateTimeSeconds,
		}
		// 首次检查: 静默记录当前最新一条,后续只通知新变化
		if acc.LastRatingTS == 0 {
			acc.LastRatingTS = maxTS
			logf(fmt.Sprintf("Codeforces(%s) rating 已初始化(最近更新于 %s)",
				h, time.Unix(maxTS, 0).Format("2006-01-02 15:04")))
			saveState(st, statePath)
			continue
		}

		var fresh []ratingChange
		for _, c := range changes {
			if c.RatingUpdateTimeSeconds > acc.LastRatingTS {
				fresh = append(fresh, c)
			}
		}
		sort.Slice(fresh, func(i, j int) bool {
			return fresh[i].RatingUpdateTimeSeconds < fresh[j].RatingUpdateTimeSeconds
		})
		for _, c := range fresh {
			announceRating(cfTag(h, multi), c, n)
			if c.RatingUpdateTimeSeconds > acc.LastRatingTS {
				acc.LastRatingTS = c.RatingUpdateTimeSeconds
			}
		}
		saveState(st, statePath)
	}
}

func announceRating(tag string, c ratingChange, n sender) {
	diff := c.NewRating - c.OldRating
	emoji := "➖"
	if diff > 0 {
		emoji = "🎉"
	} else if diff < 0 {
		emoji = "😢"
	}
	lines := []string{c.ContestName}
	if c.OldRating == 0 && c.NewRating == 0 {
		lines = append(lines, "未评级比赛(不计分)")
	} else {
		lines = append(lines, fmt.Sprintf("%d → %d (%+d)", c.OldRating, c.NewRating, diff))
		oldRank, newRank := cfRank(c.OldRating), cfRank(c.NewRating)
		if oldRank != newRank {
			lines = append(lines, fmt.Sprintf("段位: %s → %s", oldRank, newRank))
		} else {
			lines = append(lines, "段位: "+newRank)
		}
	}
	if c.Rank > 0 {
		lines = append(lines, fmt.Sprintf("名次: %d", c.Rank))
	}
	n.send(fmt.Sprintf("[%s] Rating 更新 %s", tag, emoji), strings.Join(lines, "\n"), "normal", 15000)
}

// ---------------------------------------------------------------- AtCoder

// pollAtcoder 轮询所有配置的 AtCoder 账号
func pollAtcoder(handles []string, st *appState, n sender, cfg *config, statePath string, verbose bool) bool {
	active := false
	multi := len(handles) > 1
	for _, h := range handles {
		if pollACAccount(h, multi, st, n, cfg, statePath, verbose) {
			active = true
		}
	}
	return active
}

func pollACAccount(handle string, multi bool, st *appState, n sender, cfg *config, statePath string, verbose bool) bool {
	subs, err := fetchAtcoder(handle, cfg)
	if err != nil {
		logThrottled("ac_err_"+handle, fmt.Sprintf("AtCoder 抓取失败(%s): %v", handle, err), time.Hour)
		return false
	}
	if verbose {
		logf(fmt.Sprintf("AtCoder(%s): 获取到 %d 条提交记录", handle, len(subs)))
	}
	if len(subs) == 0 {
		// 正常情况: 最近 2 小时没有提交;也可能是用户名写错(API 同样返回空)
		logThrottled("ac_empty_"+handle,
			fmt.Sprintf("AtCoder: 账号 %s 最近 %d 小时没有提交记录,将保持静默直到检测到其下一次提交(若用户名有误请检查配置)",
				handle, int(acLookback/time.Hour)),
			time.Hour)
		return false
	}

	acc := ensureAccount(&st.Atcoder, handle, "atcoder")
	tag := acTag(handle, multi)
	prunePending(acc.Pending)

	// 状态栏卡片数据: 记录最新提交快照,并维护最近 N 条提交列表
	{
		var top acSub
		for _, s := range subs {
			if s.ID > top.ID {
				top = s
			}
		}
		if rec := acToLastSub(top); acc.LastSub == nil || rec.ID >= acc.LastSub.ID {
			acc.LastSub = rec
		}
		fresh := make([]*lastSubRecord, 0, len(subs))
		for _, s := range subs {
			fresh = append(fresh, acToLastSub(s))
		}
		acc.LastSubs = mergeRecentSubs(acc.LastSubs, fresh, recentSubCap)
	}

	if acc.LastID == 0 {
		var top acSub
		for _, s := range subs {
			if s.ID > top.ID {
				top = s
			}
		}
		acc.LastID = top.ID
		if acIsPending(top.Result) {
			acc.Pending[fmt.Sprintf("%d", top.ID)] = acToInfo(top)
			logf(fmt.Sprintf("检测到进行中的提交 #%d,开始跟踪其评测结果", top.ID))
		}
		logf(fmt.Sprintf("AtCoder(%s) 已初始化(最新提交 #%d)", handle, acc.LastID))
		saveState(st, statePath)
		return len(acc.Pending) > 0
	}

	oldLast := acc.LastID
	var fresh []acSub
	byID := map[int64]acSub{}
	for _, s := range subs {
		byID[s.ID] = s
		if s.ID > oldLast {
			fresh = append(fresh, s)
		}
		if s.ID > acc.LastID {
			acc.LastID = s.ID
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].ID < fresh[j].ID })

	for _, s := range fresh {
		info := acToInfo(s)
		vi := verdictOf(acVerdicts, info.Result)
		n.send(
			fmt.Sprintf("[%s] 新提交 #%d · %s %s", tag, s.ID, vi.Emoji, vi.Label),
			info.Problem+"\n语言: "+info.Lang,
			"normal", 8000)
		if !acIsPending(info.Result) {
			announceACResult(tag, fmt.Sprintf("%d", s.ID), info, n, cfg)
		} else {
			acc.Pending[fmt.Sprintf("%d", s.ID)] = info
		}
	}

	for sid, info := range acc.Pending {
		var id int64
		if _, err := fmt.Sscanf(sid, "%d", &id); err != nil {
			continue
		}
		cur, ok := byID[id]
		if !ok {
			continue
		}
		if acIsPending(cur.Result) {
			continue
		}
		info.Result = cur.Result
		info.ExecutionTime = cur.ExecutionTime
		announceACResult(tag, sid, info, n, cfg)
		delete(acc.Pending, sid)
	}

	saveState(st, statePath)
	return len(acc.Pending) > 0
}
