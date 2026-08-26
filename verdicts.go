package main

// 判定结果表: Label 说明文字, Emoji 图标, OK(nil=未出结果, true=通过, false=未通过)
type verdictInfo struct {
	Label string
	Emoji string
	OK    *bool
}

func vb(b bool) *bool { return &b }

var cfVerdicts = map[string]verdictInfo{
	"TESTING":                 {Label: "Judging", Emoji: "⏳", OK: nil},
	"OK":                      {Label: "Accepted", Emoji: "✅", OK: vb(true)},
	"WRONG_ANSWER":            {Label: "Wrong Answer", Emoji: "❌", OK: vb(false)},
	"TIME_LIMIT_EXCEEDED":     {Label: "Time Limit Exceeded", Emoji: "⏱️", OK: vb(false)},
	"MEMORY_LIMIT_EXCEEDED":   {Label: "Memory Limit Exceeded", Emoji: "🐘", OK: vb(false)},
	"RUNTIME_ERROR":           {Label: "Runtime Error", Emoji: "💥", OK: vb(false)},
	"COMPILATION_ERROR":       {Label: "Compile Error", Emoji: "🔧", OK: vb(false)},
	"PRESENTATION_ERROR":      {Label: "Presentation Error", Emoji: "📄", OK: vb(false)},
	"IDLENESS_LIMIT_EXCEEDED": {Label: "Idleness Limit Exceeded", Emoji: "😴", OK: vb(false)},
	"SKIPPED":                 {Label: "Skipped", Emoji: "⏭️", OK: vb(false)},
	"CHALLENGED":              {Label: "Hacked", Emoji: "🗡️", OK: vb(false)},
	"REJECTED":                {Label: "Rejected", Emoji: "🚫", OK: vb(false)},
	"PARTIAL":                 {Label: "Partial", Emoji: "🟡", OK: nil},
	"CRASHED":                 {Label: "Crashed", Emoji: "💥", OK: vb(false)},
	"SECURITY_VIOLATION":      {Label: "Security Violation", Emoji: "🛡️", OK: vb(false)},
	"FAILED":                  {Label: "Failed", Emoji: "❌", OK: vb(false)},
}

var acVerdicts = map[string]verdictInfo{
	"WJ":  {Label: "Judging", Emoji: "⏳", OK: nil},
	"WR":  {Label: "Awaiting rejudge", Emoji: "⏳", OK: nil},
	"AC":  {Label: "Accepted", Emoji: "✅", OK: vb(true)},
	"WA":  {Label: "Wrong Answer", Emoji: "❌", OK: vb(false)},
	"TLE": {Label: "Time Limit Exceeded", Emoji: "⏱️", OK: vb(false)},
	"MLE": {Label: "Memory Limit Exceeded", Emoji: "🐘", OK: vb(false)},
	"RE":  {Label: "Runtime Error", Emoji: "💥", OK: vb(false)},
	"CE":  {Label: "Compile Error", Emoji: "🔧", OK: vb(false)},
	"OLE": {Label: "Output Limit Exceeded", Emoji: "📤", OK: vb(false)},
	"IE":  {Label: "Internal Error", Emoji: "⚠️", OK: vb(false)},
}

// AtCoder 仍在等待评测的状态
func acIsPending(r string) bool { return r == "WJ" || r == "WR" || r == "" }

func verdictOf(m map[string]verdictInfo, v string) verdictInfo {
	if vi, ok := m[v]; ok {
		return vi
	}
	return verdictInfo{Label: v, Emoji: "❔", OK: nil}
}

// ---------------------------------------------------------------- 短判定码(状态栏徽标用)

// CF 判定码 → 竞赛界惯用的短缩写(AC/WA/TLE...),供状态栏卡片的判定徽标显示
var cfShortVerdicts = map[string]string{
	"TESTING":                 "TEST",
	"OK":                      "AC",
	"WRONG_ANSWER":            "WA",
	"TIME_LIMIT_EXCEEDED":     "TLE",
	"MEMORY_LIMIT_EXCEEDED":   "MLE",
	"RUNTIME_ERROR":           "RE",
	"COMPILATION_ERROR":       "CE",
	"PRESENTATION_ERROR":      "PE",
	"IDLENESS_LIMIT_EXCEEDED": "ILE",
	"SKIPPED":                 "SKIP",
	"CHALLENGED":              "HACK",
	"REJECTED":                "REJ",
	"PARTIAL":                 "PART",
	"CRASHED":                 "CRASH",
	"SECURITY_VIOLATION":      "SEC",
	"FAILED":                  "FAIL",
}

// shortVerdict 返回判定码的短形式;AtCoder 的 Result 本身已是短码,原样返回。
// 未知判定回退为原始判定码(保持信息不丢失)。
func shortVerdict(platform, verdict string) string {
	if platform == "codeforces" {
		if s, ok := cfShortVerdicts[verdict]; ok {
			return s
		}
	}
	return verdict
}

// cfFailsOnTest 报告该 CF 判定是否定位到某个具体失败的测试点
// (此时 passedTestCount+1 即失败的测试号)。
// PARTIAL 是部分给分而非"挂在某测试",CHALLENGED 是被 hack,均不适用。
func cfFailsOnTest(verdict string) bool {
	switch verdict {
	case "WRONG_ANSWER", "TIME_LIMIT_EXCEEDED", "MEMORY_LIMIT_EXCEEDED",
		"RUNTIME_ERROR", "PRESENTATION_ERROR", "IDLENESS_LIMIT_EXCEEDED",
		"CRASHED", "FAILED":
		return true
	}
	return false
}
