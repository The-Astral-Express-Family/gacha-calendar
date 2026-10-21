// Package collect orchestrates one refresh cycle per game, ported from the
// plugin's service.js:
//
//  1. official column list → newest version-update notice
//  2. parse its 255-char list summary; fetch the full column only when the
//     summary lacks a date/version, or Endfield needs a deeper phase date
//  3. official preview videos + recent official dynamics
//  4. PlanNext pure rules → update / preview times (with evidence)
//  5. persist posts / version / prediction rows and download covers
//
// Failures never invent dates: the previous parsed version and prediction
// stay in place, and the error is reported on the game state.
package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/covers"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/model"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/parse"
)

const day = 24 * time.Hour

// predictionKeepWindow: a missing teaser/replay (including an outage) is not
// a retraction — explicit same-version timing is retained this long.
const predictionKeepWindow = 2 * day

type Collector struct {
	db     *gorm.DB
	bili   *bili.Client
	covers *covers.Downloader
	log    *slog.Logger

	mu      sync.Mutex
	running bool
}

func New(db *gorm.DB, client *bili.Client, dl *covers.Downloader, logger *slog.Logger) *Collector {
	return &Collector{db: db, bili: client, covers: dl, log: logger}
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
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		c.RefreshGames(ctx, targets)
	}()
	return true
}

// Start runs the scheduler loop until ctx is done: one cycle shortly after
// boot (when the data is stale), then every interval.
func (c *Collector) Start(ctx context.Context, interval, startupDelay time.Duration) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupDelay):
		}
		if c.stale(interval) {
			c.RequestRefresh(nil)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.RequestRefresh(nil)
			}
		}
	}()
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

// RefreshGames runs the per-game refresh serially (the Bilibili client is a
// serial chain anyway) and updates the service-level collect state.
func (c *Collector) RefreshGames(ctx context.Context, ids []games.GameID) []GameResult {
	results := make([]GameResult, 0, len(ids))
	okCount := 0
	for _, id := range ids {
		result := c.refreshGame(ctx, id)
		results = append(results, result)
		if result.Status == "ok" {
			okCount++
		}
		if ctx.Err() != nil {
			break
		}
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

// latestVersion loads the newest parsed version row for a game.
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
	*post = existing
	return c.db.Save(&existing).Error
}

// refreshGame collects one game end to end.
func (c *Collector) refreshGame(ctx context.Context, id games.GameID) GameResult {
	game, ok := games.Get(id)
	if !ok {
		return GameResult{Game: string(id), Status: "error", Error: "unknown game"}
	}
	started := time.Now()
	var state model.GameState
	if err := c.db.First(&state, "id = ?", string(id)).Error; err != nil {
		return GameResult{Game: string(id), Status: "error", Error: "game state missing"}
	}
	if !state.Enabled {
		return GameResult{Game: string(id), Status: "skipped"}
	}

	now := time.Now().UTC()
	var errs []string
	log := func(format string, args ...any) {
		c.log.Info("gacha: " + game.Name + " " + fmt.Sprintf(format, args...))
	}
	log("refresh start (article=%s)", state.LastArticleID)

	// 1. Official column list.
	articles, err := c.bili.ArticleList(ctx, game.Mid)
	if err != nil {
		if ctx.Err() != nil {
			return GameResult{Game: string(id), Status: "error", Error: ctx.Err().Error()}
		}
		errs = append(errs, "公告列表："+err.Error())
		c.log.Warn("gacha: "+game.Name+" article list failed", "error", err.Error())
	}
	log("article list: %d rows", len(articles))

	picked := pickArticle(game, articles, now)
	var articlePostID uint
	if picked != nil {
		if picked.IDString() != state.LastArticleID {
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

			// Summary-first merge: only read the heavily rate-limited full
			// column when the summary lacks a decisive field.
			missing := missingFields(game, facts)
			needsFullText := isCVID(picked.IDString()) && len(missing) > 0
			if facts != nil {
				log("summary parse: version=%s update=%s missing=%v full_text=%v",
					facts.Version, shortCST(facts.UpdateStart), missing, needsFullText)
			} else {
				log("summary parse: empty summary")
			}

			post := &model.Post{
				GameID: string(id), SourceType: "article", ExternalID: picked.IDString(),
				URL: bili.ArticleURL(picked.IDString()), Title: picked.Title,
				Summary: summaryText, Author: game.Name,
				PublishTime: published, FetchedAt: now, CoverURL: cover,
				IsUpdateNotice: true,
			}
			if err := c.upsertPost(post); err != nil {
				errs = append(errs, "公告入库："+err.Error())
			}
			articlePostID = post.ID

			if needsFullText {
				view, viewErr := c.bili.ArticleView(ctx, picked.IDString())
				if viewErr != nil {
					if ctx.Err() != nil {
						return GameResult{Game: string(id), Status: "error", Error: ctx.Err().Error()}
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
			_, haveVersion := c.latestVersion(id)
			if facts != nil && (facts.HasUpdateStart() || !haveVersion) {
				if facts.HasUpdateStart() {
					if err := c.upsertVersion(game, facts, articlePostID); err != nil {
						errs = append(errs, "版本入库："+err.Error())
					}
				}
				state.LastArticleID = picked.IDString()
			} else {
				errs = append(errs, "最新公告未解析出更新时间，沿用上次结果")
			}
		}
	} else if len(errs) == 0 {
		errs = append(errs, "未找到版本更新公告")
	}

	// Reconstruct current facts from the newest version row (the single
	// source of truth, also across restarts).
	var current *parse.ArticleFacts
	if row, ok := c.latestVersion(id); ok {
		current = factsFromVersion(row)
	}

	// 2. Preview videos + official dynamics → raw posts + planner inputs.
	var videos []parse.Video
	var announcements []parse.Announcement
	if current != nil {
		videos, announcements, errs = c.collectVideosAndDynamics(ctx, game, current, now, errs)
		log("preview videos=%d, recent dynamics=%d", len(videos), len(announcements))
	}

	// 3. Plan the next version.
	var plan *parse.Plan
	if current != nil {
		plan = parse.PlanNext(game, current, now, videos, announcements)
		plan = c.carryOfficialPreview(id, plan, now)
		plan = c.mergeOverride(game, id, plan)
	}

	if err := c.upsertPrediction(id, plan, articlePostID, now); err != nil {
		errs = append(errs, "预测入库："+err.Error())
	}

	status := "ok"
	switch {
	case plan == nil:
		status = "error"
	case len(errs) > 0:
		status = "partial"
	}
	lastError := joinErrors(errs)
	if plan == nil && lastError == "" {
		lastError = "暂无可用的版本信息"
	}
	refreshed := time.Now().UTC()
	c.db.Model(&model.GameState{}).Where("id = ?", string(id)).Updates(map[string]any{
		"status":          status,
		"last_error":      lastError,
		"last_refresh_at": refreshed,
		"last_article_id": state.LastArticleID,
	})
	if plan == nil {
		log("no plan: %s", lastError)
	} else {
		log("plan %s: update %s (%s), preview %s (%s)", plan.Version,
			shortCST(plan.UpdateStart), plan.UpdateEvidence,
			shortCST(plan.PreviewStart), plan.PreviewEvidence)
	}
	log("refresh %s in %d ms", status, time.Since(started).Milliseconds())

	// 4. Best-effort game main visual: article banner > preview video cover >
	// dynamic picture. The fallback SVG always exists.
	c.refreshGameCover(ctx, id)

	return GameResult{Game: string(id), Status: status, Error: lastError}
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
	if facts.Version == "" {
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
	existing.Version = row.Version
	existing.VersionName = row.VersionName
	existing.UpdateEnd = row.UpdateEnd
	existing.VersionEnd = row.VersionEnd
	existing.PreviewStart = row.PreviewStart
	existing.PhaseUpdate = row.PhaseUpdate
	existing.AnnouncementID = row.AnnouncementID
	return c.db.Save(&existing).Error
}

// collectVideosAndDynamics fetches official preview videos and recent
// dynamics, persists them as posts, and returns planner inputs (newest
// first).
func (c *Collector) collectVideosAndDynamics(ctx context.Context, game games.Game,
	current *parse.ArticleFacts, now time.Time, errs []string) ([]parse.Video, []parse.Announcement, []string) {
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
	if !current.UpdateStart.IsZero() {
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

	// Dynamics are optional: some accounts answer -352; failure is not fatal.
	var announcements []parse.Announcement
	dynamics, dynErr := c.bili.Dynamics(ctx, game.Mid)
	if dynErr != nil {
		c.log.Info("gacha: "+game.Name+" dynamics unavailable", "error", dynErr.Error())
	}
	cutoff := now.Add(-75 * day)
	for _, dyn := range dynamics {
		if dyn.Text == "" || dyn.Pubdate <= 0 {
			continue
		}
		published := time.Unix(dyn.Pubdate, 0).UTC()
		if published.Before(cutoff) {
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
		announcements = append(announcements, parse.Announcement{
			Text: dyn.Text, Pubdate: published, URL: bili.DynamicURL(dyn.ID),
		})
	}
	return videos, announcements, errs
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

// carryOfficialPreview keeps an explicitly announced preview time when a
// later outage misses the teaser/replay: never downgrade confirmed dates to
// arithmetic within the keep window.
func (c *Collector) carryOfficialPreview(id games.GameID, plan *parse.Plan, now time.Time) *parse.Plan {
	var prior model.Prediction
	err := c.db.Where("game_id = ? AND active = ?", string(id), true).First(&prior).Error
	if err != nil {
		return plan
	}
	if prior.PreviewConfidence != "official" || prior.PreviewStart == nil || prior.PreviewEnd == nil {
		return plan
	}
	if prior.PreviewStart.Before(now.Add(-predictionKeepWindow)) {
		return plan
	}
	if plan != nil && (plan.Version != prior.Version || plan.PreviewConfidence == "official") {
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
		row.UpdateStart = ptrTime(plan.UpdateStart)
		row.UpdateEnd = ptrTime(plan.UpdateEnd)
		row.PreviewStart = ptrTime(plan.PreviewStart)
		row.PreviewEnd = ptrTime(plan.PreviewEnd)
		row.UpdateConfidence = plan.UpdateConfidence
		row.PreviewConfidence = plan.PreviewConfidence
		row.UpdateEvidence = plan.UpdateEvidence
		row.PreviewEvidence = plan.PreviewEvidence
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
		timeEqualPtr(a.UpdateStart, b.UpdateStart) &&
		timeEqualPtr(a.UpdateEnd, b.UpdateEnd) &&
		timeEqualPtr(a.PreviewStart, b.PreviewStart) &&
		timeEqualPtr(a.PreviewEnd, b.PreviewEnd) &&
		a.UpdateConfidence == b.UpdateConfidence &&
		a.PreviewConfidence == b.PreviewConfidence &&
		a.UpdateEvidence == b.UpdateEvidence &&
		a.PreviewEvidence == b.PreviewEvidence
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
		UpdateStart:       derefTime(row.UpdateStart),
		UpdateEnd:         derefTime(row.UpdateEnd),
		PreviewStart:      derefTime(row.PreviewStart),
		PreviewEnd:        derefTime(row.PreviewEnd),
		UpdateConfidence:  row.UpdateConfidence,
		PreviewConfidence: row.PreviewConfidence,
		UpdateEvidence:    row.UpdateEvidence,
		PreviewEvidence:   row.PreviewEvidence,
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
		plan = parse.PlanNext(game, factsFromVersion(row), now, nil, nil)
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
