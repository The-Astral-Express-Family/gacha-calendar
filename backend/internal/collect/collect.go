// Package collect orchestrates the collection and planning pipeline:
//
//  1. official column list → newest version-update notice + preview teasers
//  2. parse the 255-char list summary; fetch the full column only when the
//     summary lacks a decisive field
//  3. official preview videos + recent official dynamics (noise-filtered)
//  4. PlanNext pure rules (calibrated by backfilled history) → plan, then a
//     same-vendor stagger pass across games
//  5. persist posts / version / prediction rows and download covers
//
// Announcements are sticky: a teaser that was seen once is replayed from the
// database on every later cycle, so an officially announced preview survives
// feed outages (the dynamics endpoint answers -352 for hours at a time).
//
// Failures never invent dates: the previous parsed version and prediction
// stay in place, and the error is reported on the game state.
package collect

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/covers"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/model"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/parse"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/vendor"
)

const day = 24 * time.Hour

// ScheduleOptions drives the event-aware poll cadence. Announcement sources
// cluster in known hours (update notices 07:00–11:00, teasers around noon,
// replays Friday evening), so the scheduler polls hard only while a game is
// inside an event watch window — and hardest during those hours.
type ScheduleOptions struct {
	// BaseInterval is the quiet-period per-game poll distance (REFRESH_INTERVAL).
	BaseInterval time.Duration
	// WatchInterval polls a game that sits inside an event watch window,
	// outside prime announcement hours.
	WatchInterval time.Duration
	// PrimeInterval polls a game inside a watch window during the hours its
	// publishers actually post.
	PrimeInterval time.Duration
	// TickInterval is the scheduler wake granularity.
	TickInterval time.Duration
}

func (o ScheduleOptions) withDefaults() ScheduleOptions {
	if o.BaseInterval <= 0 {
		o.BaseInterval = 3 * time.Hour
	}
	if o.WatchInterval <= 0 {
		o.WatchInterval = 30 * time.Minute
	}
	if o.PrimeInterval <= 0 {
		o.PrimeInterval = 15 * time.Minute
	}
	if o.PrimeInterval < 10*time.Minute {
		o.PrimeInterval = 10 * time.Minute
	}
	if o.WatchInterval < o.PrimeInterval {
		o.WatchInterval = o.PrimeInterval
	}
	if o.TickInterval <= 0 {
		o.TickInterval = 5 * time.Minute
	}
	return o
}

// primeAnnounceHours reports whether a CST instant falls in the publication
// clusters observed in the historical data: 06:30–13:30 (update notices at
// 07:00/09:00/10:00, teasers around noon) and 18:00–23:00 (streams/replays).
func primeAnnounceHours(t time.Time) bool {
	p, ok := parse.CSTParts(t)
	if !ok {
		return false
	}
	return (p.Hour >= 6 && p.Hour < 14) || (p.Hour >= 18 && p.Hour < 23)
}

// gameWatchActive reports whether `now` (CST) sits inside the game's event
// watch window derived from its live plan: from teaserLead+2 days before the
// predicted preview (teasers drop ~3–6 days ahead) until a day after the
// event, and likewise around the version maintenance.
func gameWatchActive(game games.Game, pred *model.Prediction, now time.Time) bool {
	if pred == nil {
		return false
	}
	if pred.PreviewStart != nil && pred.PreviewEnd != nil {
		start := pred.PreviewStart.Add(time.Duration(-game.TeaserLeadDays-2) * day)
		end := pred.PreviewEnd.Add(day)
		if !now.Before(start) && now.Before(end) {
			return true
		}
	}
	if pred.UpdateStart != nil && pred.UpdateEnd != nil {
		// WuWa posts its maintenance forecast 7 days ahead, Endfield its
		// dev letter ~8 days ahead — 9 days covers every vendor.
		start := pred.UpdateStart.Add(-9 * day)
		end := pred.UpdateEnd.Add(day)
		if !now.Before(start) && now.Before(end) {
			return true
		}
	}
	return false
}

// GamePollInterval is the pure per-game poll policy.
func GamePollInterval(opts ScheduleOptions, game games.Game, pred *model.Prediction,
	lastRefresh *time.Time, calibrated bool, now time.Time) time.Duration {
	if lastRefresh == nil {
		return 0
	}
	switch {
	case gameWatchActive(game, pred, now):
		if primeAnnounceHours(now) {
			return opts.PrimeInterval
		}
		return opts.WatchInterval
	case !calibrated:
		// Keep collecting while the bootstrap has not produced history.
		return opts.WatchInterval
	default:
		return opts.BaseInterval
	}
}

// dynamicNoise filters lottery/repost filler that dominates official dynamics
// feeds; none of it ever carries schedule facts.
var dynamicNoise = regexp.MustCompile(
	`抽奖|中奖|已私信|恭喜\s*@|福利|壁纸|表情包|同人|漫画|条漫|四格|兑换码|礼包码|祈愿|封面|贺图`)

func isDynamicNoise(text string) bool {
	return dynamicNoise.MatchString(text)
}

// coverStore is the slice of covers.Downloader the collector needs; an
// interface so tests can fake the downloads.
type coverStore interface {
	SaveGame(ctx context.Context, gameID, image string) string
	SavePost(ctx context.Context, postID uint, image string) string
}

type Collector struct {
	db      *gorm.DB
	bili    *bili.Client
	covers  coverStore
	vendors *vendor.Client
	log     *slog.Logger
	opts    ScheduleOptions

	mu          sync.Mutex
	running     bool
	bootRunning bool
}

func New(db *gorm.DB, client *bili.Client, dl *covers.Downloader, logger *slog.Logger) *Collector {
	return &Collector{db: db, bili: client, covers: dl, vendors: vendor.New(), log: logger}
}

// SetSchedule installs the poll cadence (call before Start).
func (c *Collector) SetSchedule(opts ScheduleOptions) {
	c.opts = opts.withDefaults()
}

// GameResult is one game's outcome in a cycle.
type GameResult struct {
	Game   string `json:"game"`
	Status string `json:"status"` // ok|partial|error|skipped
	Error  string `json:"error,omitempty"`
}

// Running reports whether a cycle is in flight.
func (c *Collector) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// RequestRefresh kicks off an async refresh of the named games (all when
// empty). Returns false when a cycle is already running.
func (c *Collector) RequestRefresh(ids []games.GameID) bool {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return false
	}
	c.running = true
	c.mu.Unlock()

	targets := ids
	if len(targets) == 0 {
		targets = games.IDs()
	}
	go func() {
		defer func() {
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		c.RefreshGames(ctx, targets)
	}()
	return true
}

// Start runs the adaptive scheduler until ctx is done: one cycle shortly
// after boot (when the data is stale), then every tick it refreshes exactly
// the games whose event-aware poll interval has elapsed.
func (c *Collector) Start(ctx context.Context, opts ScheduleOptions, startupDelay time.Duration) {
	c.SetSchedule(opts)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupDelay):
		}
		if c.stale(c.opts.BaseInterval) {
			c.RequestRefresh(nil)
		}
		ticker := time.NewTicker(c.opts.TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				due := c.dueGames(time.Now().UTC())
				if len(due) > 0 {
					c.RequestRefresh(due)
				}
				// Record the next expected wake even while busy, so
				// /api/meta always shows the scheduler's plan.
				go c.recordNextRun()
			}
		}
	}()
}

// dueGames lists enabled games whose per-game interval has elapsed.
func (c *Collector) dueGames(now time.Time) []games.GameID {
	calibrated := c.calibrated()
	var states []model.GameState
	if err := c.db.Find(&states).Error; err != nil {
		return nil
	}
	var due []games.GameID
	for _, state := range states {
		game, ok := games.Get(games.GameID(state.ID))
		if !ok || !state.Enabled {
			continue
		}
		var pred model.Prediction
		_ = c.db.Where("game_id = ? AND active = ?", state.ID, true).First(&pred).Error
		interval := GamePollInterval(c.opts, game, &pred, state.LastRefreshAt, calibrated, now)
		if now.Sub(derefTime(state.LastRefreshAt)) >= interval {
			due = append(due, game.ID)
		}
	}
	return due
}

// recordNextRun stores when the next scheduler wake will find work, for
// observability in /api/meta.
func (c *Collector) recordNextRun() {
	calibrated := c.calibrated()
	now := time.Now().UTC()
	var states []model.GameState
	if err := c.db.Find(&states).Error; err != nil {
		return
	}
	best := time.Time{}
	for _, state := range states {
		game, ok := games.Get(games.GameID(state.ID))
		if !ok || !state.Enabled {
			continue
		}
		var pred model.Prediction
		_ = c.db.Where("game_id = ? AND active = ?", state.ID, true).First(&pred).Error
		interval := GamePollInterval(c.opts, game, &pred, state.LastRefreshAt, calibrated, now)
		next := derefTime(state.LastRefreshAt).Add(interval)
		if derefTime(state.LastRefreshAt).IsZero() {
			next = now
		}
		if best.IsZero() || next.Before(best) {
			best = next
		}
	}
	if best.IsZero() {
		return
	}
	c.db.Model(&model.CollectState{}).Where("id = 1").Update("next_run_at", best.UTC())
}

func (c *Collector) stale(interval time.Duration) bool {
	var state model.CollectState
	if err := c.db.First(&state, 1).Error; err != nil {
		return true
	}
	if state.LastCycleAt == nil {
		return true
	}
	return time.Since(*state.LastCycleAt) > interval/2
}

func (c *Collector) calibrated() bool {
	var state model.CollectState
	if err := c.db.First(&state, 1).Error; err != nil {
		return false
	}
	return state.BootstrapState == "done"
}

// RefreshGames runs the per-game refresh serially (the Bilibili client is a
// serial chain anyway), applies the same-vendor preview stagger across the
// freshly computed plans, then persists and updates the service state.
func (c *Collector) RefreshGames(ctx context.Context, ids []games.GameID) []GameResult {
	type finished struct {
		id     games.GameID
		out    *gameOutcome
		result GameResult
	}
	outcomes := make([]finished, 0, len(ids))
	results := make([]GameResult, 0, len(ids))
	okCount := 0
	for _, id := range ids {
		outcome, result := c.collectGame(ctx, id)
		outcomes = append(outcomes, finished{id: id, out: outcome, result: result})
		results = append(results, result)
		if ctx.Err() != nil {
			break
		}
	}

	// Cross-game pass: same-vendor inferred previews must not collide.
	plans := map[games.GameID]*parse.Plan{}
	for _, f := range outcomes {
		if f.out != nil {
			plans[f.id] = f.out.plan
		}
	}
	parse.StaggerVendorPreviews(plans)

	for i := range outcomes {
		f := &outcomes[i]
		if f.out == nil {
			continue
		}
		c.finalizeGame(f.out)
		// The status is only known after finalization (it depends on the
		// persisted plan); reflect it on the cycle result.
		switch {
		case f.out.plan == nil:
			f.result.Status = "error"
			f.result.Error = firstNonEmptyStr(joinErrors(f.out.errs), "暂无可用的版本信息")
		case len(f.out.errs) > 0:
			f.result.Status = "partial"
			f.result.Error = joinErrors(f.out.errs)
		default:
			f.result.Status = "ok"
			f.result.Error = ""
			okCount++
		}
		results[i] = f.result
	}

	now := time.Now().UTC()
	var state model.CollectState
	if err := c.db.First(&state, 1).Error; err != nil {
		state = model.CollectState{ID: 1}
	}
	state.LastCycleAt = &now
	state.LastCycleOK = okCount
	state.LastCycleAll = len(ids)
	c.db.Save(&state)
	return results
}

func (c *Collector) latestVersion(gameID games.GameID) (*model.Version, bool) {
	var row model.Version
	err := c.db.Where("game_id = ?", string(gameID)).
		Order("update_start DESC").First(&row).Error
	if err != nil {
		return nil, false
	}
	return &row, true
}

func factsFromVersion(row *model.Version) *parse.ArticleFacts {
	return &parse.ArticleFacts{
		Version:     row.Version,
		VersionName: row.VersionName,
		UpdateStart: row.UpdateStart,
		UpdateEnd:   derefTime(row.UpdateEnd),
		VersionEnd:  derefTime(row.VersionEnd),
		PreviewAt:   derefTime(row.PreviewStart),
		PhaseUpdate: derefTime(row.PhaseUpdate),
	}
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// gameCadence loads the bootstrap-calibrated cadence for a game (nil until
// at least three versions are known).
func (c *Collector) gameCadence(id games.GameID) *parse.Cadence {
	var stats model.GameStats
	if err := c.db.First(&stats, "game_id = ?", string(id)).Error; err != nil {
		return nil
	}
	if stats.SampleCount < 3 {
		return nil
	}
	return &parse.Cadence{
		SampleCount:        stats.SampleCount,
		CycleDays:          stats.CycleDays,
		UpdateWeekday:      stats.UpdateWeekday,
		PreviewLeadDays:    stats.PreviewLeadDays,
		PreviewWeekday:     stats.PreviewWeekday,
		PreviewStartMinute: stats.PreviewStartMinute,
		AnnounceHour:       stats.AnnounceHour,
	}
}

// upsertPost creates or refreshes one raw post row; full Content is kept from
// a previous fetch when the new payload has none.
func (c *Collector) upsertPost(post *model.Post) error {
	var existing model.Post
	err := c.db.Where("source_type = ? AND external_id = ?",
		post.SourceType, post.ExternalID).First(&existing).Error
	if err != nil {
		return c.db.Create(post).Error
	}
	existing.Title = post.Title
	existing.Author = post.Author
	existing.URL = post.URL
	if post.Summary != "" {
		existing.Summary = post.Summary
	}
	if post.Content != "" {
		existing.Content = post.Content
	}
	if post.CoverURL != "" {
		existing.CoverURL = post.CoverURL
	}
	if post.CoverPath != "" {
		existing.CoverPath = post.CoverPath
	}
	existing.PublishTime = post.PublishTime
	existing.FetchedAt = post.FetchedAt
	existing.IsUpdateNotice = post.IsUpdateNotice || existing.IsUpdateNotice
	existing.IsTeaser = post.IsTeaser || existing.IsTeaser
	*post = existing
	return c.db.Save(&existing).Error
}

// pickArticle returns the newest official article that claims to be a
// version-update notice.
func pickArticle(game games.Game, articles []bili.Article, now time.Time) *bili.Article {
	var picked *bili.Article
	for i := range articles {
		item := &articles[i]
		if item.Title == "" || game.ExcludeTitle.MatchString(item.Title) {
			continue
		}
		if !game.UpdateTitle.MatchString(item.Title) {
			continue
		}
		// A little tolerance for CST/UTC rounding at the day boundary.
		if item.PublishTime > 0 && time.Unix(item.PublishTime, 0).After(now.Add(day)) {
			continue
		}
		if picked == nil || item.PublishTime > picked.PublishTime {
			picked = item
		}
	}
	return picked
}

// gameOutcome carries one game's freshly collected material through the
// cross-game stagger pass, before anything is persisted.
type gameOutcome struct {
	game         games.Game
	state        model.GameState
	plan         *parse.Plan
	articleID    uint // post id of the newest update notice, 0 when none
	errs         []string
	started      time.Time
	coverRefresh bool
}

func (c *Collector) logGame(game games.Game) func(string, ...any) {
	return func(format string, args ...any) {
		c.log.Info("gacha: " + game.Name + " " + fmt.Sprintf(format, args...))
	}
}

// collectGame gathers sources and computes the plan (no writes to
// prediction/status yet). The returned outcome is nil when the game is
// unknown/disabled.
func (c *Collector) collectGame(ctx context.Context, id games.GameID) (*gameOutcome, GameResult) {
	game, ok := games.Get(id)
	if !ok {
		return nil, GameResult{Game: string(id), Status: "error", Error: "unknown game"}
	}
	log := c.logGame(game)
	out := &gameOutcome{game: game, started: time.Now()}
	if err := c.db.First(&out.state, "id = ?", string(id)).Error; err != nil {
		return nil, GameResult{Game: string(id), Status: "error", Error: "game state missing"}
	}
	if !out.state.Enabled {
		return nil, GameResult{Game: string(id), Status: "skipped"}
	}

	now := time.Now().UTC()
	log("refresh start (article=%s)", out.state.LastArticleID)

	// 1. Official column list.
	articles, err := c.bili.ArticleList(ctx, game.Mid)
	if err != nil {
		if ctx.Err() != nil {
			return nil, GameResult{Game: string(id), Status: "error", Error: ctx.Err().Error()}
		}
		out.errs = append(out.errs, "公告列表："+err.Error())
		c.log.Warn("gacha: "+game.Name+" article list failed", "error", err.Error())
	}
	log("article list: %d rows", len(articles))

	// 1a. Preview-teaser articles: persist and remember them as sticky
	// announcement sources (miHoYo posts the stream date only via dynamic,
	// but WuWa/Endfield style vendors also use articles).
	out.errs = c.scanTeaserArticles(ctx, game, articles, now, out.errs)

	// 1a'. Self-heal post covers: backfilled articles were stored without
	// images and video/dynamic posts keep only the remote CDN URL, which
	// browsers cannot hotlink. Fill from the list already fetched and
	// localize, a few downloads per cycle.
	c.healPostCovers(ctx, game, articles)

	// 1b. Newest version-update notice.
	picked := pickArticle(game, articles, now)
	if picked != nil && picked.IDString() != out.state.LastArticleID {
		outcome, errs := c.ingestUpdateArticle(ctx, game, &out.state, picked, now, out.errs)
		out.errs = errs
		if outcome != nil {
			out.articleID = *outcome
		}
	} else if picked == nil && len(out.errs) == 0 {
		out.errs = append(out.errs, "未找到版本更新公告")
	}

	// Reconstruct current facts from the newest version row (the single
	// source of truth, also across restarts).
	var current *parse.ArticleFacts
	if row, ok := c.latestVersion(id); ok {
		current = factsFromVersion(row)
	}

	// 2. Preview videos + official dynamics → raw posts + planner inputs.
	// Runs for pre-launch games too (nil current): the teaser dynamic is
	// their only official announcement source.
	videos, errs := c.collectVideosAndDynamics(ctx, game, current, now, out.errs)
	out.errs = errs
	log("preview videos=%d", len(videos))

	// 2b. Live-room system announcement: the embedded reservation message
	// ("将于10月23日晚20:00开启") is an official teaser that survives
	// dynamics-feed risk control.
	out.errs = c.collectLiveRoom(ctx, game, now, out.errs)

	// 2c. Publisher-owned announcement systems (in-game feed / official
	// site) — fully independent of Bilibili.
	out.errs = c.collectVendorPosts(ctx, game, now, out.errs)

	// 3. Sticky announcements: this cycle's teasers/dynamics/live messages
	// were persisted above; load the last 35 days back as planner inputs.
	announcements := c.storedAnnouncements(game, now)

	// 4. Plan the next version (calibrated cadence when available). A game
	// with no version history yet (pre-launch) can still calendar an
	// officially announced stream via the preview-only path.
	if current != nil {
		out.plan = parse.PlanNext(game, current, now, videos, announcements, c.gameCadence(id))
	} else {
		out.plan = parse.PreviewOnlyPlan(game, now, announcements)
	}
	out.plan = c.carryOfficialPreview(id, out.plan, now)
	out.plan = c.mergeOverride(game, id, out.plan)
	return out, GameResult{Game: string(id)}
}

// ingestUpdateArticle parses one version-update notice (summary first, full
// column when the summary lacks a decisive field) and upserts the version
// row. Returns the persisted post id.
func (c *Collector) ingestUpdateArticle(ctx context.Context, game games.Game,
	state *model.GameState, picked *bili.Article, now time.Time, errs []string) (*uint, []string) {
	log := c.logGame(game)
	var facts *parse.ArticleFacts
	published := time.Unix(picked.PublishTime, 0).UTC()
	summaryText := bili.StripHTML(picked.Summary)
	if summaryText != "" {
		parsed := parse.ParseArticle(picked.Title, summaryText, published, game)
		facts = &parsed
	}
	cover := ""
	if len(picked.ImageURLs) > 0 {
		cover = picked.ImageURLs[0]
	}

	// Summary-first merge: only read the heavily rate-limited full column
	// when the summary lacks a decisive field.
	missing := missingFields(game, facts)
	needsFullText := isCVID(picked.IDString()) && len(missing) > 0
	if facts != nil {
		log("summary parse: version=%s update=%s missing=%v full_text=%v",
			facts.Version, shortCST(facts.UpdateStart), missing, needsFullText)
	} else {
		log("summary parse: empty summary")
	}

	post := &model.Post{
		GameID: string(game.ID), SourceType: "article", ExternalID: picked.IDString(),
		URL: bili.ArticleURL(picked.IDString()), Title: picked.Title,
		Summary: summaryText, Author: game.Name,
		PublishTime: published, FetchedAt: now, CoverURL: cover,
		IsUpdateNotice: true,
	}
	if err := c.upsertPost(post); err != nil {
		errs = append(errs, "公告入库："+err.Error())
	}
	postID := post.ID

	if needsFullText {
		view, viewErr := c.bili.ArticleView(ctx, picked.IDString())
		if viewErr != nil {
			if ctx.Err() != nil {
				return nil, errs
			}
			errs = append(errs, "公告正文："+viewErr.Error())
			c.log.Warn("gacha: "+game.Name+" full column failed", "error", viewErr.Error())
		} else {
			full := parse.ParseArticle(view.Title, bili.StripHTML(view.Content),
				time.Unix(view.PublishTime, 0).UTC(), game)
			merged := parse.MergeArticle(facts, &full)
			facts = &merged
			if len(view.ImageURLs) > 0 && post.CoverURL == "" {
				post.CoverURL = view.ImageURLs[0]
			}
			post.Content = bili.StripHTML(view.Content)
			if view.Title != "" {
				post.Title = view.Title
			}
			post.FetchedAt = now
			if err := c.db.Save(post).Error; err != nil {
				errs = append(errs, "公告入库："+err.Error())
			}
			log("full column merged: version=%s update=%s",
				facts.Version, shortCST(facts.UpdateStart))
		}
	}

	if post.ID != 0 && post.CoverURL != "" {
		if path := c.covers.SavePost(ctx, post.ID, post.CoverURL); path != "" {
			post.CoverPath = path
			c.db.Save(post)
		}
	}

	// Same retention rule as the plugin: remember the article when it
	// produced an update time, or when no previous version exists;
	// otherwise retry parsing next cycle.
	_, haveVersion := c.latestVersion(game.ID)
	if facts != nil && (facts.HasUpdateStart() || !haveVersion) {
		if facts.HasUpdateStart() {
			if err := c.upsertVersion(game, facts, postID); err != nil {
				errs = append(errs, "版本入库："+err.Error())
			}
		}
		state.LastArticleID = picked.IDString()
	} else {
		errs = append(errs, "最新公告未解析出更新时间，沿用上次结果")
	}
	return &postID, errs
}

// scanTeaserArticles persists preview-announcement articles as sticky posts.
// Only recent ones are fetched in full; old teasers need nothing beyond their
// (already persisted) summary.
func (c *Collector) scanTeaserArticles(ctx context.Context, game games.Game,
	articles []bili.Article, now time.Time, errs []string) []string {
	for i := range articles {
		item := &articles[i]
		if !games.IsPreviewTeaserTitle(item.Title) {
			continue
		}
		if item.PublishTime <= 0 || time.Unix(item.PublishTime, 0).Before(now.Add(-45*day)) {
			continue
		}
		var existing model.Post
		known := c.db.Where("source_type = ? AND external_id = ?", "article", item.IDString()).
			First(&existing).Error == nil
		summaryText := bili.StripHTML(item.Summary)
		// A freshly published teaser whose summary hides the date is worth
		// one rate-limited full read; anything already stored is not.
		needsFull := !known && isCVID(item.IDString()) &&
			!hasPreviewFact(summaryText) &&
			time.Unix(item.PublishTime, 0).After(now.Add(-14*day))
		text := summaryText
		if needsFull {
			if view, err := c.bili.ArticleView(ctx, item.IDString()); err == nil {
				text = bili.StripHTML(view.Content)
			}
		} else if known && existing.Summary != "" {
			text = existing.Summary
		}
		if text == "" {
			continue
		}
		post := &model.Post{
			GameID: string(game.ID), SourceType: "article", ExternalID: item.IDString(),
			URL: bili.ArticleURL(item.IDString()), Title: item.Title,
			Summary: text, Author: game.Name,
			PublishTime: time.Unix(item.PublishTime, 0).UTC(), FetchedAt: now,
			IsTeaser: true,
		}
		if len(item.ImageURLs) > 0 {
			post.CoverURL = item.ImageURLs[0]
		}
		if err := c.upsertPost(post); err != nil {
			errs = append(errs, "前瞻预告入库："+err.Error())
		} else if post.ID != 0 && post.CoverURL != "" {
			if path := c.covers.SavePost(ctx, post.ID, post.CoverURL); path != "" {
				post.CoverPath = path
				c.db.Save(post)
			}
		}
	}
	return errs
}

func hasPreviewFact(text string) bool {
	if text == "" {
		return false
	}
	_, okDated := parse.ExtractExplicitPreviewAt(text, time.Now().UTC())
	_, okDateOnly := parse.ExtractPreviewAt(text, time.Now().UTC(), games.Game{})
	return okDated || okDateOnly
}

// storedAnnouncements replays persisted teasers and non-noise dynamics from
// the last 35 days as planner inputs, so an officially announced stream
// survives feed outages.
func (c *Collector) storedAnnouncements(game games.Game, now time.Time) []parse.Announcement {
	var posts []model.Post
	if err := c.db.Where("game_id = ? AND (is_teaser = ? OR source_type IN ?) AND publish_time > ?",
		string(game.ID), true, []string{"dynamic", "live", "official"}, now.Add(-35*day)).
		Order("publish_time DESC").Limit(60).Find(&posts).Error; err != nil {
		return nil
	}
	out := make([]parse.Announcement, 0, len(posts))
	for _, post := range posts {
		// Vendor posts carry the version name in the title and the dates in
		// the brief — the planner needs both in one text.
		text := post.Summary
		if post.Title != "" && post.Title != post.Summary {
			if text == "" {
				text = post.Title
			} else {
				text = post.Title + "\n" + post.Summary
			}
		}
		if text == "" || isDynamicNoise(text) {
			continue
		}
		out = append(out, parse.Announcement{Text: text, Pubdate: post.PublishTime, URL: post.URL})
	}
	return out
}

// collectLiveRoom reads the official live room's system announcement (the
// embedded reservation message stating the preview date/time) and persists it
// as a sticky teaser post. Two cheap requests per game; both the live-domain
// API and the room page keep answering while the dynamics feed is blocked.
func (c *Collector) collectLiveRoom(ctx context.Context, game games.Game,
	now time.Time, errs []string) []string {
	if game.LiveRoomID == "" {
		return errs
	}
	text, published, err := c.bili.LiveRoomAnnouncement(ctx, game.LiveRoomID)
	if err != nil {
		if ctx.Err() != nil {
			return errs
		}
		c.log.Info("gacha: "+game.Name+" live room unavailable", "error", err.Error())
		return errs
	}
	if text == "" {
		return errs
	}
	if published.IsZero() {
		published = now
	}
	isTeaser := strings.Contains(text, "前瞻") || strings.Contains(text, "直播") ||
		strings.Contains(text, "版本更新")
	title := text
	if i := strings.IndexAny(text, "！!。"); i > 0 {
		title = text[:i]
	}
	post := &model.Post{
		GameID: string(game.ID), SourceType: "live",
		ExternalID: "room-" + game.LiveRoomID,
		URL:        "https://live.bilibili.com/" + game.LiveRoomID,
		Title:      title, Summary: text, Author: game.Name,
		PublishTime: published.UTC(), FetchedAt: now,
		IsTeaser: isTeaser,
	}
	// The room banner carries the preview key visual before the stream; it is
	// the only image this announcement source can offer. Best-effort — the
	// announcement itself stays valuable without it.
	if room, roomErr := c.bili.LiveRoomInfo(ctx, game.LiveRoomID); roomErr == nil && room.Cover != "" {
		post.CoverURL = room.Cover
	}
	if err := c.upsertPost(post); err != nil {
		errs = append(errs, "直播间公告入库："+err.Error())
	} else if post.ID != 0 && post.CoverPath == "" && post.CoverURL != "" {
		if path := c.covers.SavePost(ctx, post.ID, post.CoverURL); path != "" {
			post.CoverPath = path
			c.db.Save(post)
		}
	}
	return errs
}

// vendorRelevant filters the publisher feeds down to schedule-bearing posts;
// the in-game announcement feed in particular is full of activity banners.
var vendorRelevant = regexp.MustCompile(
	`版本更新|更新说明|更新公告|内容说明|维护预告|前瞻|直播|研发通讯|版本说明`)

// collectVendorPosts reads the publisher's own announcement system (see
// internal/vendor) and persists schedule-relevant posts as sticky official
// announcements. Independent of every Bilibili endpoint.
func (c *Collector) collectVendorPosts(ctx context.Context, game games.Game,
	now time.Time, errs []string) []string {
	if game.VendorAnnouncer == "" {
		return errs
	}
	var posts []vendor.Post
	var err error
	switch {
	case strings.HasPrefix(game.VendorAnnouncer, "mihoyo-ann:"):
		parts := strings.Split(game.VendorAnnouncer, ":")
		if len(parts) != 4 {
			return errs
		}
		posts, err = c.vendors.FetchMihoyoAnnouncements(ctx, parts[1], parts[2], parts[3])
	case game.VendorAnnouncer == "endfield-site":
		posts, err = c.vendors.FetchEndfieldNews(ctx)
	default:
		return errs
	}
	if err != nil {
		if ctx.Err() == nil {
			c.log.Info("gacha: "+game.Name+" vendor source unavailable",
				"source", game.VendorAnnouncer, "error", err.Error())
		}
		return errs
	}

	stored := 0
	for i := range posts {
		post := posts[i]
		text := post.Title
		if !vendorRelevant.MatchString(text) {
			continue
		}
		if post.PublishedAt.IsZero() {
			post.PublishedAt = now
		}
		if post.PublishedAt.Before(now.Add(-45 * day)) {
			continue
		}
		summary := firstNonEmptyStr(post.Brief, post.Subtitle)
		isTeaser := strings.Contains(post.Title, "前瞻") || strings.Contains(post.Title, "直播")
		row := &model.Post{
			GameID: string(game.ID), SourceType: "official",
			ExternalID: post.ExternalID, URL: post.URL,
			Title: post.Title, Summary: summary, Author: game.Name,
			PublishTime: post.PublishedAt, FetchedAt: now,
			IsTeaser: isTeaser, CoverURL: post.Cover,
		}
		if err := c.upsertPost(row); err != nil {
			errs = append(errs, "官方源入库："+err.Error())
			continue
		}
		if row.ID != 0 && row.CoverPath == "" && row.CoverURL != "" {
			if path := c.covers.SavePost(ctx, row.ID, row.CoverURL); path != "" {
				row.CoverPath = path
				c.db.Save(row)
			}
		}
		stored++
	}
	if stored > 0 {
		c.logGame(game)("vendor source: %d posts", stored)
	}
	return errs
}

func shortCST(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(parse.CST).Format("01-02 15:04")
}

func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "；"
		}
		out += e
	}
	return out
}

func isCVID(id string) bool {
	if id == "" || len(id) > 12 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func missingFields(game games.Game, facts *parse.ArticleFacts) []string {
	var missing []string
	if facts == nil {
		return []string{"摘要为空"}
	}
	if facts.UpdateStart.IsZero() {
		missing = append(missing, "缺更新时间")
	}
	if facts.Version == "" && game.ID != games.Endfield {
		missing = append(missing, "缺版本号")
	}
	// These two announce an explicit end date; a truncated summary must not
	// silently downgrade to the arithmetic cadence.
	if game.ExpectVersionEnd && facts.VersionEnd.IsZero() {
		missing = append(missing, "缺版本结束日")
	}
	// Endfield's next phase date lives deeper in the article.
	if game.PhaseHint && facts.PhaseUpdate.IsZero() {
		missing = append(missing, "缺阶段更新日")
	}
	return missing
}

func (c *Collector) upsertVersion(game games.Game, facts *parse.ArticleFacts, postID uint) error {
	row := model.Version{
		GameID:       string(game.ID),
		Version:      facts.Version,
		VersionName:  facts.VersionName,
		UpdateStart:  facts.UpdateStart.UTC(),
		UpdateEnd:    ptrTime(facts.UpdateEnd),
		VersionEnd:   ptrTime(facts.VersionEnd),
		PreviewStart: ptrTime(facts.PreviewAt),
		PhaseUpdate:  ptrTime(facts.PhaseUpdate),
	}
	if postID != 0 {
		row.AnnouncementID = &postID
	}
	var existing model.Version
	err := c.db.Where("game_id = ? AND update_start = ?", row.GameID, row.UpdateStart).
		First(&existing).Error
	if err != nil {
		return c.db.Create(&row).Error
	}
	// Merge, never clobber: a backfill's summary-only parse must not wipe
	// richer facts (explicit ends, phase dates) an earlier full read stored.
	existing.Version = firstNonEmptyStr(row.Version, existing.Version)
	existing.VersionName = firstNonEmptyStr(row.VersionName, existing.VersionName)
	existing.UpdateEnd = firstTimePtr(row.UpdateEnd, existing.UpdateEnd)
	existing.VersionEnd = firstTimePtr(row.VersionEnd, existing.VersionEnd)
	existing.PreviewStart = firstTimePtr(row.PreviewStart, existing.PreviewStart)
	existing.PhaseUpdate = firstTimePtr(row.PhaseUpdate, existing.PhaseUpdate)
	if row.AnnouncementID != nil {
		existing.AnnouncementID = row.AnnouncementID
	}
	return c.db.Save(&existing).Error
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstTimePtr(values ...*time.Time) *time.Time {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// collectVideosAndDynamics fetches official preview videos and recent
// dynamics (paged, noise-filtered), persists them as posts, and returns the
// preview-video planner inputs.
func (c *Collector) collectVideosAndDynamics(ctx context.Context, game games.Game,
	current *parse.ArticleFacts, now time.Time, errs []string) ([]parse.Video, []string) {
	// Series rows expose the uploader as `upMid`; search rows use `mid`.
	official := func(rows []bili.Archive) []bili.Archive {
		var out []bili.Archive
		for _, row := range rows {
			if row.AuthorID == game.Mid {
				out = append(out, row)
			}
		}
		return out
	}
	isPreview := func(rows []bili.Archive) bool {
		for _, row := range rows {
			if parse.IsPreviewTitle(row.Title) {
				return true
			}
		}
		return false
	}

	var archives []bili.Archive
	series, seriesErr := c.bili.FeatureVideos(ctx, game.Mid, game.SeriesKeyword)
	if seriesErr != nil {
		c.log.Info("gacha: "+game.Name+" feature video list unavailable", "error", seriesErr.Error())
	}
	archives = append(archives, official(series)...)

	// The official series endpoint is the reliable path (WuWa only appears
	// there); search is a fallback for accounts whose series is not filled in.
	if !isPreview(archives) {
		for _, keyword := range []string{
			game.Name + " 版本前瞻特别节目",
			game.SearchKeyword,
		} {
			found, searchErr := c.bili.SearchVideos(ctx, keyword)
			if searchErr != nil {
				errs = append(errs, "前瞻视频搜索："+searchErr.Error())
				break
			}
			matched := official(found)
			archives = append(archives, matched...)
			if isPreview(matched) {
				break
			}
		}
	}

	// Do not let the previous version's preview video masquerade as the next
	// one: only uploads after the current version went live are candidates.
	floor := now.Add(-120 * day)
	if current != nil && !current.UpdateStart.IsZero() {
		floor = current.UpdateStart.Add(-day)
	}
	var videos []parse.Video
	seen := map[string]bool{}
	for _, row := range archives {
		if row.BVID == "" || row.Pubdate <= 0 {
			continue
		}
		if time.Unix(row.Pubdate, 0).Before(floor) {
			continue
		}
		if !parse.IsPreviewTitle(row.Title) {
			continue
		}
		if seen[row.BVID] {
			continue
		}
		seen[row.BVID] = true
		videos = append(videos, parse.Video{
			BVID: row.BVID, Title: row.Title, Pubdate: time.Unix(row.Pubdate, 0).UTC(),
		})
		if err := c.upsertPost(&model.Post{
			GameID: string(game.ID), SourceType: "video", ExternalID: row.BVID,
			URL: bili.VideoURL(row.BVID), Title: row.Title, Author: game.Name,
			PublishTime: time.Unix(row.Pubdate, 0).UTC(), FetchedAt: now,
			CoverURL: row.Pic,
		}); err != nil {
			errs = append(errs, "视频入库："+err.Error())
		}
	}

	// Dynamics are optional: the endpoint answers -352 for hours. Page a few
	// screens deep, keep only signal posts, and rely on the sticky stored
	// announcements when it stays down.
	dynamics, dynErr := c.bili.Dynamics(ctx, game.Mid)
	if dynErr != nil {
		c.log.Info("gacha: "+game.Name+" dynamics unavailable", "error", dynErr.Error())
	}
	cutoff := now.Add(-35 * day)
	for _, dyn := range dynamics {
		if dyn.Text == "" || dyn.Pubdate <= 0 {
			continue
		}
		published := time.Unix(dyn.Pubdate, 0).UTC()
		if published.Before(cutoff) {
			continue
		}
		if isDynamicNoise(dyn.Text) {
			continue
		}
		cover := ""
		if len(dyn.Pictures) > 0 {
			cover = dyn.Pictures[0]
		}
		if err := c.upsertPost(&model.Post{
			GameID: string(game.ID), SourceType: "dynamic", ExternalID: dyn.ID,
			URL: bili.DynamicURL(dyn.ID), Title: dyn.Text, Author: game.Name,
			Summary: dyn.Text, PublishTime: published, FetchedAt: now,
			CoverURL: cover,
		}); err != nil {
			errs = append(errs, "动态入库："+err.Error())
		}
	}
	return videos, errs
}

// finalizeGame persists the (possibly stagger-adjusted) plan, updates the
// game state row and refreshes the cover.
func (c *Collector) finalizeGame(out *gameOutcome) {
	if out == nil {
		return
	}
	log := c.logGame(out.game)
	id := out.game.ID
	now := time.Now().UTC()
	if err := c.upsertPrediction(id, out.plan, out.articleID, now); err != nil {
		out.errs = append(out.errs, "预测入库："+err.Error())
	}

	status := "ok"
	switch {
	case out.plan == nil:
		status = "error"
	case len(out.errs) > 0:
		status = "partial"
	}
	lastError := joinErrors(out.errs)
	if out.plan == nil && lastError == "" {
		lastError = "暂无可用的版本信息"
	}
	refreshed := time.Now().UTC()
	c.db.Model(&model.GameState{}).Where("id = ?", string(id)).Updates(map[string]any{
		"status":          status,
		"last_error":      lastError,
		"last_refresh_at": refreshed,
		"last_article_id": out.state.LastArticleID,
	})
	if out.plan == nil {
		log("no plan: %s", lastError)
	} else {
		log("plan %s: update %s (%s), preview %s (%s) phase=%v", out.plan.Version,
			shortCST(out.plan.UpdateStart), out.plan.UpdateEvidence,
			shortCST(out.plan.PreviewStart), out.plan.PreviewEvidence,
			out.plan.IsPhaseUpdate)
	}
	log("refresh %s in %d ms", status, time.Since(out.started).Milliseconds())

	// Best-effort game main visual: article banner > preview video cover >
	// dynamic picture. The fallback SVG always exists.
	c.refreshGameCover(context.Background(), id)
}

// maxCoverHealPerGame bounds per-cycle cover downloads: the image CDNs are
// generous, but a handful per game per refresh keeps the heal polite.
const maxCoverHealPerGame = 8

// healPostCovers backfills missing post covers. Two gaps exist in stored
// rows: bootstrap-backfilled articles were stored without cover_url, and
// video/dynamic posts keep only the remote CDN URL — the image CDNs reject
// foreign referers, so a post is only displayable once its cover is
// downloaded locally. The article list already in hand carries image_urls,
// so recent article rows heal without extra API calls; every post that has
// a cover URL but no local file gets downloaded, newest first, until no
// gaps remain.
func (c *Collector) healPostCovers(ctx context.Context, game games.Game, articles []bili.Article) {
	var posts []model.Post
	if err := c.db.Where("game_id = ? AND cover_path = ''", string(game.ID)).
		Order("publish_time DESC").Limit(2 * maxCoverHealPerGame).
		Find(&posts).Error; err != nil || len(posts) == 0 {
		return
	}
	listImages := make(map[string]string, len(articles))
	for i := range articles {
		if imgs := articles[i].ImageURLs; len(imgs) > 0 && imgs[0] != "" {
			listImages[articles[i].IDString()] = imgs[0]
		}
	}
	healed, downloads := 0, 0
	for i := range posts {
		post := &posts[i]
		if post.CoverURL == "" && post.SourceType == "article" {
			post.CoverURL = listImages[post.ExternalID]
		}
		if post.CoverURL == "" || downloads >= maxCoverHealPerGame {
			continue
		}
		path := c.covers.SavePost(ctx, post.ID, post.CoverURL)
		if path == "" {
			continue
		}
		downloads++
		if err := c.db.Model(post).Updates(map[string]any{
			"cover_url": post.CoverURL, "cover_path": path,
		}).Error; err == nil {
			healed++
		}
	}
	if healed > 0 {
		c.logGame(game)("cover heal: %d posts localized (%d downloads)", healed, downloads)
	}
}

// refreshGameCover stores the newest event main visual for a game:
// update-notice banner > newest preview video cover > newest dynamic picture.
func (c *Collector) refreshGameCover(ctx context.Context, id games.GameID) {
	pick := func(sourceType string) string {
		var post model.Post
		err := c.db.Where("game_id = ? AND source_type = ? AND cover_url != ''",
			string(id), sourceType).Order("publish_time DESC").First(&post).Error
		if err != nil {
			return ""
		}
		return post.CoverURL
	}
	image := pick("article")
	if image == "" {
		image = pick("video")
	}
	if image == "" {
		image = pick("dynamic")
	}
	if image == "" {
		return
	}
	if path := c.covers.SaveGame(ctx, string(id), image); path != "" {
		c.db.Model(&model.GameState{}).Where("id = ?", string(id)).
			Update("cover_path", path)
	}
}

// carryOfficialPreview keeps an explicitly announced preview time across
// feed outages: an official (or agent-confirmed) time for the upcoming
// version is never downgraded back to arithmetic rules within its window.
func (c *Collector) carryOfficialPreview(id games.GameID, plan *parse.Plan, now time.Time) *parse.Plan {
	var prior model.Prediction
	err := c.db.Where("game_id = ? AND active = ?", string(id), true).First(&prior).Error
	if err != nil {
		return plan
	}
	if prior.PreviewConfidence != "official" && prior.PreviewConfidence != "agent" {
		return plan
	}
	if prior.PreviewStart == nil || prior.PreviewEnd == nil {
		return plan
	}
	// Keep it until a few days after the stream; afterwards the replay video
	// and the next version's notice take over.
	if now.After(prior.PreviewEnd.Add(3 * day)) {
		return plan
	}
	if plan != nil && plan.Version != prior.Version && plan.Version != "" && prior.Version != "" {
		return plan
	}
	if plan != nil && (plan.PreviewConfidence == "official" || plan.PreviewConfidence == "agent") {
		return plan
	}
	if plan == nil {
		plan = &parse.Plan{Version: prior.Version, UpdateConfidence: "inferred"}
	}
	plan.PreviewStart = *prior.PreviewStart
	plan.PreviewEnd = *prior.PreviewEnd
	plan.PreviewConfidence = prior.PreviewConfidence
	plan.PreviewEvidence = prior.PreviewEvidence
	return plan
}

// mergeOverride layers an Agent/human correction over the rule-derived plan.
// A correction older than the newest official article is ignored.
func (c *Collector) mergeOverride(game games.Game, id games.GameID, plan *parse.Plan) *parse.Plan {
	var override model.Override
	if err := c.db.First(&override, "game_id = ?", string(id)).Error; err != nil {
		return plan
	}
	var notice model.Post
	if err := c.db.Where("game_id = ? AND source_type = ? AND is_update_notice = ?",
		string(id), "article", true).Order("publish_time DESC").First(&notice).Error; err == nil {
		if notice.PublishTime.After(override.AppliedAt) {
			return plan
		}
	}

	updateStart := derefTime(override.UpdateStart)
	updateEnd := derefTime(override.UpdateEnd)
	previewStart := derefTime(override.PreviewStart)
	previewEnd := derefTime(override.PreviewEnd)
	if updateStart.IsZero() && previewStart.IsZero() {
		return plan
	}

	if plan == nil {
		plan = &parse.Plan{Version: override.Version}
	} else if override.Version != "" {
		plan.Version = override.Version
	}
	if !updateStart.IsZero() {
		plan.UpdateStart = updateStart
		if updateEnd.IsZero() {
			plan.UpdateEnd = parse.DefaultUpdateEnd(updateStart, game)
		} else {
			plan.UpdateEnd = updateEnd
		}
		plan.UpdateConfidence = "agent"
		if override.Note != "" {
			plan.UpdateEvidence = override.Note
		} else {
			plan.UpdateEvidence = "Agent 核对官方公告"
		}
	}
	if !previewStart.IsZero() {
		plan.PreviewStart = previewStart
		if previewEnd.IsZero() {
			_, endMinutes := game.PreviewWindow()
			plan.PreviewEnd = parse.AtMinutes(previewStart, endMinutes)
		} else {
			plan.PreviewEnd = previewEnd
		}
		plan.PreviewConfidence = "agent"
		if override.Note != "" {
			plan.PreviewEvidence = override.Note
		} else {
			plan.PreviewEvidence = "Agent 核对官方公告"
		}
	}
	return plan
}

// upsertPrediction stores a new prediction row when the plan changed and
// supersedes the previous active one.
func (c *Collector) upsertPrediction(id games.GameID, plan *parse.Plan, postID uint, now time.Time) error {
	var prior model.Prediction
	priorErr := c.db.Where("game_id = ? AND active = ?", string(id), true).First(&prior).Error

	row := model.Prediction{
		GameID: string(id), Active: true, GeneratedAt: now,
	}
	if plan != nil {
		row.Version = plan.Version
		row.VersionName = plan.VersionName
		row.UpdateStart = ptrTime(plan.UpdateStart)
		row.UpdateEnd = ptrTime(plan.UpdateEnd)
		row.PreviewStart = ptrTime(plan.PreviewStart)
		row.PreviewEnd = ptrTime(plan.PreviewEnd)
		row.UpdateConfidence = plan.UpdateConfidence
		row.PreviewConfidence = plan.PreviewConfidence
		row.UpdateEvidence = plan.UpdateEvidence
		row.PreviewEvidence = plan.PreviewEvidence
		row.IsPhaseUpdate = plan.IsPhaseUpdate
		if postID != 0 {
			row.AnnouncementID = &postID
		}
	}
	if priorErr == nil && predictionEqual(&prior, &row) {
		return nil
	}
	if err := c.db.Create(&row).Error; err != nil {
		return err
	}
	if priorErr == nil {
		prior.Active = false
		superseded := row.ID
		prior.SupersededBy = &superseded
		c.db.Save(&prior)
	}
	return nil
}

func predictionEqual(a, b *model.Prediction) bool {
	return a.GameID == b.GameID &&
		a.Version == b.Version &&
		a.VersionName == b.VersionName &&
		timeEqualPtr(a.UpdateStart, b.UpdateStart) &&
		timeEqualPtr(a.UpdateEnd, b.UpdateEnd) &&
		timeEqualPtr(a.PreviewStart, b.PreviewStart) &&
		timeEqualPtr(a.PreviewEnd, b.PreviewEnd) &&
		a.UpdateConfidence == b.UpdateConfidence &&
		a.PreviewConfidence == b.PreviewConfidence &&
		a.UpdateEvidence == b.UpdateEvidence &&
		a.PreviewEvidence == b.PreviewEvidence &&
		a.IsPhaseUpdate == b.IsPhaseUpdate
}

func timeEqualPtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// planFromPrediction reconstructs a plan struct from a stored prediction row.
func planFromPrediction(row *model.Prediction) *parse.Plan {
	if row == nil {
		return nil
	}
	return &parse.Plan{
		Version:           row.Version,
		VersionName:       row.VersionName,
		UpdateStart:       derefTime(row.UpdateStart),
		UpdateEnd:         derefTime(row.UpdateEnd),
		PreviewStart:      derefTime(row.PreviewStart),
		PreviewEnd:        derefTime(row.PreviewEnd),
		UpdateConfidence:  row.UpdateConfidence,
		PreviewConfidence: row.PreviewConfidence,
		UpdateEvidence:    row.UpdateEvidence,
		PreviewEvidence:   row.PreviewEvidence,
		IsPhaseUpdate:     row.IsPhaseUpdate,
	}
}

// RecomputeGame re-derives one game's prediction without any network access.
// usePriorBase keeps the previous active prediction as the base plan (it
// carries the last full collection's video/dynamic evidence) with the stored
// override layered over it — the plugin's mergePlan semantics, used when a
// correction is written. With usePriorBase=false the base is re-derived from
// the stored announcement facts by the pure rules, used when a correction is
// cleared so stale agent values cannot survive. The next scheduled refresh
// re-derives everything from fresh sources either way.
func (c *Collector) RecomputeGame(id games.GameID, usePriorBase bool) error {
	game, ok := games.Get(id)
	if !ok {
		return fmt.Errorf("unknown game %s", id)
	}
	now := time.Now().UTC()

	var prior model.Prediction
	var plan *parse.Plan
	havePrior := c.db.Where("game_id = ? AND active = ?", string(id), true).
		First(&prior).Error == nil
	if havePrior && usePriorBase {
		plan = planFromPrediction(&prior)
	} else if row, have := c.latestVersion(id); have {
		plan = parse.PlanNext(game, factsFromVersion(row), now, nil,
			c.storedAnnouncements(game, now), c.gameCadence(id))
		plan = c.carryOfficialPreview(id, plan, now)
	}
	plan = c.mergeOverride(game, id, plan)
	if err := c.upsertPrediction(id, plan, 0, now); err != nil {
		return err
	}
	status := "ok"
	if plan == nil {
		status = "error"
	}
	c.db.Model(&model.GameState{}).Where("id = ?", string(id)).Updates(map[string]any{
		"status": status,
	})
	return nil
}

// --- bootstrap (first-initialization guided backfill) ----------------------
//
// A fresh database knows only the newest announcement per game — far too thin
// to calibrate cadence or preview rhythms. The bootstrap walks the official
// column history (and the preview-video collection), rebuilds the version
// table over roughly the last 18 months, then derives the per-game stats the
// planner prefers over static defaults. It runs automatically when the
// history is thin and can be forced via POST /api/admin/bootstrap.

const bootstrapHistoryDays = 540

// NeedsBootstrap reports whether the guided initialization should run.
func (c *Collector) NeedsBootstrap() bool {
	var state model.CollectState
	if err := c.db.First(&state, 1).Error; err != nil || state.BootstrapState == "" {
		return true
	}
	return false
}

// RequestBootstrap kicks off the guided backfill asynchronously; it returns
// false when one is already running.
func (c *Collector) RequestBootstrap() bool {
	c.mu.Lock()
	if c.bootRunning {
		c.mu.Unlock()
		return false
	}
	c.bootRunning = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			c.bootRunning = false
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := c.RunBootstrap(ctx); err != nil {
			c.log.Warn("gacha: bootstrap failed", "error", err.Error())
		}
	}()
	return true
}

// BootstrapRunning reports whether the guided initialization is in flight.
func (c *Collector) BootstrapRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bootRunning
}

// RunBootstrap backfills version history and recomputes the calibration
// stats. Serial by design: it shares the rate-limited Bilibili client with
// the regular cycles, and it waits for (and is awaited by) any refresh in
// flight so the post-bootstrap refresh actually sees the new stats.
func (c *Collector) RunBootstrap(ctx context.Context) error {
	c.waitForIdle(ctx, 5*time.Minute)
	c.setBootstrapState("running", nil)

	for _, id := range games.IDs() {
		game, ok := games.Get(id)
		if !ok {
			continue
		}
		var state model.GameState
		if err := c.db.First(&state, "id = ?", string(id)).Error; err != nil || !state.Enabled {
			continue
		}
		if ctx.Err() != nil {
			c.setBootstrapState("", nil)
			return ctx.Err()
		}
		c.backfillArticles(ctx, game)
		c.backfillPreviewTimes(ctx, game)
	}

	if err := c.RecomputeAllStats(); err != nil {
		c.setBootstrapState("", nil)
		return err
	}
	now := time.Now().UTC()
	c.setBootstrapState("done", &now)

	// Regenerate predictions with the fresh calibration; a refresh that is
	// already running (started before the stats existed) is waited out.
	c.waitForIdle(ctx, 20*time.Minute)
	c.RequestRefresh(nil)
	return nil
}

// waitForIdle blocks until no refresh cycle is running (or the context ends).
func (c *Collector) waitForIdle(ctx context.Context, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for c.Running() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
	// The caller's own RequestRefresh may still lose the race to an external
	// trigger; give one more short window.
	for c.Running() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			if time.Now().After(deadline.Add(30 * time.Second)) {
				return
			}
		}
	}
}

func (c *Collector) setBootstrapState(status string, at *time.Time) {
	updates := map[string]any{"bootstrap_state": status, "bootstrap_at": at}
	c.db.Model(&model.CollectState{}).Where("id = 1").Updates(updates)
}

// backfillArticles walks the official column list backwards and upserts every
// version-update notice it can parse. Full-column reads are capped per game:
// most summaries already carry the maintenance date.
func (c *Collector) backfillArticles(ctx context.Context, game games.Game) {
	log := c.logGame(game)
	now := time.Now().UTC()
	fullReads := 0
	versions := 0
	for pn := 1; pn <= 8; pn++ {
		articles, err := c.bili.ArticleListPage(ctx, game.Mid, pn)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log("backfill page %d failed: %s", pn, err.Error())
			return
		}
		if len(articles) == 0 {
			break
		}
		stop := false
		for i := range articles {
			item := &articles[i]
			if item.PublishTime > 0 &&
				time.Unix(item.PublishTime, 0).Before(now.Add(-bootstrapHistoryDays*day)) {
				stop = true
				break
			}
			if item.Title == "" || game.ExcludeTitle.MatchString(item.Title) ||
				!game.UpdateTitle.MatchString(item.Title) {
				continue
			}
			published := time.Unix(item.PublishTime, 0).UTC()
			summaryText := bili.StripHTML(item.Summary)
			var facts *parse.ArticleFacts
			if summaryText != "" {
				parsed := parse.ParseArticle(item.Title, summaryText, published, game)
				facts = &parsed
			}
			if facts != nil && !facts.HasUpdateStart() && isCVID(item.IDString()) && fullReads < 14 {
				if view, viewErr := c.bili.ArticleView(ctx, item.IDString()); viewErr == nil {
					full := parse.ParseArticle(view.Title, bili.StripHTML(view.Content),
						time.Unix(view.PublishTime, 0).UTC(), game)
					facts = ptrTo(parse.MergeArticle(facts, &full))
					fullReads++
				}
			}
			if facts == nil || !facts.HasUpdateStart() {
				continue
			}
			if err := c.upsertVersion(game, facts, 0); err == nil {
				versions++
			}
			// Keep the posts page useful: store the recent notices' summaries.
			// Cover localization is left to healPostCovers on later cycles.
			if published.After(now.Add(-180 * day)) {
				cover := ""
				if len(item.ImageURLs) > 0 {
					cover = item.ImageURLs[0]
				}
				_ = c.upsertPost(&model.Post{
					GameID: string(game.ID), SourceType: "article", ExternalID: item.IDString(),
					URL: bili.ArticleURL(item.IDString()), Title: item.Title,
					Summary: summaryText, Author: game.Name,
					PublishTime: published, FetchedAt: now, IsUpdateNotice: true,
					CoverURL: cover,
				})
			}
		}
		if stop {
			break
		}
	}
	log("backfill: %d versions (full reads %d)", versions, fullReads)
}

func ptrTo[T any](v T) *T { return &v }

// backfillPreviewTimes maps historical preview videos onto version rows that
// lack a preview time, so the stats have the preview rhythm to learn from.
func (c *Collector) backfillPreviewTimes(ctx context.Context, game games.Game) {
	var rows []model.Version
	if err := c.db.Where("game_id = ? AND preview_start IS NULL", string(game.ID)).
		Order("update_start ASC").Find(&rows).Error; err != nil || len(rows) == 0 {
		return
	}
	archives, err := c.bili.FeatureVideos(ctx, game.Mid, game.SeriesKeyword)
	if err != nil {
		c.log.Info("gacha: "+game.Name+" backfill videos unavailable", "error", err.Error())
		return
	}
	filled := 0
	for i := range archives {
		row := archives[i]
		if row.BVID == "" || row.Pubdate <= 0 || !parse.IsPreviewTitle(row.Title) {
			continue
		}
		video := parse.Video{BVID: row.BVID, Title: row.Title, Pubdate: time.Unix(row.Pubdate, 0).UTC()}
		for v := range rows {
			version := &rows[v]
			if version.PreviewStart != nil {
				continue
			}
			at, ok := parse.PreviewFromVideo(video, game, version.UpdateStart)
			if !ok {
				continue
			}
			if lead, ok := parse.DayDiffPublic(at, version.UpdateStart); !ok || lead < 2 || lead > 28 {
				continue
			}
			utc := at.UTC()
			version.PreviewStart = &utc
			if end, ok := parse.CSTEndAtDefault(at, game); ok {
				utcEnd := end.UTC()
				version.PreviewEnd = &utcEnd
			}
			c.db.Model(&model.Version{}).Where("id = ?", version.ID).
				Updates(map[string]any{"preview_start": version.PreviewStart, "preview_end": version.PreviewEnd})
			filled++
			break
		}
	}
	if filled > 0 {
		c.logGame(game)("backfill: %d preview times", filled)
	}
}

// RecomputeAllStats derives the calibration table from the version history.
func (c *Collector) RecomputeAllStats() error {
	for _, id := range games.IDs() {
		var rows []model.Version
		if err := c.db.Where("game_id = ?", string(id)).
			Order("update_start ASC").Find(&rows).Error; err != nil {
			return err
		}
		stats := model.GameStats{GameID: string(id), ComputedAt: time.Now().UTC()}
		if len(rows) >= 2 {
			cycles := make([]int, 0, len(rows)-1)
			for i := 1; i < len(rows); i++ {
				if d, ok := parse.DayDiffPublic(rows[i-1].UpdateStart, rows[i].UpdateStart); ok && d >= 14 && d <= 90 {
					cycles = append(cycles, d)
				}
			}
			if v, ok := parse.MedianInt(cycles); ok {
				stats.CycleDays = v
			}
			weekdays := make([]int, 0, len(rows))
			for _, row := range rows {
				if p, ok := parse.CSTParts(row.UpdateStart); ok {
					weekdays = append(weekdays, p.Weekday)
				}
			}
			if v, ok := parse.ModeInt(weekdays); ok {
				stats.UpdateWeekday = v
			}
			stats.SampleCount = len(rows)
		}
		var leads, previewWeekdays, previewMinutes []int
		for _, row := range rows {
			if row.PreviewStart == nil {
				continue
			}
			if p, ok := parse.CSTParts(*row.PreviewStart); ok {
				previewWeekdays = append(previewWeekdays, p.Weekday)
				previewMinutes = append(previewMinutes, p.Hour*60+p.Minute)
			}
			if d, ok := parse.DayDiffPublic(*row.PreviewStart, row.UpdateStart); ok && d >= 2 && d <= 28 {
				leads = append(leads, d)
			}
		}
		if v, ok := parse.MedianInt(leads); ok {
			stats.PreviewLeadDays = v
		}
		if v, ok := parse.ModeInt(previewWeekdays); ok {
			stats.PreviewWeekday = v
		}
		if v, ok := parse.ModeInt(previewMinutes); ok {
			stats.PreviewStartMinute = v
		}
		var notices []model.Post
		hours := []int{}
		if err := c.db.Where("game_id = ? AND is_update_notice = ?", string(id), true).
			Find(&notices).Error; err == nil {
			for _, post := range notices {
				if p, ok := parse.CSTParts(post.PublishTime); ok {
					hours = append(hours, p.Hour)
				}
			}
		}
		if v, ok := parse.ModeInt(hours); ok {
			stats.AnnounceHour = v
		}

		var existing model.GameStats
		if err := c.db.First(&existing, "game_id = ?", string(id)).Error; err == nil {
			stats.CreatedAt = existing.CreatedAt
		}
		if err := c.db.Save(&stats).Error; err != nil {
			return err
		}
	}
	return nil
}
