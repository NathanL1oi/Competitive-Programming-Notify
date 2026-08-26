package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

func httpJSON(u string, out any) error {
	var last error
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := httpClient.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == 200 {
				return json.Unmarshal(body, out)
			}
			if rerr != nil {
				last = rerr
			} else {
				last = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		} else {
			last = err
		}
		time.Sleep(time.Duration(i+1) * 1500 * time.Millisecond)
	}
	return last
}

// ---------------------------------------------------------------- CF 签名认证

const randChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "123456"[:n] // 兜底,任何 6 字符串都合法
	}
	for i := range b {
		b[i] = randChars[int(b[i])%len(randChars)]
	}
	return string(b)
}

// cfSignedParams 按 Codeforces 官方规则生成带签名的查询参数:
//
//	hash 输入 = rand + "/" + method + "?" + 按字母序排序的 query(含 apiKey 与 time) + "#" + secret
//	apiSig   = rand + sha512hex(hash 输入) 的前 6 个十六进制字符
//
// now 与 randStr 参数便于单元测试注入固定值。
func cfSignedParams(method string, base url.Values, key, secret string, now int64, randStr string) url.Values {
	out := cloneValues(base)
	out.Set("apiKey", key)
	out.Set("time", fmt.Sprintf("%d", now))

	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var qb strings.Builder
	for i, k := range keys {
		if i > 0 {
			qb.WriteByte('&')
		}
		qb.WriteString(k)
		qb.WriteByte('=')
		qb.WriteString(out.Get(k))
	}

	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(randStr + "/" + method + "?" + qb.String() + "#" + secret))
	sig := hex.EncodeToString(mac.Sum(nil))[:6]

	out.Set("apiSig", randStr+sig)
	return out
}

type cfSub struct {
	ID        int64 `json:"id"`
	ContestID int64 `json:"contestId"`
	Problem   struct {
		Index string `json:"index"`
		Name  string `json:"name"`
	} `json:"problem"`
	Verdict             string `json:"verdict"`
	PassedTestCount     *int   `json:"passedTestCount"`
	Testset             string `json:"testset"` // SAMPLES / PRETESTS / TESTS / ...(区分失败点在预测试还是正式测试)
	TimeConsumedMillis  *int64 `json:"timeConsumedMillis"`
	MemoryConsumedBytes *int64 `json:"memoryConsumedBytes"`
	ProgrammingLanguage string `json:"programmingLanguage"`
	CreationTimeSeconds int64  `json:"creationTimeSeconds"`
}

type cfResponse struct {
	Status  string  `json:"status"`
	Comment string  `json:"comment"`
	Result  []cfSub `json:"result"`
}

type ratingChange struct {
	ContestID               int64  `json:"contestId"`
	ContestName             string `json:"contestName"`
	Handle                  string `json:"handle"`
	Rank                    int    `json:"rank"`
	RatingUpdateTimeSeconds int64  `json:"ratingUpdateTimeSeconds"`
	OldRating               int    `json:"oldRating"`
	NewRating               int    `json:"newRating"`
}

type ratingResponse struct {
	Status  string         `json:"status"`
	Comment string         `json:"comment"`
	Result  []ratingChange `json:"result"`
}

// cfAPI 调用 Codeforces API;配置了 key/secret 时带签名,
// 认证失败(签名/apiKey 相关)则回退到无认证请求。
func cfAPI(method string, params url.Values, cfg *config) ([]byte, error) {
	authed := cfg.CodeforcesAPIKey != "" && cfg.CodeforcesAPISecret != ""

	if authed {
		signed := cfSignedParams(method, params, cfg.CodeforcesAPIKey,
			cfg.CodeforcesAPISecret, time.Now().Unix(), randomString(6))
		u := "https://codeforces.com/api/" + method + "?" + signed.Encode()
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err == nil {
			req.Header.Set("User-Agent", userAgent)
			if resp, err := httpClient.Do(req); err == nil {
				body, rerr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if rerr == nil && resp.StatusCode == 200 {
					return body, nil
				}
				// 认证失败: 回退到无认证并提示
				logThrottled("cf_auth_fallback",
					"Codeforces 认证请求失败,已回退到无认证请求(请检查 api key/secret 与系统时间)", time.Hour)
			}
		}
	}

	u := "https://codeforces.com/api/" + method + "?" + params.Encode()
	return httpGet(u)
}

// httpGet 带 3 次重试(与 httpJSON 一致);codeforces.com / atcoder.jp
// 在本机网络下偶有连接被重置(EOF),重试一轮通常即成功。
func httpGet(u string) ([]byte, error) {
	var last error
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := httpClient.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == 200 {
				return body, nil
			}
			if rerr != nil {
				last = rerr
			} else {
				last = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		} else {
			last = err
		}
		time.Sleep(time.Duration(i+1) * 1500 * time.Millisecond)
	}
	return nil, last
}

func cloneValues(v url.Values) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	return c
}

// fetchCodeforces 使用 Codeforces 官方 API(user.status),返回最近的提交列表。
// 声明为变量便于单元测试注入假数据。
var fetchCodeforces = func(handle string, cfg *config) ([]cfSub, error) {
	params := url.Values{
		"handle": {handle},
		"from":   {"1"},
		"count":  {fmt.Sprintf("%d", cfCount)},
	}
	body, err := cfAPI("user.status", params, cfg)
	if err != nil {
		return nil, err
	}
	var d cfResponse
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, err
	}
	if d.Status != "OK" {
		return nil, fmt.Errorf("%s", orDefault(d.Comment, "Codeforces API 返回异常"))
	}
	return d.Result, nil
}

// fetchCodeforcesRating 使用官方 API(user.rating),返回该账号全部历史 rating 变化
var fetchCodeforcesRating = func(handle string, cfg *config) ([]ratingChange, error) {
	params := url.Values{"handle": {handle}}
	body, err := cfAPI("user.rating", params, cfg)
	if err != nil {
		return nil, err
	}
	var d ratingResponse
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, err
	}
	if d.Status != "OK" {
		return nil, fmt.Errorf("%s", orDefault(d.Comment, "user.rating 返回异常"))
	}
	return d.Result, nil
}

type acSub struct {
	ID            int64    `json:"id"`
	ContestID     string   `json:"contest_id"`
	ProblemID     string   `json:"problem_id"`
	Language      string   `json:"language"`
	Result        string   `json:"result"`
	Point         *float64 `json:"point"` // 得分:AC 时为该题满分;部分给分题非 AC 时反映部分通过进度
	ExecutionTime *int64   `json:"execution_time"`
	EpochSecond   int64    `json:"epoch_second"`
}

// fetchAtcoder 使用 kenkoooo.com 第三方 API(AtCoder 官方无公开提交 API)
var fetchAtcoder = func(handle string, cfg *config) ([]acSub, error) {
	u := "https://kenkoooo.com/atcoder/atcoder-api/v3/user/submissions?" + url.Values{
		"user":        {handle},
		"from_second": {fmt.Sprintf("%d", time.Now().Unix()-int64(acLookback.Seconds()))},
	}.Encode()
	var d []acSub
	if err := httpJSON(u, &d); err != nil {
		return nil, err
	}
	return d, nil
}

// ---------------------------------------------------------------- 用户信息(状态栏用户卡片)

// cfUser 对应 Codeforces user.info 的返回条目。
// 未参与过 Rated 比赛的账号 rating/rank 等字段会缺失,故用指针。
type cfUser struct {
	Handle                  string `json:"handle"`
	Rating                  *int   `json:"rating"`
	MaxRating               *int   `json:"maxRating"`
	Rank                    string `json:"rank"`
	MaxRank                 string `json:"maxRank"`
	Contribution            *int   `json:"contribution"`
	FriendOfCount           *int   `json:"friendOfCount"`
	TitlePhoto              string `json:"titlePhoto"`
	Avatar                  string `json:"avatar"`
	LastOnlineTimeSeconds   int64  `json:"lastOnlineTimeSeconds"`
	RegistrationTimeSeconds int64  `json:"registrationTimeSeconds"`
}

type cfUserResponse struct {
	Status  string   `json:"status"`
	Comment string   `json:"comment"`
	Result  []cfUser `json:"result"`
}

// fetchCFUserInfos 使用官方 API(user.info),一次请求可批量查询多个用户(分号分隔)
var fetchCFUserInfos = func(handles []string, cfg *config) ([]cfUser, error) {
	params := url.Values{
		"handles":              {strings.Join(handles, ";")},
		"checkHistoricHandles": {"false"},
	}
	body, err := cfAPI("user.info", params, cfg)
	if err != nil {
		return nil, err
	}
	var d cfUserResponse
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, err
	}
	if d.Status != "OK" {
		return nil, fmt.Errorf("%s", orDefault(d.Comment, "user.info 返回异常"))
	}
	return d.Result, nil
}

// acHistoryEntry 对应 atcoder.jp/users/{handle}/history/json 的返回条目
// (AtCoder 官方页面提供的公开 JSON,含 rating 历史)
type acHistoryEntry struct {
	IsRated           bool   `json:"IsRated"`
	Place             int    `json:"Place"`
	OldRating         int    `json:"OldRating"`
	NewRating         int    `json:"NewRating"`
	Performance       int    `json:"Performance"`
	ContestName       string `json:"ContestName"`
	ContestNameEn     string `json:"ContestNameEn"`
	ContestScreenName string `json:"ContestScreenName"`
	EndTime           string `json:"EndTime"` // RFC3339
}

// fetchACHistory 抓取 AtCoder 用户的 rated 比赛历史(按时间升序)
var fetchACHistory = func(handle string) ([]acHistoryEntry, error) {
	u := "https://atcoder.jp/users/" + url.PathEscape(handle) + "/history/json"
	var d []acHistoryEntry
	if err := httpJSON(u, &d); err != nil {
		return nil, err
	}
	return d, nil
}

// fetchAtcoderLatest 抓取用户全局最新一次提交,不受轮询 2 小时回看窗限制。
// v3 接口按 epoch_second 升序、每请求最多 500 条,这里从一年前开始向后翻页,
// 最多 5 页(覆盖 2500 条/年),返回见过的最大 id 的提交;
// 一年内无提交返回 (nil, nil)。
var fetchAtcoderLatest = func(handle string) (*acSub, error) {
	from := time.Now().Unix() - int64((365*24*time.Hour)/time.Second)
	var best *acSub
	for page := 0; page < 5; page++ {
		u := "https://kenkoooo.com/atcoder/atcoder-api/v3/user/submissions?" + url.Values{
			"user":        {handle},
			"from_second": {fmt.Sprintf("%d", from)},
		}.Encode()
		var d []acSub
		if err := httpJSON(u, &d); err != nil {
			return nil, err
		}
		if len(d) == 0 {
			break
		}
		top := d[0]
		for _, s := range d {
			if s.ID > top.ID {
				top = s
			}
		}
		if best == nil || top.ID > best.ID {
			t := top
			best = &t
		}
		if len(d) < 500 {
			break // 已是最后一页
		}
		from = d[len(d)-1].EpochSecond + 1
	}
	return best, nil
}
