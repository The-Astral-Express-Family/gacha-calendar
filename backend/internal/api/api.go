package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/collect"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/model"
)

// Deps bundles what the handlers need.
type Deps struct {
	DB        *gorm.DB
	Collector *collect.Collector
	// Bili is the shared Bilibili client; its optional login session is what
	// the admin QR-login endpoints install.
	Bili         *bili.Client
	BiliSessions *bili.SessionStore
	// BiliRefresher drives the passport cookie rotation that keeps the login
	// alive; the admin refresh endpoint triggers it manually.
	BiliRefresher   *bili.SessionRefresher
	AdminToken      string
	RefreshInterval time.Duration
	Version         string
	Log             *slog.Logger
}

// Register mounts every route under the given mux (paths already carry /api
// and /healthz prefixes).
func Register(mux *http.ServeMux, d Deps) {
	h := &handlers{d: d, failures: newFailureLimiter()}

	mux.HandleFunc("GET /api/games", h.games)
	mux.HandleFunc("GET /api/predictions", h.predictions)
	mux.HandleFunc("GET /api/predictions/{id}", h.prediction)
	mux.HandleFunc("GET /api/posts", h.posts)
	mux.HandleFunc("GET /api/posts/{id}", h.post)
	mux.HandleFunc("GET /api/versions", h.versions)
	mux.HandleFunc("GET /api/meta", h.meta)

	// Admin routes are only mounted when a token is configured.
	if d.AdminToken != "" {
		mux.HandleFunc("POST /api/admin/refresh", h.admin(h.refresh))
		mux.HandleFunc("POST /api/admin/bootstrap", h.admin(h.bootstrap))
		mux.HandleFunc("PUT /api/admin/overrides/{game}", h.admin(h.putOverride))
		mux.HandleFunc("DELETE /api/admin/overrides/{game}", h.admin(h.deleteOverride))
		mux.HandleFunc("POST /api/admin/bili/login", h.admin(h.biliLoginQR))
		mux.HandleFunc("GET /api/admin/bili/login", h.admin(h.biliLoginStatus))
		mux.HandleFunc("GET /api/admin/bili/login/qr.png", h.admin(h.biliLoginQRPNG))
		mux.HandleFunc("POST /api/admin/bili/login/poll", h.admin(h.biliLoginPoll))
		mux.HandleFunc("POST /api/admin/bili/login/refresh", h.admin(h.biliLoginRefresh))
		mux.HandleFunc("DELETE /api/admin/bili/login", h.admin(h.biliLoginDelete))
	}
}

type handlers struct {
	d        Deps
	failures *failureLimiter

	// single-slot state for the admin QR login flow
	qrMu         sync.Mutex
	qrKey, qrURL string
	qrAt         time.Time
}

// dbError logs the underlying failure server-side and answers with a generic
// message so driver/SQL internals never reach the client.
func (h *handlers) dbError(w http.ResponseWriter, r *http.Request, code string, err error) {
	if h.d.Log != nil {
		h.d.Log.Error("api error", "code", code, "path", r.URL.Path, "error", err)
	}
	writeError(w, http.StatusInternalServerError, code, "服务内部错误，请稍后重试")
}

// --- read models (UTC RFC3339 everywhere; timezone metadata in /api/meta) ---

type timeMeta struct {
	UTC             string `json:"utc"`
	UnixSeconds     int64  `json:"unix_seconds"`
	EndUTC          string `json:"end_utc,omitempty"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
	UntilSeconds    *int64 `json:"until_seconds,omitempty"`
}

func timeMetaOf(t *time.Time, until *time.Time) *timeMeta {
	if t == nil {
		return nil
	}
	meta := &timeMeta{UTC: t.UTC().Format(time.RFC3339), UnixSeconds: t.Unix()}
	if until != nil {
		meta.EndUTC = until.UTC().Format(time.RFC3339)
		meta.DurationSeconds = int64(until.Sub(*t).Seconds())
	}
	return meta
}

func untilSeconds(t *time.Time, now time.Time) *int64 {
	if t == nil {
		return nil
	}
	delta := int64(t.Sub(now).Seconds())
	return &delta
}

type predictionView struct {
	ID                uint         `json:"id"`
	GameID            string       `json:"game_id"`
	GameName          string       `json:"game_name"`
	Version           string       `json:"version"`
	VersionName       string       `json:"version_name,omitempty"`
	Update            *timeMeta    `json:"update"`
	Preview           *timeMeta    `json:"preview"`
	UpdateConfidence  string       `json:"update_confidence"`
	PreviewConfidence string       `json:"preview_confidence"`
	UpdateEvidence    string       `json:"update_evidence"`
	PreviewEvidence   string       `json:"preview_evidence"`
	IsPhaseUpdate     bool         `json:"is_phase_update"`
	AnnouncementID    *uint        `json:"announcement_id"`
	AnnouncementURL   string       `json:"announcement_url,omitempty"`
	GeneratedAt       timeMetaOnly `json:"generated_at"`
	Active            bool         `json:"active"`
}

type timeMetaOnly struct {
	UTC string `json:"utc"`
}

func viewPrediction(p model.Prediction, now time.Time, gameName, announcementURL string) predictionView {
	view := predictionView{
		ID: p.ID, GameID: p.GameID, GameName: gameName, Version: p.Version,
		VersionName:       p.VersionName,
		Update:            timeMetaOf(p.UpdateStart, p.UpdateEnd),
		Preview:           timeMetaOf(p.PreviewStart, p.PreviewEnd),
		UpdateConfidence:  p.UpdateConfidence,
		PreviewConfidence: p.PreviewConfidence,
		UpdateEvidence:    p.UpdateEvidence,
		PreviewEvidence:   p.PreviewEvidence,
		IsPhaseUpdate:     p.IsPhaseUpdate,
		AnnouncementID:    p.AnnouncementID,
		AnnouncementURL:   announcementURL,
		GeneratedAt:       timeMetaOnly{UTC: p.GeneratedAt.UTC().Format(time.RFC3339)},
		Active:            p.Active,
	}
	if view.Update != nil {
		view.Update.UntilSeconds = untilSeconds(p.UpdateStart, now)
	}
	if view.Preview != nil {
		view.Preview.UntilSeconds = untilSeconds(p.PreviewStart, now)
	}
	return view
}

func (h *handlers) gameNames() map[string]string {
	names := map[string]string{}
	var states []model.GameState
	if err := h.d.DB.Find(&states).Error; err == nil {
		for _, s := range states {
			names[s.ID] = s.Name
		}
	}
	return names
}

func (h *handlers) announcementURL(id *uint) string {
	if id == nil {
		return ""
	}
	var post model.Post
	if err := h.d.DB.First(&post, *id).Error; err != nil {
		return ""
	}
	return post.URL
}

// parseGamesParam accepts `game=hsr` and `games=hsr,genshin` (repeatable).
func parseGamesParam(r *http.Request) []string {
	seen := map[string]bool{}
	var out []string
	for _, key := range []string{"game", "games"} {
		for _, value := range r.URL.Query()[key] {
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if part == "" || seen[part] {
					continue
				}
				if _, ok := games.Get(games.GameID(part)); !ok {
					continue
				}
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	return out
}

func sortOrder(r *http.Request, allowed map[string]string, defaultField string) (column string, descending bool, errMessage string) {
	field := r.URL.Query().Get("sort")
	if field == "" {
		field = defaultField
	}
	column, ok := allowed[field]
	if !ok {
		return "", false, "sort 允许字段：" + strings.Join(keysOf(allowed), ", ")
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	switch order {
	case "", "desc", "descending":
		descending = true
	case "asc", "ascending":
		descending = false
	default:
		return "", false, "order 允许 asc/desc"
	}
	return column, descending, ""
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- GET /api/predictions ---

var predictionSorts = map[string]string{
	"update_start":  "update_start",
	"preview_start": "preview_start",
	"generated_at":  "generated_at",
	"created_at":    "created_at",
}

func (h *handlers) predictions(w http.ResponseWriter, r *http.Request) {
	page, pageSize := clampPagination(r)
	column, descending, bad := sortOrder(r, predictionSorts, "update_start")
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid_sort", bad)
		return
	}

	query := h.d.DB.Model(&model.Prediction{})
	if gameIDs := parseGamesParam(r); len(gameIDs) > 0 {
		query = query.Where("game_id IN ?", gameIDs)
	}
	// active: absent → all rows (history paginates); "1"/"true" → only the
	// live plan per game; "0"/"false" → only superseded history.
	if set, val := parseBoolParam(r.URL.Query().Get("active")); set {
		query = query.Where("active = ?", val)
	}

	from, hasFrom, badFrom := parseRFC3339("from", r.URL.Query().Get("from"))
	to, hasTo, badTo := parseRFC3339("to", r.URL.Query().Get("to"))
	if badFrom != "" || badTo != "" {
		writeError(w, http.StatusBadRequest, "invalid_time", firstNonEmpty(badFrom, badTo))
		return
	}
	if hasFrom || hasTo {
		// `on` decides which event time the range applies to.
		on := strings.ToLower(r.URL.Query().Get("on"))
		switch on {
		case "", "any":
			conds := []string{}
			args := []any{}
			if hasFrom {
				conds = append(conds,
					"(update_start IS NOT NULL AND update_start >= ?)",
					"(preview_start IS NOT NULL AND preview_start >= ?)")
				args = append(args, from, from)
			}
			if hasTo {
				conds = append(conds,
					"(update_start IS NOT NULL AND update_start <= ?)",
					"(preview_start IS NOT NULL AND preview_start <= ?)")
				args = append(args, to, to)
			}
			query = query.Where("("+strings.Join(conds, " OR ")+")", args...)
		case "update":
			if hasFrom {
				query = query.Where("update_start >= ?", from)
			}
			if hasTo {
				query = query.Where("update_start <= ?", to)
			}
		case "preview":
			if hasFrom {
				query = query.Where("preview_start >= ?", from)
			}
			if hasTo {
				query = query.Where("preview_start <= ?", to)
			}
		default:
			writeError(w, http.StatusBadRequest, "invalid_on", "on 允许 any/update/preview")
			return
		}
	}
	if set, val := parseBoolParam(r.URL.Query().Get("upcoming")); set && val {
		nowUTC := time.Now().UTC()
		query = query.Where("update_start >= ? OR preview_start >= ?", nowUTC, nowUTC)
	}

	direction := "ASC"
	if descending {
		direction = "DESC"
	}
	// Deterministic pagination: tie-break on the primary key.
	query = query.Order(column + " " + direction + ", id " + direction)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	var rows []model.Prediction
	if err := query.Offset(offset(page, pageSize)).Limit(pageSize).Find(&rows).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}

	now := time.Now().UTC()
	names := h.gameNames()
	views := make([]predictionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewPrediction(row, now, names[row.GameID], h.announcementURL(row.AnnouncementID)))
	}
	writeList(w, views, page, pageSize, total)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (h *handlers) prediction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var row model.Prediction
	if err := h.d.DB.First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "预测不存在")
			return
		}
		h.dbError(w, r, "db_error", err)
		return
	}
	names := h.gameNames()
	writeJSON(w, http.StatusOK, map[string]any{
		"data": viewPrediction(row, time.Now().UTC(), names[row.GameID], h.announcementURL(row.AnnouncementID)),
	})
}

// --- GET /api/posts ---

// likeEscaper neutralizes LIKE wildcards in the q parameter so searches only
// match literal text (paired with the ESCAPE '\' clause in the query).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

var postSorts = map[string]string{
	"publish_time": "publish_time",
	"fetched_at":   "fetched_at",
	"created_at":   "created_at",
}

type postView struct {
	ID             uint         `json:"id"`
	GameID         string       `json:"game_id"`
	GameName       string       `json:"game_name"`
	SourceType     string       `json:"source_type"`
	ExternalID     string       `json:"external_id"`
	URL            string       `json:"url"`
	Title          string       `json:"title"`
	Summary        string       `json:"summary"`
	Content        string       `json:"content,omitempty"`
	Author         string       `json:"author"`
	PublishTime    timeMetaOnly `json:"publish_time"`
	FetchedAt      timeMetaOnly `json:"fetched_at"`
	CoverURL       string       `json:"cover_url,omitempty"`
	CoverPath      string       `json:"cover_path,omitempty"`
	IsUpdateNotice bool         `json:"is_update_notice"`
}

func viewPost(p model.Post, gameName string, withContent bool) postView {
	view := postView{
		ID: p.ID, GameID: p.GameID, GameName: gameName,
		SourceType: p.SourceType, ExternalID: p.ExternalID, URL: p.URL,
		Title: p.Title, Summary: p.Summary, Author: p.Author,
		PublishTime:    timeMetaOnly{UTC: p.PublishTime.UTC().Format(time.RFC3339)},
		FetchedAt:      timeMetaOnly{UTC: p.FetchedAt.UTC().Format(time.RFC3339)},
		CoverURL:       p.CoverURL,
		CoverPath:      p.CoverPath,
		IsUpdateNotice: p.IsUpdateNotice,
	}
	if withContent {
		view.Content = p.Content
	}
	return view
}

func (h *handlers) posts(w http.ResponseWriter, r *http.Request) {
	page, pageSize := clampPagination(r)
	column, descending, bad := sortOrder(r, postSorts, "publish_time")
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid_sort", bad)
		return
	}
	query := h.d.DB.Model(&model.Post{})
	if gameIDs := parseGamesParam(r); len(gameIDs) > 0 {
		query = query.Where("game_id IN ?", gameIDs)
	}
	if sourceType := r.URL.Query().Get("type"); sourceType != "" {
		switch sourceType {
		case "article", "dynamic", "video", "live", "official":
			query = query.Where("source_type = ?", sourceType)
		default:
			writeError(w, http.StatusBadRequest, "invalid_type", "type 允许 article/dynamic/video/live/official")
			return
		}
	}
	if set, val := parseBoolParam(r.URL.Query().Get("is_update_notice")); set {
		query = query.Where("is_update_notice = ?", val)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		like := "%" + likeEscaper.Replace(q) + "%"
		query = query.Where("title LIKE ? ESCAPE '\\' OR summary LIKE ? ESCAPE '\\'", like, like)
	}
	from, hasFrom, badFrom := parseRFC3339("from", r.URL.Query().Get("from"))
	to, hasTo, badTo := parseRFC3339("to", r.URL.Query().Get("to"))
	if badFrom != "" || badTo != "" {
		writeError(w, http.StatusBadRequest, "invalid_time", firstNonEmpty(badFrom, badTo))
		return
	}
	if hasFrom {
		query = query.Where("publish_time >= ?", from)
	}
	if hasTo {
		query = query.Where("publish_time <= ?", to)
	}

	direction := "ASC"
	if descending {
		direction = "DESC"
	}
	query = query.Order(column + " " + direction + ", id " + direction)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	var rows []model.Post
	if err := query.Offset(offset(page, pageSize)).Limit(pageSize).Find(&rows).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	names := h.gameNames()
	views := make([]postView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewPost(row, names[row.GameID], false))
	}
	writeList(w, views, page, pageSize, total)
}

func (h *handlers) post(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var row model.Post
	if err := h.d.DB.First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "公告不存在")
			return
		}
		h.dbError(w, r, "db_error", err)
		return
	}
	names := h.gameNames()
	writeJSON(w, http.StatusOK, map[string]any{"data": viewPost(row, names[row.GameID], true)})
}

// --- GET /api/versions (historical parsed versions) ---

var versionSorts = map[string]string{
	"update_start": "update_start",
	"created_at":   "created_at",
}

type versionView struct {
	ID             uint          `json:"id"`
	GameID         string        `json:"game_id"`
	GameName       string        `json:"game_name"`
	Version        string        `json:"version"`
	VersionName    string        `json:"version_name"`
	Update         *timeMeta     `json:"update"`
	VersionEnd     *timeMetaOnly `json:"version_end,omitempty"`
	Preview        *timeMeta     `json:"preview,omitempty"`
	AnnouncementID *uint         `json:"announcement_id"`
}

func (h *handlers) versions(w http.ResponseWriter, r *http.Request) {
	page, pageSize := clampPagination(r)
	column, descending, bad := sortOrder(r, versionSorts, "update_start")
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid_sort", bad)
		return
	}
	query := h.d.DB.Model(&model.Version{})
	if gameIDs := parseGamesParam(r); len(gameIDs) > 0 {
		query = query.Where("game_id IN ?", gameIDs)
	}
	from, hasFrom, badFrom := parseRFC3339("from", r.URL.Query().Get("from"))
	to, hasTo, badTo := parseRFC3339("to", r.URL.Query().Get("to"))
	if badFrom != "" || badTo != "" {
		writeError(w, http.StatusBadRequest, "invalid_time", firstNonEmpty(badFrom, badTo))
		return
	}
	if hasFrom {
		query = query.Where("update_start >= ?", from)
	}
	if hasTo {
		query = query.Where("update_start <= ?", to)
	}
	direction := "ASC"
	if descending {
		direction = "DESC"
	}
	query = query.Order(column + " " + direction + ", id " + direction)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	var rows []model.Version
	if err := query.Offset(offset(page, pageSize)).Limit(pageSize).Find(&rows).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	names := h.gameNames()
	views := make([]versionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, versionView{
			ID: row.ID, GameID: row.GameID, GameName: names[row.GameID],
			Version: row.Version, VersionName: row.VersionName,
			Update:         timeMetaOf(&row.UpdateStart, row.UpdateEnd),
			VersionEnd:     metaOnly(row.VersionEnd),
			Preview:        timeMetaOf(row.PreviewStart, row.PreviewEnd),
			AnnouncementID: row.AnnouncementID,
		})
	}
	writeList(w, views, page, pageSize, total)
}

func metaOnly(t *time.Time) *timeMetaOnly {
	if t == nil {
		return nil
	}
	return &timeMetaOnly{UTC: t.UTC().Format(time.RFC3339)}
}

// --- GET /api/games ---

type gameView struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	BilibiliSpace string          `json:"bilibili_space"`
	CycleDays     int             `json:"cycle_days"`
	Enabled       bool            `json:"enabled"`
	Status        string          `json:"status"`
	LastRefreshAt *timeMetaOnly   `json:"last_refresh_at,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	Cover         string          `json:"cover,omitempty"`
	PreviewCover  string          `json:"preview_cover,omitempty"`
	Fallback      string          `json:"fallback"`
	Calibrated    bool            `json:"calibrated"`
	VersionCount  int64           `json:"version_count"`
	Prediction    *predictionView `json:"prediction,omitempty"`
}

func (h *handlers) games(w http.ResponseWriter, r *http.Request) {
	var states []model.GameState
	if err := h.d.DB.Find(&states).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	now := time.Now().UTC()
	views := make([]gameView, 0, len(states))
	for _, state := range states {
		view := gameView{
			ID: state.ID, Name: state.Name,
			BilibiliSpace: "https://space.bilibili.com/" + state.Mid,
			CycleDays:     state.CycleDays,
			Enabled:       state.Enabled,
			Status:        state.Status,
			LastError:     state.LastError,
			Cover:         state.CoverPath,
			Fallback:      "/fallback/" + state.ID + ".svg",
		}
		if state.LastRefreshAt != nil {
			view.LastRefreshAt = metaOnly(state.LastRefreshAt)
		}
		var stats model.GameStats
		if err := h.d.DB.First(&stats, "game_id = ?", state.ID).Error; err == nil {
			view.Calibrated = stats.SampleCount >= 3
			view.CycleDays = maxInt(stats.CycleDays, state.CycleDays)
		}
		h.d.DB.Model(&model.Version{}).Where("game_id = ?", state.ID).Count(&view.VersionCount)
		var prediction model.Prediction
		if err := h.d.DB.Where("game_id = ? AND active = ?", state.ID, true).
			First(&prediction).Error; err == nil {
			p := viewPrediction(prediction, now, state.Name, h.announcementURL(prediction.AnnouncementID))
			view.Prediction = &p
		}
		view.PreviewCover = h.previewCover(state.ID, now)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": views})
}

// previewCover picks the newest image-bearing teaser post (官宣动态、直播
// 间公告横幅、官方源) as the preview event's main visual. Teaser-ness is the
// flag or a 前瞻/直播 title; the 60-day window drops last version's stale
// banner once a newer teaser exists — newest-first ordering does the rest.
func (h *handlers) previewCover(gameID string, now time.Time) string {
	var post model.Post
	err := h.d.DB.Where(
		"game_id = ? AND cover_path != '' AND publish_time > ? AND (is_teaser = ? OR title LIKE ? OR title LIKE ?)",
		gameID, now.Add(-60*24*time.Hour), true, "%前瞻%", "%直播%").
		Order("publish_time DESC").First(&post).Error
	if err != nil {
		return ""
	}
	return post.CoverPath
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- GET /api/meta ---

func (h *handlers) meta(w http.ResponseWriter, r *http.Request) {
	var state model.CollectState
	_ = h.d.DB.First(&state, 1).Error
	running := false
	bootRunning := false
	if h.d.Collector != nil {
		running = h.d.Collector.Running()
		bootRunning = h.d.Collector.BootstrapRunning()
	}
	var lastCycleAt, nextRunAt, bootstrapAt *string
	strPtr := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		formatted := t.UTC().Format(time.RFC3339)
		return &formatted
	}
	lastCycleAt = strPtr(state.LastCycleAt)
	nextRunAt = strPtr(state.NextRunAt)
	bootstrapAt = strPtr(state.BootstrapAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"service":     "gacha-calendar",
		"version":     h.d.Version,
		"server_time": time.Now().UTC().Format(time.RFC3339),
		"timezone": map[string]any{
			"business":   "Asia/Shanghai",
			"utc_offset": "+08:00",
			"note":       "预测口径为国服时间（Asia/Shanghai, UTC+8, 无夏令时）；所有时间字段均为 UTC RFC3339，请按本地时区转换",
		},
		"refresh": map[string]any{
			"interval_seconds": int(h.d.RefreshInterval.Seconds()),
			"running":          running,
			"last_cycle_at":    lastCycleAt,
			"last_cycle_ok":    state.LastCycleOK,
			"last_cycle_all":   state.LastCycleAll,
			"last_error":       state.LastError,
			"next_run_at":      nextRunAt,
			"scheduler":        "adaptive: 事件窗口内 15–30 分钟（发布黄金时段加密），平时按基础间隔",
		},
		"bootstrap": map[string]any{
			"state":       state.BootstrapState,
			"running":     bootRunning,
			"finished_at": bootstrapAt,
		},
	})
}

// --- admin: refresh + overrides ---

func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Games []string `json:"games"`
	}
	if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
				return
			}
		}
	}
	var ids []games.GameID
	for _, raw := range body.Games {
		id := games.GameID(raw)
		if _, ok := games.Get(id); ok {
			ids = append(ids, id)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_game", "未知游戏："+raw)
			return
		}
	}
	started := h.d.Collector.RequestRefresh(ids)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"hint":    "刷新在后台串行执行，可通过 /api/meta 或 /api/games 观察状态",
	})
}

// bootstrap re-runs the guided history backfill (first initialization or a
// forced recalibration). It is serial with regular cycles on purpose.
func (h *handlers) bootstrap(w http.ResponseWriter, r *http.Request) {
	started := h.d.Collector.RequestBootstrap()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"hint":    "回填在后台串行执行（约数分钟），可通过 /api/meta 的 bootstrap 字段观察进度",
	})
}

func (h *handlers) putOverride(w http.ResponseWriter, r *http.Request) {
	gameID := games.GameID(r.PathValue("game"))
	if _, ok := games.Get(gameID); !ok {
		writeError(w, http.StatusBadRequest, "invalid_game", "未知游戏："+string(gameID))
		return
	}
	var body struct {
		Version      *string `json:"version"`
		UpdateStart  *string `json:"update_start"`
		UpdateEnd    *string `json:"update_end"`
		PreviewStart *string `json:"preview_start"`
		PreviewEnd   *string `json:"preview_end"`
		Note         *string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求体不是合法 JSON")
		return
	}
	read := func(value *string, name string) (*time.Time, string) {
		if value == nil || *value == "" {
			return nil, ""
		}
		parsed, err := time.Parse(time.RFC3339, *value)
		if err != nil {
			return nil, name + " 必须是带时区的 ISO 8601"
		}
		utc := parsed.UTC()
		return &utc, ""
	}
	updateStart, e1 := read(body.UpdateStart, "update_start")
	updateEnd, e2 := read(body.UpdateEnd, "update_end")
	previewStart, e3 := read(body.PreviewStart, "preview_start")
	previewEnd, e4 := read(body.PreviewEnd, "preview_end")
	for _, message := range []string{e1, e2, e3, e4} {
		if message != "" {
			writeError(w, http.StatusBadRequest, "invalid_time", message)
			return
		}
	}
	if updateStart == nil && previewStart == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "至少给出 update_start 或 preview_start")
		return
	}
	later := func(a, b *time.Time) bool { return a != nil && b != nil && !a.After(*b) }
	if later(updateEnd, updateStart) {
		writeError(w, http.StatusBadRequest, "invalid_input", "update_end 必须晚于 update_start")
		return
	}
	if later(previewEnd, previewStart) {
		writeError(w, http.StatusBadRequest, "invalid_input", "preview_end 必须晚于 preview_start")
		return
	}
	if previewStart != nil && updateStart != nil && !previewStart.Before(*updateStart) {
		writeError(w, http.StatusBadRequest, "invalid_input", "前瞻直播必须早于版本更新")
		return
	}

	row := model.Override{GameID: string(gameID), AppliedAt: time.Now().UTC()}
	if body.Version != nil {
		row.Version = strings.TrimSpace(*body.Version)
	}
	if body.Note != nil {
		row.Note = strings.TrimSpace(*body.Note)
	}
	row.UpdateStart, row.UpdateEnd = updateStart, updateEnd
	row.PreviewStart, row.PreviewEnd = previewStart, previewEnd

	var existing model.Override
	err := h.d.DB.First(&existing, "game_id = ?", string(gameID)).Error
	if err == nil {
		row.ID = existing.ID
		row.CreatedAt = existing.CreatedAt
	}
	if err := h.d.DB.Save(&row).Error; err != nil {
		h.dbError(w, r, "db_error", err)
		return
	}
	// Recompute synchronously so the correction is visible immediately,
	// then let the background refresh re-derive from fresh sources.
	if err := h.d.Collector.RecomputeGame(gameID, true); err != nil {
		h.dbError(w, r, "recompute_failed", err)
		return
	}
	h.d.Collector.RequestRefresh([]games.GameID{gameID})
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"game": string(gameID), "applied_at": row.AppliedAt.Format(time.RFC3339),
	}})
}

func (h *handlers) deleteOverride(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("game")
	result := h.d.DB.Delete(&model.Override{}, "game_id = ?", gameID)
	if result.Error != nil {
		h.dbError(w, r, "db_error", result.Error)
		return
	}
	if err := h.d.Collector.RecomputeGame(games.GameID(gameID), false); err != nil {
		h.dbError(w, r, "recompute_failed", err)
		return
	}
	h.d.Collector.RequestRefresh([]games.GameID{games.GameID(gameID)})
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"game": gameID, "cleared": true}})
}
