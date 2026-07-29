package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// release_check — 「机器上装的这个程序有没有新发布版」。
//
// 这是**两条更新里的第二条**，和第一条性质完全不同，所以刻意分开实现：
//
//	① 页面旧了：服务器重新部署过，浏览器还跑着旧 JS chunk。动作 = 刷新（本地、瞬时、零成本）。
//	   已由前端 useAppUpdate 用 asset-hash 比对完成，不需要服务端参与。
//	② 程序旧了：GitHub 上打了新 tag。动作 = 去下载重装（离开应用、要在机器上操作）。就是本文件。
//
// 把这两条合并成一个"有更新"是错的：它们的紧迫度、动作、和用户要付出的代价都不一样。
//
// # 为什么查询放在服务端而不是浏览器
//
// 页面的 CSP `connect-src` 只放行同源（这个仓在远程 mesh 那次踩过这个坑：跨源请求被静默拦掉，
// 表现成"连不上"，查了好几轮才定位到 console 里那行 CSP 报错）。前端直连 api.github.com 会
// 重蹈覆辙，而放宽 CSP 是拿攻击面换一个小功能，不划算。服务端查还顺带三个好处：结果可缓存、
// 不把每个访客的 IP 暴露给 GitHub、离线时能干净地降级。
//
// # 三条克制
//
//   - **只有 release 构建才查**。跑 `dev-<hash>` 的是开发者，他手上的代码**可能比 release 还新**，
//     对他说"有新版本"是噪音，甚至是误导。isReleaseVersion 就是这道闸门。
//   - **只有知道自己是哪个产品才查**。仓库名来自 Config.ReleaseRepo，空则整条检查关闭 —— 嵌入方
//     （deepwork-pro）用的是它自己的 tag，拿它去比 deepwork-terminal 的 release 只会说假话。
//   - **不后台轮询**。缓存 + 懒触发（有人问 /version 才顺手刷新），没有常驻 goroutine。
//   - **查不到 ≠ 已是最新**。失败就是失败，如实返回空，让 UI 说"没查到"，绝不显示一个假的 ✓。
const (
	// 成功结果缓存这么久 —— 发布是低频事件，没必要问得勤。
	releaseCacheTTL = 6 * time.Hour
	// 失败结果只缓存这么久 —— 一次网络抖动不该让用户 6 小时都查不到。
	releaseFailTTL = 15 * time.Minute
	releaseTimeout = 5 * time.Second
)

// ReleaseInfo 是"最新发布版"的全部事实。两个字段要么都有、要么都没有。
type ReleaseInfo struct {
	Tag string `json:"tag"`
	URL string `json:"url"`
}

type releaseChecker struct {
	// repo 是 "owner/name"；空 ⟹ 这个构建没有声明自己的上游，一律不查。
	repo      string
	mu        sync.Mutex
	info      ReleaseInfo
	checkedAt time.Time
	inFlight  bool
	// httpGet 可注入，测试不打真网络。
	httpGet func(ctx context.Context, url string) (*http.Response, error)
}

func newReleaseChecker(repo string) *releaseChecker {
	return &releaseChecker{repo: repo, httpGet: defaultReleaseGet}
}

func defaultReleaseGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub 要求带 User-Agent，否则 403。
	req.Header.Set("User-Agent", "deepwork-terminal")
	req.Header.Set("Accept", "application/vnd.github+json")
	return http.DefaultClient.Do(req)
}

// ReleaseState 是给 UI 的**唯一权威答案**，四态互斥。
//
// 为什么由服务端定态、而不是把 latest 丢给前端自己比：语义化版本的比较规则属于"发布"这个领域，
// 它只该有一份实现。更要紧的是 —— **"查过了，你是最新的" 和 "根本没查到" 必须是两个态**。
// 早先的设计只在"有更新"时下发 latest 字段，于是这两种情况在前端长得一模一样，UI 只能二选一地
// 猜，而猜错的那一半会变成一句自信的假话（对着一次失败的查询显示 ✓ 已是最新）。
type ReleaseState string

const (
	// ReleaseLocal 本地构建（非干净 tag）：从不查询，也不该对它说任何关于发布版的话。
	ReleaseLocal ReleaseState = "local"
	// ReleaseCurrent 查到了，且当前就是最新发布版。
	ReleaseCurrent ReleaseState = "current"
	// ReleaseOutdated 查到了，且有更新的发布版（此时 ReleaseInfo 一定非空）。
	ReleaseOutdated ReleaseState = "outdated"
	// ReleaseUnknown 是发布版，但还没有可用结果（首次加载 / 离线 / GitHub 不可达）。
	// **它不是 current**，UI 必须如实说"没查到"。
	ReleaseUnknown ReleaseState = "unknown"
)

// Latest 立刻返回当前已知的状态，并在过期时于后台刷新。
//
// 刻意不阻塞：/version 是 UI 首屏就要的，绝不能为了一个"顺带"的信息把它卡在一次跨公网请求上。
// 代价是首次加载多半是 unknown，下一次才有结果 —— 对一个低频事件这完全可以接受，而且 unknown
// 本来就是一句诚实的话。
func (c *releaseChecker) Latest(currentVersion string) (ReleaseInfo, ReleaseState) {
	if c.repo == "" || !isReleaseVersion(currentVersion) {
		// 没声明上游，或不是发布版：不查，也不对它说任何关于发布版的话。
		return ReleaseInfo{}, ReleaseLocal
	}
	c.mu.Lock()
	info, fresh := c.info, time.Since(c.checkedAt) < c.ttlLocked()
	if !fresh && !c.inFlight {
		c.inFlight = true
		go c.refresh()
	}
	c.mu.Unlock()
	if info.Tag == "" {
		return ReleaseInfo{}, ReleaseUnknown
	}
	if IsNewerRelease(currentVersion, info.Tag) {
		return info, ReleaseOutdated
	}
	return info, ReleaseCurrent
}

func (c *releaseChecker) ttlLocked() time.Duration {
	if c.info.Tag == "" {
		return releaseFailTTL // 上次没查到（或没查过）→ 早点再试
	}
	return releaseCacheTTL
}

func (c *releaseChecker) refresh() {
	info := c.fetch()
	c.mu.Lock()
	c.info = info
	c.checkedAt = time.Now()
	c.inFlight = false
	c.mu.Unlock()
}

func (c *releaseChecker) fetch() ReleaseInfo {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	resp, err := c.httpGet(ctx, "https://api.github.com/repos/"+c.repo+"/releases/latest")
	if err != nil {
		logger.Debug("release check failed", "error", err)
		return ReleaseInfo{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		logger.Debug("release check non-200", "status", resp.StatusCode)
		return ReleaseInfo{}
	}
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		logger.Debug("release check decode failed", "error", err)
		return ReleaseInfo{}
	}
	if payload.TagName == "" || payload.HTMLURL == "" {
		return ReleaseInfo{}
	}
	return ReleaseInfo{Tag: payload.TagName, URL: payload.HTMLURL}
}

// semverRe 匹配"干净的发布版本"：v0.7.14 / 0.7.14。**刻意不接受**任何后缀 —— `v0.7.14-3-gb2535a0`
// 是 git describe 的产物，意味着"在 tag 之后又走了 3 个提交"，那不是一个发布版。
var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// isReleaseVersion 判断这个构建是不是"发布版"——只有它才有资格与 GitHub 上的 tag 比较。
func isReleaseVersion(v string) bool {
	return semverRe.MatchString(strings.TrimSpace(v))
}

// IsNewerRelease 报告 latest 是否严格新于 current。两者都必须是干净的发布版，否则一律返回
// false —— 比不了就不比，绝不猜。
func IsNewerRelease(current, latest string) bool {
	c := semverRe.FindStringSubmatch(strings.TrimSpace(current))
	l := semverRe.FindStringSubmatch(strings.TrimSpace(latest))
	if c == nil || l == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		cv, _ := strconv.Atoi(c[i])
		lv, _ := strconv.Atoi(l[i])
		if lv != cv {
			return lv > cv
		}
	}
	return false
}
