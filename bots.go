package terminal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	botPrecheckTimeout = 4 * time.Second
	botOutputCapBytes  = 16 * 1024
	botLiveCacheTTL    = 3 * time.Second
	scoutRoleTitle     = "终端值守与异常巡检幕僚"
	reviewRoleTitle    = "多仓变更与工作树守门幕僚"
)

// botsStoreMu serializes read-modify-write operations on <dataDir>/bots/<slug>/*.
// It is never held across external command execution in handleBotRunCheck / handleBotChat.
var botsStoreMu sync.Mutex

// BotItem is one triage card in a resident bot's board (Needs you / Working / Done).
type BotItem struct {
	ID                  string `json:"id"`
	Num                 int    `json:"num"`
	Status              string `json:"status"` // "needs_you" | "working" | "done"
	Origin              string `json:"origin"`
	Title               string `json:"title"`
	Question            string `json:"question"`
	TargetFile          string `json:"targetFile,omitempty"`
	BranchSlug          string `json:"branchSlug,omitempty"`
	SpawnedWorktreePath string `json:"spawnedWorktreePath,omitempty"`
	SpawnedTabID        string `json:"spawnedTabId,omitempty"`
	CreatedAt           string `json:"createdAt"`
	SessionID           string `json:"sessionId,omitempty"`
	RepoCWD             string `json:"repoCwd,omitempty"`
	Live                bool   `json:"live,omitempty"`
}

// BotTrigger describes a zero-token precheck trigger attached to a resident bot.
type BotTrigger struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Precheck         string `json:"precheck"`
	Mode             string `json:"mode"`
	Stats            string `json:"stats"`
	SkippedZeroToken int    `json:"skippedZeroToken"`
}

// BotThreadMessage is a single message inside a resident bot's conversation thread.
type BotThreadMessage struct {
	ID        string   `json:"id"`
	Role      string   `json:"role"` // "user" | "bot"
	Meta      string   `json:"meta,omitempty"`
	Text      string   `json:"text"`
	Files     []string `json:"files,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// BotThread is a conversation room inside a resident bot workspace (home or alert thread).
type BotThread struct {
	ID       string             `json:"id"`
	Title    string             `json:"title"`
	Kind     string             `json:"kind"` // "home" | "alert"
	Messages []BotThreadMessage `json:"messages"`
}

// BotDetail is the full wire representation returned by GET /bots and GET /bots/{id}.
type BotDetail struct {
	ID            string       `json:"id"`
	Slug          string       `json:"slug"`
	Name          string       `json:"name"`
	Emoji         string       `json:"emoji"`
	RoleTitle     string       `json:"roleTitle"`
	ProfileID     string       `json:"profileId"`
	RepoCWD       string       `json:"repoCwd"`
	NeedsYouCount int          `json:"needsYouCount"`
	WorkingCount  int          `json:"workingCount"`
	SoulMD        string       `json:"soulMd"`
	MemoryMD      string       `json:"memoryMd"`
	Triggers      []BotTrigger `json:"triggers"`
	Items         []BotItem    `json:"items"`
	Threads       []BotThread  `json:"threads"`
	itemOverrides map[string]botItemOverride
}

// botMetaFile is the on-disk schema of <dataDir>/bots/<slug>/bot.json.
type botMetaFile struct {
	ID            string                     `json:"id"`
	Slug          string                     `json:"slug"`
	Name          string                     `json:"name"`
	Emoji         string                     `json:"emoji"`
	RoleTitle     string                     `json:"roleTitle"`
	ProfileID     string                     `json:"profileId"`
	RepoCWD       string                     `json:"repoCwd"`
	Triggers      []BotTrigger               `json:"triggers"`
	ItemOverrides map[string]botItemOverride `json:"itemOverrides,omitempty"`
}

type botItemOverride struct {
	Status              string `json:"status,omitempty"`
	SpawnedWorktreePath string `json:"spawnedWorktreePath,omitempty"`
	SpawnedTabID        string `json:"spawnedTabId,omitempty"`
	UpdatedAt           string `json:"updatedAt"`
}

var validBotSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func isValidBotSlug(slug string) bool {
	return validBotSlugRE.MatchString(slug)
}

func (s *Server) botsRootDir() string {
	return filepath.Join(s.dataDir(), "bots")
}

func (s *Server) botDir(slug string) string {
	return filepath.Join(s.botsRootDir(), slug)
}

func (s *Server) defaultBotRepoCWD() string {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		return wd
	}
	return s.dataDir()
}

// ensureBotsSeededLocked creates default resident bots (scout & review-buddy) on first load
// when the bots directory does not yet contain any valid bot directories.
func (s *Server) ensureBotsSeededLocked() error {
	root := s.botsRootDir()
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && isValidBotSlug(e.Name()) {
				if _, statErr := os.Stat(filepath.Join(root, e.Name(), "bot.json")); statErr == nil {
					return nil
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	repoCWD := s.defaultBotRepoCWD()

	seeds := []BotDetail{
		{
			ID:        "scout",
			Slug:      "scout",
			Name:      "Scout",
			Emoji:     "🤖",
			RoleTitle: scoutRoleTitle,
			ProfileID: "default",
			RepoCWD:   repoCWD,
			SoulMD: `# SOUL.md · Scout
- 角色：只读检查本机终端会话，报告等待确认、异常退出和正在运行的 Agent。
- 只引用观察到的终端证据；巡检不向会话发送输入，也不修改仓库。
`,
			MemoryMD: "# MEMORY.md · Scout\n- 零 Token 预检与本机巡检均不调用模型。\n",
			Triggers: []BotTrigger{
				{
					ID:       "trig-ci-guard",
					Name:     "本机仓库状态预检",
					Precheck: "git status --porcelain | grep -q .",
					Mode:     "先跑只读检查，无变化不调模型",
					Stats:    "等待首次巡检",
				},
				{
					ID:               "trig-alert-lookaround",
					Name:             "本机提交记录预检",
					Precheck:         "git log -n 3 --oneline",
					Mode:             "轻量只读命令检查",
					Stats:            "等待首次巡检",
					SkippedZeroToken: 0,
				},
			},
			Items: []BotItem{},
		},
		{
			ID:        "review-buddy",
			Slug:      "review-buddy",
			Name:      "ReviewBuddy",
			Emoji:     "🛡️",
			RoleTitle: reviewRoleTitle,
			ProfileID: "default",
			RepoCWD:   repoCWD,
			SoulMD: `# SOUL.md · ReviewBuddy
- 角色：只读巡检本机会话涉及的 Git 仓库，报告未提交变更和独立工作树的状态。
- 巡检不提交、不合入，也不删除工作树。
`,
			MemoryMD: "# MEMORY.md · ReviewBuddy\n- 按 Git 主仓库聚合会话；保留独立工作树的路径。\n",
			Triggers: []BotTrigger{
				{
					ID:       "trig-pr-precheck",
					Name:     "本机变更 Diff 预检",
					Precheck: "git diff --stat | grep -q .",
					Mode:     "只读查看本地 Diff",
					Stats:    "等待首次巡检",
				},
			},
			Items: []BotItem{},
		},
	}

	for _, b := range seeds {
		home := botHomeThread(b.Slug)
		home.Messages = []BotThreadMessage{botWelcomeMessage(b.Slug, now)}
		b.Threads = []BotThread{home}
		if err := s.saveBotLocked(b); err != nil {
			return err
		}
	}
	return nil
}

func botHomeThread(slug string) BotThread {
	id := slug + "-home"
	if slug == "review-buddy" {
		id = "rb-home"
	}
	return BotThread{ID: id, Title: "🏠 幕僚主对话室 (Home)", Kind: "home", Messages: []BotThreadMessage{}}
}

func botWelcomeMessage(slug, now string) BotThreadMessage {
	text := "我只读检查本机终端会话，报告等待确认、异常退出和正在运行的 Agent。点击巡检可获取当前状态。"
	if slug == "review-buddy" {
		text = "我只读检查本机会话涉及的多个 Git 仓库，报告未提交变更与独立工作树。点击巡检查看当前状态，不会提交或合入代码。"
	}
	return BotThreadMessage{ID: uuid.NewString(), Role: "bot", Text: text, CreatedAt: now}
}

func normalizeBotDetail(b *BotDetail) {
	if b.Triggers == nil {
		b.Triggers = []BotTrigger{}
	}
	if b.Items == nil {
		b.Items = []BotItem{}
	}
	if b.Threads == nil {
		b.Threads = []BotThread{}
	}
	for i := range b.Threads {
		if b.Threads[i].Messages == nil {
			b.Threads[i].Messages = []BotThreadMessage{}
		}
	}
	needsYou := 0
	working := 0
	for _, it := range b.Items {
		switch it.Status {
		case "needs_you":
			needsYou++
		case "working":
			working++
		}
	}
	b.NeedsYouCount = needsYou
	b.WorkingCount = working
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(perm)
	}
	if err := errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *Server) saveBotLocked(b BotDetail) error {
	normalizeBotDetail(&b)
	dir := s.botDir(b.Slug)
	if err := s.saveBotMetaLocked(b); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "SOUL.md"), []byte(b.SoulMD), 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "MEMORY.md"), []byte(b.MemoryMD), 0o644); err != nil {
		return err
	}
	if err := s.saveBotItemsLocked(b.Slug, b.Items); err != nil {
		return err
	}
	return s.saveBotThreadsLocked(b.Slug, b.Threads)
}

func (s *Server) saveBotMetaLocked(b BotDetail) error {
	normalizeBotDetail(&b)
	meta := botMetaFile{
		ID:            b.ID,
		Slug:          b.Slug,
		Name:          b.Name,
		Emoji:         b.Emoji,
		RoleTitle:     b.RoleTitle,
		ProfileID:     b.ProfileID,
		RepoCWD:       b.RepoCWD,
		Triggers:      b.Triggers,
		ItemOverrides: b.itemOverrides,
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.botDir(b.Slug), "bot.json"), raw, 0o644)
}

func (s *Server) saveBotItemsLocked(slug string, items []BotItem) error {
	if items == nil {
		items = []BotItem{}
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.botDir(slug), "items.json"), raw, 0o644)
}

func (s *Server) saveBotThreadsLocked(slug string, threads []BotThread) error {
	if threads == nil {
		threads = []BotThread{}
	}
	raw, err := json.MarshalIndent(threads, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.botDir(slug), "threads.json"), raw, 0o644)
}

func readBotJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		logger.Warn("invalid bot JSON", "path", path, "error", err)
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (s *Server) loadBotLocked(slug string) (BotDetail, error) {
	if !isValidBotSlug(slug) {
		return BotDetail{}, os.ErrNotExist
	}
	dir := s.botDir(slug)
	var meta *botMetaFile
	if err := readBotJSON(filepath.Join(dir, "bot.json"), &meta); err != nil {
		return BotDetail{}, err
	}
	if meta == nil {
		return BotDetail{}, errors.New("decode bot.json: expected a bot object")
	}
	if meta.Slug == "" {
		meta.Slug = slug
	} else if meta.Slug != slug {
		return BotDetail{}, fmt.Errorf("bot slug %q does not match directory %q", meta.Slug, slug)
	}
	if meta.ID == "" {
		meta.ID = meta.Slug
	}

	soulRaw, err := os.ReadFile(filepath.Join(dir, "SOUL.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return BotDetail{}, err
	}
	memRaw, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return BotDetail{}, err
	}

	var items []BotItem
	if err := readBotJSON(filepath.Join(dir, "items.json"), &items); err != nil && !errors.Is(err, os.ErrNotExist) {
		return BotDetail{}, err
	}
	var threads []BotThread
	if err := readBotJSON(filepath.Join(dir, "threads.json"), &threads); err != nil && !errors.Is(err, os.ErrNotExist) {
		return BotDetail{}, err
	}

	b := BotDetail{
		ID:            meta.ID,
		Slug:          meta.Slug,
		Name:          meta.Name,
		Emoji:         meta.Emoji,
		RoleTitle:     meta.RoleTitle,
		ProfileID:     meta.ProfileID,
		RepoCWD:       meta.RepoCWD,
		SoulMD:        string(soulRaw),
		MemoryMD:      string(memRaw),
		Triggers:      meta.Triggers,
		Items:         items,
		Threads:       threads,
		itemOverrides: meta.ItemOverrides,
	}
	if sanitizeLegacyDemoSeedLocked(&b) {
		if err := s.saveBotLocked(b); err != nil {
			return BotDetail{}, err
		}
	}
	normalizeBotDetail(&b)
	return b, nil
}

func sanitizeLegacyDemoSeedLocked(bot *BotDetail) (healed bool) {
	hasDemoMarker := func(text string) bool {
		for _, marker := range []string{
			"[示例预诊]",
			"#inc-208",
			"GitHub Check #419",
			"iOS Safari 软键盘",
			"iOS Safari 表单提交按钮",
			"只读巡检发现异常信号 (",
			"已定位疑似回归点，是否立即派生独立 CoW 工作树接管修复？",
		} {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}
	demoThreads := make(map[string]bool)
	items := bot.Items[:0]
	for _, item := range bot.Items {
		if hasDemoMarker(strings.Join([]string{item.Origin, item.Title, item.Question, item.TargetFile, item.BranchSlug}, "\n")) {
			if number, ok := strings.CutPrefix(item.ID, "item-"); ok {
				demoThreads[bot.Slug+"-alert-"+number] = true
			}
			delete(bot.itemOverrides, item.ID)
			healed = true
			continue
		}
		items = append(items, item)
	}
	bot.Items = items

	threads := bot.Threads[:0]
	for _, thread := range bot.Threads {
		if thread.Kind == "alert" {
			demo := demoThreads[thread.ID] || hasDemoMarker(thread.Title)
			for _, msg := range thread.Messages {
				demo = demo || hasDemoMarker(msg.Text) || hasDemoMarker(msg.Meta)
			}
			if demo {
				healed = true
				continue
			}
		}
		if thread.Kind == "home" {
			messages := thread.Messages[:0]
			for _, msg := range thread.Messages {
				if (msg.Role == "bot" && (strings.Contains(msg.Text, "看板中的 [示例预诊]") || strings.Contains(msg.Text, "待命中！可发 `/check git diff --stat`"))) ||
					msg.Text == "Scout，帮我盯住线上告警与主干回归；绿灯抖动静默过滤，真要改代码时准备好定位卡等我拍板。" {
					continue
				}
				messages = append(messages, msg)
			}
			if len(messages) != len(thread.Messages) {
				healed = true
				if len(messages) == 0 {
					messages = append(messages, botWelcomeMessage(bot.Slug, time.Now().UTC().Format(time.RFC3339)))
				}
				thread.Messages = messages
			}
		}
		threads = append(threads, thread)
	}
	bot.Threads = threads

	lines := strings.Split(bot.MemoryMD, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if hasDemoMarker(line) {
			healed = true
			continue
		}
		kept = append(kept, line)
	}
	bot.MemoryMD = strings.Join(kept, "\n")

	var oldRole, newRole string
	switch bot.Slug {
	case "scout":
		oldRole, newRole = "线上告警与回归哨兵", scoutRoleTitle
	case "review-buddy":
		oldRole, newRole = "PR 与架构守门幕僚", reviewRoleTitle
	}
	if oldRole != "" && bot.RoleTitle == oldRole {
		bot.RoleTitle = newRole
		bot.SoulMD = strings.Replace(bot.SoulMD, "("+oldRole+")", "("+newRole+")", 1)
		bot.SoulMD = strings.Replace(bot.SoulMD, "- 角色：7×24 盯住线上稳定性告警与主干回归，过滤噪音，只在需要代码决策时呈交高信噪比预诊卡。", "- 角色：只读巡检本机终端会话，报告等待确认、异常退出与运行中的 Agent。", 1)
		healed = true
	}
	for index := range bot.Triggers {
		trigger := &bot.Triggers[index]
		switch trigger.Name {
		case "每 15 分钟主干 CI 失败守卫":
			trigger.Name = "本机仓库状态预检"
			healed = true
		case "线上告警 Look-around 只读分诊":
			trigger.Name = "本机提交记录预检"
			healed = true
		case "工作日待审变更 Diff 预检":
			trigger.Name, trigger.Mode = "本机变更 Diff 预检", "只读查看本地 Diff"
			healed = true
		}
		switch trigger.Mode {
		case "零 Token 门控 + 只读定位", "零 Token 门控":
			trigger.Mode = "先跑只读检查，无变化不调模型"
			healed = true
		case "Look-around 词法级只读沙箱", "Look-around 只读沙箱":
			trigger.Mode = "轻量只读命令检查"
			healed = true
		}
	}
	return healed
}

type botLiveFinding struct {
	text  string
	files []string
}

type botLiveSnapshot struct {
	sessionCount, repoCount int
	scout, review           []BotItem
	findings                []botLiveFinding
	complete                bool
}

type botLiveCache struct {
	mu       sync.Mutex
	at       time.Time
	fallback string
	snapshot botLiveSnapshot
}

func (s *Server) liveBots(ctx context.Context, fallback string, forceRefresh bool) botLiveSnapshot {
	var sessions []*Session
	if s.mgr != nil {
		sessions = s.mgr.List()
	}
	if len(sessions) != 0 || (strings.HasSuffix(os.Args[0], ".test") && !sameOrSubPath(fallback, os.TempDir())) {
		fallback = ""
	}
	cache := &s.botLiveCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !forceRefresh && cache.fallback == fallback && cache.snapshot.sessionCount == len(sessions) && time.Since(cache.at) < botLiveCacheTTL {
		return cache.snapshot
	}
	ctx, cancel := context.WithTimeout(ctx, worktreeOpTimeout)
	defer cancel()
	snapshot := s.inspectLiveBots(ctx, sessions, fallback, forceRefresh)
	if snapshot.complete {
		cache.at, cache.fallback, cache.snapshot = time.Now(), fallback, snapshot
	}
	return snapshot
}

func (s *Server) inspectLiveBots(ctx context.Context, sessions []*Session, fallback string, forceRefresh bool) botLiveSnapshot {
	snapshot := botLiveSnapshot{sessionCount: len(sessions), complete: true}
	var overview []SessionOverviewEntry
	if len(sessions) > 0 && s.sessionAgent != nil {
		ovBudget := 1500 * time.Millisecond
		if forceRefresh {
			ovBudget = 5 * time.Second
		}
		ovCtx, ovCancel := context.WithTimeout(ctx, ovBudget)
		overview = s.overviewSnapshot(ovCtx).entries
		ovCancel()
	}
	entries := make(map[string]SessionOverviewEntry, len(overview))
	for _, entry := range overview {
		entries[entry.ID] = entry
	}
	cwds := make([]sessionWorktreeCWD, 0, len(sessions)+1)
	if fallback != "" {
		cwds = append(cwds, sessionWorktreeCWD{cwd: fallback})
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, sess := range sessions {
		cwd := sessionWorktreeDir(sess)
		cwds = append(cwds, sessionWorktreeCWD{id: sess.ID, cwd: cwd})
		entry, observed := entries[sess.ID]
		if !observed {
			sess.mu.Lock()
			entry.Title, entry.CWD, entry.Engine = sessionTitle(sess), cwd, sess.Engine
			sess.mu.Unlock()
			if sig, _, _, ok := sess.PendingSignal(); ok {
				entry.AwaitingUser = true
				entry.StatusEvidence = sanitizeFieldMax(strings.TrimSpace(sig.Title+" "+sig.Body), 120)
			}
			if s.sessionAgent != nil {
				snapshot.complete = false
			}
		}
		if item, finding, ok := inspectBotSession(sess, entry, now); ok {
			snapshot.scout = append(snapshot.scout, item)
			snapshot.findings = append(snapshot.findings, finding)
		}
	}
	repos, complete := s.collectWorktreeRepos(ctx, cwds)
	snapshot.complete = snapshot.complete && complete
	snapshot.repoCount = len(repos)
	for _, repo := range repos {
		primary := repo.Worktrees[0]
		if primary.Dirty {
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			out, err := runLocalWorktreeGit(probeCtx, repo.Root, "status", "--porcelain=v1", "-z")
			cancel()
			if err != nil {
				snapshot.complete = false
			} else if changes := parseStatusZ(out); len(changes) > 0 {
				item, finding := botDirtyRepoItem(repo, changes, now)
				snapshot.review = append(snapshot.review, item)
				snapshot.findings = append(snapshot.findings, finding)
			}
		}
		for _, wt := range repo.Worktrees {
			snapshot.complete = snapshot.complete && !wt.statusUnreadable
			if wt.IsPrimary {
				continue
			}
			branchName := firstTrimmedNonempty(wt.Branch, filepath.Base(wt.Path))
			wtHasSession := false
			for _, source := range cwds {
				if source.id != "" && sameOrSubPath(source.cwd, wt.Path) {
					wtHasSession = true
					break
				}
			}
			var status, title, question string
			switch {
			case wt.Merged:
				status = "done"
				title = fmt.Sprintf("分支目录 %s 已合入主分支", branchName)
				question = fmt.Sprintf("目录：%s · 已合入主分支，可在左栏安全移除。", wt.Path)
			case wt.Dirty:
				status = "needs_you"
				title = fmt.Sprintf("分支目录 %s 有未提交变更", branchName)
				question = fmt.Sprintf("目录：%s · 存在未提交改动，可进入终端整理或合入。", wt.Path)
			case wtHasSession:
				status = "working"
				title = fmt.Sprintf("分支目录 %s 正在使用中", branchName)
				question = wt.Path
			default:
				continue
			}
			snapshot.review = append(snapshot.review, BotItem{
				ID: "live-wt-" + shortHash(wt.Path), Status: status, Live: true, RepoCWD: wt.Path,
				SpawnedWorktreePath: wt.Path, Origin: repo.Name + " · 分支目录", CreatedAt: now,
				Title: title, Question: question,
			})
			if wt.Dirty {
				snapshot.findings = append(snapshot.findings, botLiveFinding{
					text: fmt.Sprintf("%s 的分支目录 %s 有未提交变更：%s", repo.Name, branchName, wt.Path),
				})
			}
		}
	}
	return snapshot
}

func inspectBotSession(sess *Session, entry SessionOverviewEntry, now string) (BotItem, botLiveFinding, bool) {
	sess.mu.Lock()
	exited, exitCode, buffer, createdAt := sess.Status == StatusExited, sess.exitCode, sess.Buffer, sess.CreatedAt
	sess.mu.Unlock()
	if !createdAt.IsZero() {
		now = createdAt.UTC().Format(time.RFC3339)
	}
	name := firstTrimmedNonempty(entry.Title, sess.ID)
	titleSlug := firstTrimmedNonempty(sanitizeSlugToken(name), "session-"+shortHash(sess.ID))
	item := BotItem{SessionID: sess.ID, RepoCWD: entry.CWD, Live: true, CreatedAt: now, BranchSlug: "task/" + titleSlug}
	tool := firstTrimmedNonempty(string(entry.AgentTool), entry.Engine, "shell")
	rawTail := func() string {
		if buffer == nil {
			return ""
		}
		return botTailSnippet(strings.Split(stripANSIForTail(string(buffer.ReadTail(4096))), "\n"))
	}
	var evidence, detail string
	switch {
	case exited && exitCode != 0:
		item.ID, item.Status = "live-exit-"+sess.ID, "needs_you"
		item.Origin = fmt.Sprintf("异常退出 · exit=%d", exitCode)
		item.Title = fmt.Sprintf("会话「%s」异常退出 (exit %d)", name, exitCode)
		evidence = rawTail()
		item.Question = firstTrimmedNonempty(evidence, "终端无可用的退出输出。")
	case exited:
		return BotItem{}, botLiveFinding{}, false
	case entry.AgentStatus == "waiting" || entry.AwaitingUser:
		item.ID, item.Status = "live-wait-"+sess.ID, "needs_you"
		item.Origin = "实时会话 · " + tool
		item.Title = fmt.Sprintf("会话「%s」等待确认或输入", name)
		evidence = firstTrimmedNonempty(entry.StatusEvidence, botTailSnippet(entry.Tail))
		if evidence == "" {
			evidence = rawTail()
		}
		item.Question = firstTrimmedNonempty(evidence, "终端无可用的等待提示。")
		if entry.CWD != "" {
			item.Question += "\n目录：" + entry.CWD
		}
	case entry.AgentStatus == "running":
		item.ID, item.Status = "live-run-"+sess.ID, "working"
		item.Origin = "实时运行中 · " + tool
		if entry.CWD != "" {
			detail = " (" + filepath.Base(entry.CWD) + ")"
		}
		item.Title = fmt.Sprintf("会话「%s」正在后台执行%s", name, detail)
		evidence = botTailSnippet(entry.Tail)
		item.Question = firstTrimmedNonempty(evidence, "Agent 正在持续运行中，可一键直达会话查看实时终端输出。")
	default:
		return BotItem{}, botLiveFinding{}, false
	}
	refs := extractFileRefs(evidence+"\n"+botTailSnippet(entry.Tail), entry.CWD)
	if len(refs) != 0 {
		item.TargetFile = refs[0]
		if item.RepoCWD != "" {
			for index, ref := range refs {
				if !filepath.IsAbs(ref) {
					refs[index] = filepath.Join(item.RepoCWD, ref)
				}
			}
		}
	}
	return item, botLiveFinding{text: item.Title, files: refs}, true
}

func botTailSnippet(lines []string) string {
	var tail []string
	for index := len(lines) - 1; index >= 0 && len(tail) < 3; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			tail = append([]string{clipUTF8(line, 240)}, tail...)
		}
	}
	return strings.Join(tail, "\n")
}

func shortHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:8])
}

func botDirtyRepoItem(repo worktreeRepo, changes []changed, now string) (BotItem, botLiveFinding) {
	branch := firstTrimmedNonempty(repo.Worktrees[0].Branch, "detached HEAD")
	var topFiles, refs []string
	for _, change := range changes[:min(4, len(changes))] {
		topFiles = append(topFiles, change.path)
		refs = append(refs, filepath.Join(repo.Root, change.path))
	}
	more := ""
	if len(changes) > len(topFiles) {
		more = fmt.Sprintf(" 等 %d 个文件", len(changes))
	}
	title := fmt.Sprintf("%s (%s) 有 %d 处未提交变更", repo.Name, branch, len(changes))
	files := strings.Join(topFiles, "、") + more
	item := BotItem{
		ID: "live-dirty-" + shortHash(repo.Root), Status: "needs_you", Live: true, RepoCWD: repo.Root,
		SessionID: repo.firstSessionID, Origin: fmt.Sprintf("Git 仓库 · %s (%d 会话)", branch, repo.SessionCount),
		Title: title, Question: "变更文件：" + files + "。可点下方文件名预览，或进入该仓库终端整理提交。",
		TargetFile: refs[0], BranchSlug: "review/" + firstTrimmedNonempty(sanitizeSlugToken(repo.Name), "repo-"+shortHash(repo.Root)) + "-wip",
		CreatedAt: now,
	}
	return item, botLiveFinding{text: title + "：" + files, files: refs}
}

func (snapshot botLiveSnapshot) summary() (string, []string) {
	var text strings.Builder
	fmt.Fprintf(&text, "本机只读巡检：%d 会话 / %d 仓库。", snapshot.sessionCount, snapshot.repoCount)
	if !snapshot.complete {
		text.WriteString("部分状态未能读取，以下仅列出已确认的结果。")
	} else if len(snapshot.findings) == 0 {
		text.WriteString("当前无挂起异常会话与未提交变更。")
	}
	var files []string
	seen := make(map[string]bool)
	for _, finding := range snapshot.findings {
		text.WriteString("\n- " + finding.text)
		for _, ref := range finding.files {
			if !seen[ref] && len(files) < 12 {
				files = append(files, ref)
				seen[ref] = true
			}
		}
	}
	return text.String(), files
}

func (snapshot botLiveSnapshot) enrich(bot BotDetail) BotDetail {
	var live []BotItem
	switch bot.Slug {
	case "scout":
		live = snapshot.scout
	case "review-buddy":
		live = snapshot.review
	}
	items := make([]BotItem, 0, len(live)+len(bot.Items))
	seen := make(map[string]bool)
	for _, source := range [][]BotItem{live, bot.Items} {
		for _, item := range source {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			if override, exists := bot.itemOverrides[item.ID]; exists {
				if override.Status != "" {
					item.Status = override.Status
				}
				if override.SpawnedWorktreePath != "" {
					item.SpawnedWorktreePath = override.SpawnedWorktreePath
				}
				if override.SpawnedTabID != "" {
					item.SpawnedTabID = override.SpawnedTabID
				}
			}
			items = append(items, item)
		}
	}
	bot.Items = items
	used := make(map[int]bool)
	for _, item := range items {
		used[item.Num] = true
	}
	nextNum := 1
	for index := range items {
		if items[index].Num == 0 {
			for used[nextNum] {
				nextNum++
			}
			items[index].Num = nextNum
			nextNum++
		}
	}
	bot.Triggers = append([]BotTrigger{}, bot.Triggers...)
	for index := range bot.Triggers {
		if bot.Triggers[index].Stats == "等待首次巡检" {
			bot.Triggers[index].Stats = fmt.Sprintf("实时巡检 · %d 会话 / %d 仓库", snapshot.sessionCount, snapshot.repoCount)
		}
	}
	normalizeBotDetail(&bot)
	return bot
}

func (s *Server) loadAllBotsLocked() ([]BotDetail, error) {
	if err := s.ensureBotsSeededLocked(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.botsRootDir())
	if err != nil {
		return nil, err
	}
	bots := make([]BotDetail, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !isValidBotSlug(e.Name()) {
			continue
		}
		b, err := s.loadBotLocked(e.Name())
		if err != nil {
			continue
		}
		bots = append(bots, b)
	}
	sort.Slice(bots, func(i, j int) bool {
		pi := botOrderRank(bots[i].Slug)
		pj := botOrderRank(bots[j].Slug)
		if pi != pj {
			return pi < pj
		}
		return bots[i].Slug < bots[j].Slug
	})
	return bots, nil
}

func botOrderRank(slug string) int {
	switch slug {
	case "scout":
		return 0
	case "review-buddy":
		return 1
	default:
		return 2
	}
}

// resolveBotLocked ensures seeding and finds a bot by slug or ID.
func (s *Server) resolveBotLocked(id string) (BotDetail, error) {
	if err := s.ensureBotsSeededLocked(); err != nil {
		return BotDetail{}, err
	}
	if isValidBotSlug(id) {
		if bot, err := s.loadBotLocked(id); !errors.Is(err, os.ErrNotExist) {
			return bot, err
		}
	}
	bots, err := s.loadAllBotsLocked()
	if err != nil {
		return BotDetail{}, err
	}
	for _, b := range bots {
		if b.ID == id || b.Slug == id {
			return b, nil
		}
	}
	return BotDetail{}, os.ErrNotExist
}

// handleListBots handles GET /bots.
func (s *Server) handleListBots(w http.ResponseWriter, r *http.Request) {
	botsStoreMu.Lock()
	bots, err := s.loadAllBotsLocked()
	botsStoreMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(bots) > 0 {
		fallback := bots[0].RepoCWD
		for _, bot := range bots {
			if bot.Slug == "review-buddy" {
				fallback = bot.RepoCWD
				break
			}
		}
		snapshot := s.liveBots(r.Context(), fallback, false)
		for index, bot := range bots {
			bots[index] = snapshot.enrich(bot)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
}

// handleGetBot handles GET /bots/{id}.
func (s *Server) handleGetBot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isValidBotSlug(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bot id"})
		return
	}
	botsStoreMu.Lock()
	bot, err := s.resolveBotLocked(id)
	botsStoreMu.Unlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.liveBots(r.Context(), bot.RepoCWD, false).enrich(bot))
}

// handleUpdateBotDocs handles PUT /bots/{id}/docs.
func (s *Server) handleUpdateBotDocs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isValidBotSlug(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bot id"})
		return
	}
	var req struct {
		SoulMD   *string `json:"soulMd"`
		MemoryMD *string `json:"memoryMd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	bot, err := s.updateBotDocs(id, req.SoulMD, req.MemoryMD)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.liveBots(r.Context(), bot.RepoCWD, false).enrich(bot))
}

func (s *Server) updateBotDocs(id string, soul, memory *string) (BotDetail, error) {
	botsStoreMu.Lock()
	defer botsStoreMu.Unlock()
	bot, err := s.resolveBotLocked(id)
	if err != nil {
		return BotDetail{}, err
	}
	if soul != nil {
		bot.SoulMD = *soul
		if err := writeFileAtomic(filepath.Join(s.botDir(bot.Slug), "SOUL.md"), []byte(bot.SoulMD), 0o644); err != nil {
			return BotDetail{}, err
		}
	}
	if memory != nil {
		bot.MemoryMD = *memory
		if err := writeFileAtomic(filepath.Join(s.botDir(bot.Slug), "MEMORY.md"), []byte(bot.MemoryMD), 0o644); err != nil {
			return BotDetail{}, err
		}
	}
	return bot, nil
}

// handleBotChat handles POST /bots/{id}/chat.
func (s *Server) handleBotChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isValidBotSlug(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bot id"})
		return
	}
	var req struct {
		ThreadID string `json:"threadId"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	msgText := strings.TrimSpace(req.Message)
	if msgText == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}

	// Gate /check commands BEFORE acquiring the store lock or touching disk.
	var checkCmd string
	isCheck := false
	if msgText == "/check" || strings.HasPrefix(msgText, "/check ") {
		isCheck = true
		checkCmd = strings.TrimSpace(strings.TrimPrefix(msgText, "/check"))
		if err := ValidateReadOnlyCommand(checkCmd); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   err.Error(),
				"blocked": true,
			})
			return
		}
	}

	// Resolve bot metadata (repoCwd) first.
	botsStoreMu.Lock()
	bot, err := s.resolveBotLocked(id)
	botsStoreMu.Unlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var checkOutput string
	var checkExit int
	if isCheck {
		checkExit, checkOutput = runValidatedReadOnlyCommand(r.Context(), bot.RepoCWD, checkCmd)
	}
	snapshot := s.liveBots(r.Context(), bot.RepoCWD, false)
	var fileNotes []botLiveFinding
	if !isCheck {
		fileNotes = botReferencedFileNotes(bot.RepoCWD, msgText)
	}

	botsStoreMu.Lock()
	defer botsStoreMu.Unlock()

	bot, err = s.resolveBotLocked(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
		return
	}

	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)

	userFiles := extractFileRefs(msgText, bot.RepoCWD)
	var replyMeta, replyText string
	var replyFiles []string
	if isCheck {
		replyMeta = fmt.Sprintf("%s · 轻量只读命令检查 · exit=%d · 0 文件写入", bot.Name, checkExit)
		if checkOutput == "" {
			replyText = fmt.Sprintf("已执行只读检查 `%s`（退出码 %d），当前无输出。", checkCmd, checkExit)
		} else {
			replyText = fmt.Sprintf("已执行只读检查 `%s`（退出码 %d）：\n```\n%s\n```", checkCmd, checkExit, checkOutput)
		}
		replyFiles = extractFileRefs(checkOutput, bot.RepoCWD)
	} else {
		replyMeta = bot.Name + " · 本机只读巡检"
		replyText, replyFiles = snapshot.summary()
		for _, note := range fileNotes {
			replyText += "\n- " + note.text
			for _, ref := range note.files {
				if !slices.Contains(replyFiles, ref) {
					replyFiles = append(replyFiles, ref)
				}
			}
		}
	}

	idx := findBotThreadIndex(bot.Threads, req.ThreadID)
	if idx < 0 {
		idx = ensureBotHome(&bot)
	}

	bot.Threads[idx].Messages = append(bot.Threads[idx].Messages,
		BotThreadMessage{
			ID:        uuid.NewString(),
			Role:      "user",
			Text:      msgText,
			Files:     userFiles,
			CreatedAt: ts,
		},
		BotThreadMessage{
			ID:        uuid.NewString(),
			Role:      "bot",
			Meta:      replyMeta,
			Text:      replyText,
			Files:     replyFiles,
			CreatedAt: ts,
		},
	)

	if err := s.saveBotThreadsLocked(bot.Slug, bot.Threads); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": snapshot.enrich(bot)})
}

func botReferencedFileNotes(cwd, message string) []botLiveFinding {
	if !filepath.IsAbs(cwd) || !strings.Contains(message, "@") {
		return nil
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return nil
	}
	defer root.Close()
	var notes []botLiveFinding
	seen := make(map[string]bool)
	for _, match := range fileRefRE.FindAllString(message, -1) {
		if !strings.HasPrefix(match, "@") {
			continue
		}
		ref := normalizeFileRef(match, cwd)
		if seen[ref] {
			continue
		}
		seen[ref] = true
		path, position, _ := strings.Cut(ref, ":")
		path = filepath.Clean(path)
		if !filepath.IsLocal(path) {
			continue
		}
		if note, ok := summarizeBotFile(root, path, position); ok {
			notes = append(notes, botLiveFinding{text: note, files: []string{ref}})
			if len(notes) == 4 {
				break
			}
		}
	}
	return notes
}

func summarizeBotFile(root *os.Root, path, position string) (string, bool) {
	if info, err := root.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	const limit = 16 << 10
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", false
	}
	if len(raw) > limit {
		return fmt.Sprintf("@%s：%d 字节，仅检查了文件开头，未回显内容。", path, info.Size()), true
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return fmt.Sprintf("@%s：%d 字节，不是 UTF-8 文本。", path, info.Size()), true
	}
	lines := bytes.Count(raw, []byte{'\n'})
	if len(raw) != 0 && raw[len(raw)-1] != '\n' {
		lines++
	}
	note := fmt.Sprintf("@%s：UTF-8 文本，%d 行 / %d 字节。", path, lines, len(raw))
	if lineText, _, _ := strings.Cut(position, ":"); lineText != "" {
		if line, err := strconv.Atoi(lineText); err == nil && line > 0 {
			if line <= lines {
				note += fmt.Sprintf("第 %d 行存在。", line)
			} else {
				note += fmt.Sprintf("第 %d 行超出文件范围。", line)
			}
		}
	}
	return note, true
}

func findBotThreadIndex(threads []BotThread, threadID string) int {
	if threadID != "" {
		for i := range threads {
			if threads[i].ID == threadID {
				return i
			}
		}
	}
	for i := range threads {
		if threads[i].Kind == "home" {
			return i
		}
	}
	return -1
}

func ensureBotHome(bot *BotDetail) int {
	if idx := findBotThreadIndex(bot.Threads, ""); idx >= 0 {
		return idx
	}
	bot.Threads = append(bot.Threads, botHomeThread(bot.Slug))
	return len(bot.Threads) - 1
}

type botSimulateAlertReq struct {
	Origin     string `json:"origin"`
	Title      string `json:"title"`
	Question   string `json:"question"`
	TargetFile string `json:"targetFile,omitempty"`
	BranchSlug string `json:"branchSlug,omitempty"`
}

// handleBotRunCheck handles POST /bots/{id}/run-check.
func (s *Server) handleBotRunCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isValidBotSlug(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bot id"})
		return
	}
	var req struct {
		TriggerID     string               `json:"triggerId"`
		PrecheckCmd   string               `json:"precheckCmd"`
		SimulateAlert *botSimulateAlertReq `json:"simulateAlert"`
		Patrol        bool                 `json:"patrol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	precheckCmd := strings.TrimSpace(req.PrecheckCmd)
	if req.PrecheckCmd != "" {
		if err := ValidateReadOnlyCommand(precheckCmd); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "blocked": true})
			return
		}
	}

	botsStoreMu.Lock()
	bot, err := s.resolveBotLocked(id)
	botsStoreMu.Unlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if req.Patrol && req.PrecheckCmd == "" && req.SimulateAlert == nil {
		s.runBotPatrol(w, r, bot, req.TriggerID)
		return
	}

	// Resolve effective precheck command.
	trigIdx := -1
	if req.TriggerID != "" {
		for i := range bot.Triggers {
			if bot.Triggers[i].ID == req.TriggerID {
				trigIdx = i
				if req.PrecheckCmd == "" {
					precheckCmd = strings.TrimSpace(bot.Triggers[i].Precheck)
				}
				break
			}
		}
	}
	if req.PrecheckCmd == "" && precheckCmd == "" && req.SimulateAlert == nil && len(bot.Triggers) > 0 {
		trigIdx = 0
		precheckCmd = strings.TrimSpace(bot.Triggers[0].Precheck)
	}

	// Always validate precheckCmd whenever provided OR whenever running a real check.
	if req.PrecheckCmd == "" && (precheckCmd != "" || req.SimulateAlert == nil) {
		if err := ValidateReadOnlyCommand(precheckCmd); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   err.Error(),
				"blocked": true,
			})
			return
		}
	}

	// Path A: simulated alert → create a Needs you BotItem + alert thread.
	if req.SimulateAlert != nil {
		snapshot := s.liveBots(r.Context(), bot.RepoCWD, false)
		botsStoreMu.Lock()
		defer botsStoreMu.Unlock()

		bot, err = s.resolveBotLocked(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}

		nextNum := 101
		for _, it := range bot.Items {
			if it.Num >= nextNum {
				nextNum = it.Num + 1
			}
		}
		now := time.Now().UTC()
		ts := now.Format(time.RFC3339)

		origin := strings.TrimSpace(req.SimulateAlert.Origin)
		if origin == "" {
			origin = "模拟告警触发"
		}
		title := strings.TrimSpace(req.SimulateAlert.Title)
		if title == "" {
			title = "发现需要确认的异常信号"
		}
		question := strings.TrimSpace(req.SimulateAlert.Question)
		if question == "" {
			question = "这是手动创建的模拟告警，未执行实际巡检。"
		}

		newItem := BotItem{
			ID:         fmt.Sprintf("item-%d", nextNum),
			Num:        nextNum,
			Status:     "needs_you",
			Origin:     origin,
			Title:      title,
			Question:   question,
			TargetFile: strings.TrimSpace(req.SimulateAlert.TargetFile),
			BranchSlug: strings.TrimSpace(req.SimulateAlert.BranchSlug),
			CreatedAt:  ts,
		}
		bot.Items = append([]BotItem{newItem}, bot.Items...)

		var files []string
		if newItem.TargetFile != "" {
			files = []string{newItem.TargetFile}
		}
		alertThread := BotThread{
			ID:    fmt.Sprintf("%s-alert-%d", bot.Slug, nextNum),
			Title: fmt.Sprintf("🔔 告警分诊 #%d", nextNum),
			Kind:  "alert",
			Messages: []BotThreadMessage{
				{
					ID:        uuid.NewString(),
					Role:      "user",
					Text:      fmt.Sprintf("[%s] %s", origin, title),
					Files:     files,
					CreatedAt: ts,
				},
				{
					ID:        uuid.NewString(),
					Role:      "bot",
					Meta:      fmt.Sprintf("%s · 轻量只读命令检查 · 生成预诊单 #%d", bot.Name, nextNum),
					Text:      question,
					Files:     files,
					CreatedAt: ts,
				},
			},
		}
		bot.Threads = append(bot.Threads, alertThread)

		if err := s.saveBotItemsLocked(bot.Slug, bot.Items); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.saveBotThreadsLocked(bot.Slug, bot.Threads); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"skippedZeroToken": false,
			"item":             newItem,
			"bot":              snapshot.enrich(bot),
		})
		return
	}

	// Path B: real zero-token read-only precheck execution (outside lock, 4s timeout).
	exitCode, output := runValidatedReadOnlyCommand(r.Context(), bot.RepoCWD, precheckCmd)
	snapshot := s.liveBots(r.Context(), bot.RepoCWD, false)

	botsStoreMu.Lock()
	defer botsStoreMu.Unlock()

	bot, err = s.resolveBotLocked(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
		return
	}

	// Re-resolve trigger index against freshly loaded bot.
	resolvedTrigIdx := -1
	if req.TriggerID != "" {
		for i := range bot.Triggers {
			if bot.Triggers[i].ID == req.TriggerID {
				resolvedTrigIdx = i
				break
			}
		}
	}
	if resolvedTrigIdx < 0 && trigIdx >= 0 && trigIdx < len(bot.Triggers) {
		resolvedTrigIdx = trigIdx
	}
	if resolvedTrigIdx < 0 && len(bot.Triggers) > 0 {
		resolvedTrigIdx = 0
	}

	if exitCode != 0 {
		if resolvedTrigIdx >= 0 {
			bot.Triggers[resolvedTrigIdx].SkippedZeroToken++
			bot.Triggers[resolvedTrigIdx].Stats = fmt.Sprintf(
				"已静默拦截 %d 次空跑 (0 Token)", bot.Triggers[resolvedTrigIdx].SkippedZeroToken,
			)
		} else {
			bot.Triggers = append(bot.Triggers, BotTrigger{
				ID:               "trig-precheck",
				Name:             "只读预检门控",
				Precheck:         precheckCmd,
				Mode:             "先跑只读检查，无变化不调模型",
				Stats:            "已静默拦截 1 次空跑 (0 Token)",
				SkippedZeroToken: 1,
			})
		}
		if err := s.saveBotMetaLocked(bot); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"skippedZeroToken": true,
			"exitCode":         exitCode,
			"output":           output,
			"bot":              snapshot.enrich(bot),
		})
		return
	}

	// exitCode == 0: precheck matched a signal; record structured read-only inspection note.
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	homeIdx := ensureBotHome(&bot)
	noteText := fmt.Sprintf("预检 `%s` 命中信号（exit=0），已完成轻量只读命令检查：", precheckCmd)
	if output != "" {
		noteText += "\n```\n" + output + "\n```"
	} else {
		noteText += "（无标准输出）"
	}
	bot.Threads[homeIdx].Messages = append(bot.Threads[homeIdx].Messages, BotThreadMessage{
		ID:        uuid.NewString(),
		Role:      "bot",
		Meta:      fmt.Sprintf("%s · 只读预检命中 · 0 文件写入", bot.Name),
		Text:      noteText,
		Files:     extractFileRefs(output, bot.RepoCWD),
		CreatedAt: ts,
	})
	if err := s.saveBotThreadsLocked(bot.Slug, bot.Threads); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skippedZeroToken": false,
		"exitCode":         0,
		"output":           output,
		"bot":              snapshot.enrich(bot),
	})
}

func (s *Server) runBotPatrol(w http.ResponseWriter, r *http.Request, bot BotDetail, triggerID string) {
	snapshot := s.liveBots(r.Context(), bot.RepoCWD, true)
	text, files := snapshot.summary()
	now := time.Now()
	stats := fmt.Sprintf("%s 巡检 · %d 会话 / %d 仓库", now.Local().Format("15:04"), snapshot.sessionCount, snapshot.repoCount)

	botsStoreMu.Lock()
	defer botsStoreMu.Unlock()
	bot, err := s.resolveBotLocked(bot.Slug)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(bot.Triggers) == 0 {
		bot.Triggers = append(bot.Triggers, BotTrigger{ID: "trig-patrol", Name: "本机巡检", Mode: "本机只读巡检", Stats: stats})
	}
	matched := false
	for index := range bot.Triggers {
		trigger := &bot.Triggers[index]
		if triggerID == "" || trigger.ID == triggerID {
			trigger.Stats = stats
			matched = true
		}
	}
	if !matched {
		bot.Triggers[0].Stats = stats
	}
	home := ensureBotHome(&bot)
	bot.Threads[home].Messages = append(bot.Threads[home].Messages, BotThreadMessage{
		ID: uuid.NewString(), Role: "bot", Meta: bot.Name + " · 本机只读巡检",
		Text: text, Files: files, CreatedAt: now.UTC().Format(time.RFC3339),
	})
	if err := s.saveBotMetaLocked(bot); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.saveBotThreadsLocked(bot.Slug, bot.Threads); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skippedZeroToken": false, "patrol": true, "bot": snapshot.enrich(bot)})
}

// handlePatchBotItem handles PATCH /bots/{id}/items/{itemId} with CAS-idempotent worktree binding.
func (s *Server) handlePatchBotItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	itemID := r.PathValue("itemId")
	if !isValidBotSlug(id) || strings.TrimSpace(itemID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bot or item id"})
		return
	}
	var req struct {
		Status              string `json:"status"`
		SpawnedWorktreePath string `json:"spawnedWorktreePath"`
		SpawnedTabID        string `json:"spawnedTabId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Status != "" && req.Status != "needs_you" && req.Status != "working" && req.Status != "done" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid item status"})
		return
	}

	botsStoreMu.Lock()
	bot, err := s.resolveBotLocked(id)
	botsStoreMu.Unlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	snapshot := s.liveBots(r.Context(), bot.RepoCWD, false)

	botsStoreMu.Lock()
	defer botsStoreMu.Unlock()
	bot, err = s.resolveBotLocked(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	idx := slices.IndexFunc(bot.Items, func(item BotItem) bool { return item.ID == itemID })
	merged := snapshot.enrich(bot)
	liveIdx := slices.IndexFunc(merged.Items, func(item BotItem) bool { return item.ID == itemID })
	if liveIdx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "bot item not found"})
		return
	}

	item := merged.Items[liveIdx]
	alreadySpawned := false
	wantsSpawnBind := req.SpawnedWorktreePath != "" || req.SpawnedTabID != ""
	hasExistingSpawn := item.SpawnedWorktreePath != "" || item.SpawnedTabID != ""

	if wantsSpawnBind {
		if hasExistingSpawn {
			// CAS protection: preserve the first bound worktree/tab and report alreadySpawned=true.
			alreadySpawned = true
		} else {
			item.SpawnedWorktreePath = req.SpawnedWorktreePath
			item.SpawnedTabID = req.SpawnedTabID
			if req.Status != "" {
				item.Status = req.Status
			} else if item.Status == "needs_you" {
				item.Status = "working"
			}
		}
	} else if req.Status != "" {
		item.Status = req.Status
	}

	if !alreadySpawned && (wantsSpawnBind || req.Status != "") {
		var err error
		if idx >= 0 {
			persisted := &bot.Items[idx]
			persisted.Status, persisted.SpawnedWorktreePath, persisted.SpawnedTabID = item.Status, item.SpawnedWorktreePath, item.SpawnedTabID
			err = s.saveBotItemsLocked(bot.Slug, bot.Items)
		} else {
			if bot.itemOverrides == nil {
				bot.itemOverrides = make(map[string]botItemOverride)
			}
			override := bot.itemOverrides[itemID]
			if wantsSpawnBind {
				override.SpawnedWorktreePath, override.SpawnedTabID = item.SpawnedWorktreePath, item.SpawnedTabID
			}
			if req.Status != "" || (wantsSpawnBind && merged.Items[liveIdx].Status == "needs_you") {
				override.Status = item.Status
			}
			override.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			bot.itemOverrides[itemID] = override
			err = s.saveBotMetaLocked(bot)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		merged = snapshot.enrich(bot)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"item":           merged.Items[liveIdx],
		"alreadySpawned": alreadySpawned,
		"bot":            merged,
	})
}

// runValidatedReadOnlyCommand executes a command that has ALREADY passed ValidateReadOnlyCommand
// with a 4-second timeout and bounded output capture.
func runValidatedReadOnlyCommand(parent context.Context, dir, cmdStr string) (int, string) {
	ctx, cancel := context.WithTimeout(parent, botPrecheckTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
	if dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			cmd.Dir = dir
		}
	}
	cmd.Env = append(cmd.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")

	var buf limitedBuffer
	buf.limit = botOutputCapBytes
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err == nil {
		return 0, out
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), out
	}
	if ctx.Err() != nil {
		if out != "" {
			out += "\n(precheck timed out)"
		} else {
			out = "(precheck timed out)"
		}
		return 124, out
	}
	return 1, out
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	origLen := len(p)
	if b.limit <= 0 {
		return origLen, nil
	}
	rem := b.limit - b.buf.Len()
	if rem <= 0 {
		b.truncated = true
		return origLen, nil
	}
	if len(p) > rem {
		b.buf.Write(p[:rem])
		b.truncated = true
		return origLen, nil
	}
	b.buf.Write(p)
	return origLen, nil
}

func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += "\n...(truncated)"
	}
	return s
}

// fileRefRE matches relative or absolute file references (including Unicode/Chinese paths)
// with common code/doc extensions and optional :line or :line:col suffixes.
var fileRefRE = regexp.MustCompile(`@?((?:@[A-Za-z0-9_\p{Han}./-]+|(?:[A-Za-z0-9_./][A-Za-z0-9_\p{Han}./-]*|[\p{Han}A-Za-z0-9_.-]+/[A-Za-z0-9_\p{Han}./-]+))\.(?:go|tsx|ts|json|jsx|js|vue|py|rs|java|cpp|cc|css|csv|c|hpp|html|h|md|yaml|yml|toml|scss|sh|sql|txt)(?::\d+(?::\d+)?)?)`)

func extractFileRefs(text, cwd string) []string {
	if text == "" {
		return nil
	}
	matches := fileRefRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		ref := normalizeFileRef(m[1], cwd)
		if ref == "" {
			continue
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func normalizeFileRef(raw, cwd string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "@")
	if raw == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return ""
	}
	// Separate optional :line(:col) suffix before converting absolute paths inside cwd to relative.
	pathPart := raw
	suffix := ""
	if idx := strings.IndexByte(raw, ':'); idx > 0 {
		pathPart = raw[:idx]
		suffix = raw[idx:]
	}
	if cwd != "" && filepath.IsAbs(pathPart) {
		if rel, err := filepath.Rel(cwd, pathPart); err == nil && filepath.IsLocal(rel) {
			pathPart = filepath.ToSlash(rel)
		}
	}
	return pathPart + suffix
}
