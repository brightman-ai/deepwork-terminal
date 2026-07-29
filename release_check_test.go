package terminal

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsReleaseVersionGatesLocalBuilds(t *testing.T) {
	release := []string{"v0.7.14", "0.7.14", "v1.0.0", " v2.13.5 "}
	for _, v := range release {
		if !isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = false, want true", v)
		}
	}
	// 这些都**不是**发布版 —— 对跑着它们的人提示"有新版本"是噪音甚至误导：
	// git describe 的 -N-g<hash> 意味着已经在 tag 之后又走了 N 个提交，本地代码可能比 release 新。
	local := []string{"dev", "dev-b2535a0", "dev-b2535a0-dirty", "v0.7.14-3-gb2535a0", "", "nightly"}
	for _, v := range local {
		if isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = true, want false", v)
		}
	}
}

func TestIsNewerRelease(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.7.14", "v0.7.15", true},
		{"v0.7.14", "v0.8.0", true},
		{"v0.7.14", "v1.0.0", true},
		{"v0.7.14", "v0.7.14", false},  // 相同不算新
		{"v0.7.15", "v0.7.14", false},  // 更旧不算新
		{"v0.9.0", "v0.10.0", true},    // 数字比较，不是字符串比较
		{"v0.10.0", "v0.9.0", false},   // 反向同理
		{"dev-b2535a0", "v0.7.15", false}, // 本地构建：比不了就不比
		{"v0.7.14", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := IsNewerRelease(c.current, c.latest); got != c.want {
			t.Errorf("IsNewerRelease(%q,%q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// 本地构建绝不发起网络请求 —— 这既是隐私性质，也是"不对开发者说噪音话"的落实。
func TestLatestNeverQueriesForLocalBuilds(t *testing.T) {
	called := false
	c := newReleaseChecker()
	c.httpGet = func(context.Context, string) (*http.Response, error) {
		called = true
		return nil, io.EOF
	}
	if got, state := c.Latest("dev-b2535a0-dirty"); got.Tag != "" || state != ReleaseLocal {
		t.Errorf("Latest(local build) = %+v/%s, want empty/local", got, state)
	}
	if called {
		t.Error("本地构建不该发起任何网络请求")
	}
}

func TestFetchParsesRelease(t *testing.T) {
	c := newReleaseChecker()
	c.httpGet = func(context.Context, string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"tag_name":"v0.7.16","html_url":"https://github.com/x/y/releases/tag/v0.7.16"}`)),
		}, nil
	}
	got := c.fetch()
	if got.Tag != "v0.7.16" || got.URL == "" {
		t.Errorf("fetch() = %+v, want tag+url", got)
	}
}

// 失败必须返回空，绝不返回一个"看起来成功"的零值当作已是最新 —— UI 据此说"没查到"而不是 ✓。
func TestFetchFailsClosed(t *testing.T) {
	for name, get := range map[string]func(context.Context, string) (*http.Response, error){
		"network error": func(context.Context, string) (*http.Response, error) { return nil, io.EOF },
		"non-200": func(context.Context, string) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		"bad json": func(context.Context, string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json"))}, nil
		},
		"empty fields": func(context.Context, string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":""}`))}, nil
		},
	} {
		c := newReleaseChecker()
		c.httpGet = get
		if got := c.fetch(); got.Tag != "" || got.URL != "" {
			t.Errorf("%s: fetch() = %+v, want empty", name, got)
		}
	}
}

// 四态必须互斥且各自可达 —— 尤其 unknown 绝不能塌成 current（那会变成对着一次失败的查询
// 说"✓ 已是最新"，是一句自信的假话）。
func TestLatestFourStates(t *testing.T) {
	t.Run("unknown: 是发布版但还没有结果", func(t *testing.T) {
		c := newReleaseChecker()
		c.httpGet = func(context.Context, string) (*http.Response, error) { return nil, io.EOF }
		_, state := c.Latest("v0.7.14")
		if state != ReleaseUnknown {
			t.Errorf("state = %s, want unknown（绝不能是 current）", state)
		}
	})
	t.Run("current: 查到了且就是最新", func(t *testing.T) {
		c := newReleaseChecker()
		c.info = ReleaseInfo{Tag: "v0.7.14", URL: "u"}
		c.checkedAt = time.Now()
		_, state := c.Latest("v0.7.14")
		if state != ReleaseCurrent {
			t.Errorf("state = %s, want current", state)
		}
	})
	t.Run("outdated: 查到了更新的", func(t *testing.T) {
		c := newReleaseChecker()
		c.info = ReleaseInfo{Tag: "v0.7.16", URL: "u"}
		c.checkedAt = time.Now()
		info, state := c.Latest("v0.7.14")
		if state != ReleaseOutdated || info.Tag != "v0.7.16" || info.URL == "" {
			t.Errorf("= %+v/%s, want outdated + 非空 info", info, state)
		}
	})
	t.Run("local: 本地构建", func(t *testing.T) {
		c := newReleaseChecker()
		c.info = ReleaseInfo{Tag: "v9.9.9", URL: "u"} // 即使缓存里有更新的，也不对本地构建说
		c.checkedAt = time.Now()
		_, state := c.Latest("dev-b2535a0")
		if state != ReleaseLocal {
			t.Errorf("state = %s, want local", state)
		}
	})
}
