// Package bili is a minimal read-only Bilibili client, ported from the
// plugin's bilibili.js with the same verified preconditions:
//
//   - official column lists (x/space/article) work without login
//   - full columns (x/article/view) are rate limited: serialize with a
//     >= 2.8 s gap and retry with exponential backoff
//   - search and the dynamic feed need an anonymous buvid3 cookie from the
//     SPI endpoint, refreshable when it goes stale
//   - x/space/wbi/arc/search is not used (-352 even with a cookie; WBI
//     signing is not worth the fragility)
//
// Every failure surfaces as *Error; callers keep the previous state instead
// of writing guessed values.
package bili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBase        = "https://api.bilibili.com"
	defaultReferer = "https://www.bilibili.com/"
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// transientCodes mean "slow down / refresh the cookie", never "no data".
var transientCodes = map[int]bool{-352: true, -412: true, -500: true, -503: true, -509: true, -799: true}

// Error is a Bilibili API failure. Code 0 with a message means a transport
// or encoding problem.
type Error struct {
	Code    int
	Message string
	URL     string
}

func (e *Error) Error() string {
	return fmt.Sprintf("bilibili: %s (code %d, %s)", e.Message, e.Code, e.URL)
}

func transient(err error) bool {
	var be *Error
	if errors.As(err, &be) {
		return transientCodes[be.Code]
	}
	return true
}

var backoff = []time.Duration{5 * time.Second, 12 * time.Second, 25 * time.Second}

// Client serializes every API request behind one chain — Bilibili limits per
// source IP, not per endpoint. `gap` per call states the minimum distance
// from the previous request.
type Client struct {
	http *http.Client
	log  *slog.Logger

	mu            sync.Mutex
	lastRequest   time.Time
	cookie        string
	cookieFetched bool
}

func New(logger *slog.Logger) *Client {
	return &Client{
		http: &http.Client{Timeout: 20 * time.Second},
		log:  logger,
	}
}

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// get performs one logical GET with retries, respecting the serial gap.
// useCookie=false serves the cookie bootstrap request itself.
func (c *Client) get(ctx context.Context, path string, gap time.Duration, retries int, referer string, useCookie bool) (json.RawMessage, error) {
	full := path
	if !strings.HasPrefix(path, "http") {
		full = apiBase + path
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		cookie := ""
		if useCookie {
			if fetched, err := c.ensureCookie(ctx); err == nil {
				cookie = fetched
			}
		}
		data, err := c.rawJSON(ctx, full, cookie, referer, gap)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !transient(err) || attempt >= retries {
			_ = lastErr
			return nil, err
		}
		c.log.Warn("bilibili retry", "attempt", attempt+1, "path", cleanPath(path), "error", err.Error())
		var be *Error
		if errors.As(err, &be) && (be.Code == -352 || be.Code == 412) {
			c.mu.Lock()
			c.cookieFetched = false
			c.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff[min(attempt, len(backoff)-1)]):
		}
	}
}

func cleanPath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// rawJSON runs one HTTP request through the serial chain.
func (c *Client) rawJSON(ctx context.Context, full, cookie, referer string, gap time.Duration) (json.RawMessage, error) {
	if gap < 1200*time.Millisecond {
		gap = 1200 * time.Millisecond
	}
	c.mu.Lock()
	wait := time.Until(c.lastRequest.Add(gap))
	c.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	// The `x/space/*` and `x/series/*` endpoints answer HTTP 412 when called
	// as a bare document request; a space origin makes them accept the same
	// anonymous cookie the browser sends.
	req.Header.Set("Origin", "https://space.bilibili.com")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	c.mu.Lock()
	c.lastRequest = time.Now()
	c.mu.Unlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Code: 0, Message: err.Error(), URL: full}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, &Error{Code: 412, Message: "请求被 B 站风控拦截（HTTP 412）", URL: full}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Code: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), URL: full}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &Error{Code: 0, Message: err.Error(), URL: full}
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &Error{Code: 0, Message: "B 站返回了非 JSON 内容", URL: full}
	}
	if envelope.Code != 0 {
		return nil, &Error{Code: envelope.Code, Message: envelope.Message, URL: full}
	}
	return envelope.Data, nil
}

// ensureCookie fetches the anonymous buvid cookie once, best-effort.
func (c *Client) ensureCookie(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.cookieFetched {
		cookie := c.cookie
		c.mu.Unlock()
		return cookie, nil
	}
	c.mu.Unlock()

	data, err := c.get(ctx, "/x/frontend/finger/spi", 0, 1, defaultReferer, false)
	if err != nil {
		c.log.Warn("anonymous buvid cookie unavailable", "error", err.Error())
		return "", err
	}
	var spi struct {
		B3 string `json:"b_3"`
		B4 string `json:"b_4"`
	}
	if err := json.Unmarshal(data, &spi); err != nil || spi.B3 == "" {
		return "", fmt.Errorf("spi response unusable")
	}
	cookie := "buvid3=" + spi.B3
	if spi.B4 != "" {
		cookie += "; buvid4=" + spi.B4
	}
	c.mu.Lock()
	c.cookie = cookie
	c.cookieFetched = true
	c.mu.Unlock()
	return cookie, nil
}

// FlexString unmarshals a JSON value that may arrive as either a string or
// a number (Bilibili returns article ids as numbers).
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		*f = ""
		return nil
	}
	if len(text) >= 2 && text[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	*f = FlexString(text)
	return nil
}

// Article is one row of the official column list.
type Article struct {
	ID          FlexString `json:"id"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	PublishTime int64      `json:"publish_time"`
	ImageURLs   []string   `json:"image_urls"`
}

// IDString returns the article id as a plain string.
func (a Article) IDString() string { return string(a.ID) }

// FlexInt64 unmarshals a JSON value that may arrive as either a number or a
// numeric string (the dynamics feed sends pub_ts as string on some accounts).
type FlexInt64 int64

func (f *FlexInt64) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == `""` {
		*f = 0
		return nil
	}
	if len(text) >= 2 && text[0] == '"' {
		text = strings.Trim(text, `"`)
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return err
	}
	*f = FlexInt64(value)
	return nil
}

func (c *Client) ArticleList(ctx context.Context, mid string) ([]Article, error) {
	return c.ArticleListPage(ctx, mid, 1)
}

// ArticleListPage reads one page of the official column list (newest first).
func (c *Client) ArticleListPage(ctx context.Context, mid string, page int) ([]Article, error) {
	data, err := c.get(ctx,
		"/x/space/article?mid="+url.QueryEscape(mid)+
			"&pn="+strconv.Itoa(page)+"&ps=30&sort=publish_time",
		1200*time.Millisecond, 2,
		"https://space.bilibili.com/"+url.QueryEscape(mid)+"/article", true)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Articles []Article `json:"articles"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Articles, nil
}

// ArticleView is a full column.
type ArticleView struct {
	ID          uint64
	Title       string
	Content     string // HTML, stripped by the caller
	PublishTime int64
	Author      string
	ImageURLs   []string
}

func (c *Client) ArticleView(ctx context.Context, id string) (ArticleView, error) {
	data, err := c.get(ctx, "/x/article/view?id="+url.QueryEscape(id),
		2800*time.Millisecond, 4, ArticleURL(id), true)
	if err != nil {
		return ArticleView{}, err
	}
	var payload struct {
		ID          uint64   `json:"id"`
		Title       string   `json:"title"`
		Content     string   `json:"content"`
		PublishTime int64    `json:"publish_time"`
		Author      string   `json:"author_name"`
		ImageURLs   []string `json:"image_urls"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ArticleView{}, err
	}
	return ArticleView{
		ID: payload.ID, Title: payload.Title, Content: payload.Content,
		PublishTime: payload.PublishTime, Author: payload.Author,
		ImageURLs: payload.ImageURLs,
	}, nil
}

// Archive is a video row from either the series endpoint or search.
// AuthorID is the uploader's mid (search rows use `mid`, series rows `upMid`).
type Archive struct {
	BVID     string
	Title    string
	Pubdate  int64
	AuthorID string
	Pic      string
}

// upMid appears as `upMid` (camel case) in series rows; decode via alias.
type seriesArchive struct {
	BVID    string     `json:"bvid"`
	Title   string     `json:"title"`
	Pubdate int64      `json:"pubdate"`
	UpMid   FlexString `json:"upMid"`
	Pic     string     `json:"pic"`
}

// FeatureVideos lists official collection uploads matching a keyword.
func (c *Client) FeatureVideos(ctx context.Context, mid, keyword string) ([]Archive, error) {
	data, err := c.get(ctx,
		"/x/series/recArchivesByKeywords?mid="+url.QueryEscape(mid)+
			"&keywords="+url.QueryEscape(keyword)+"&ps=20",
		2500*time.Millisecond, 3,
		"https://space.bilibili.com/"+url.QueryEscape(mid)+"/upload/video", true)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Archives []seriesArchive `json:"archives"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	out := make([]Archive, 0, len(payload.Archives))
	for _, row := range payload.Archives {
		out = append(out, Archive{
			BVID: row.BVID, Title: StripHTML(row.Title), Pubdate: row.Pubdate,
			AuthorID: string(row.UpMid), Pic: row.Pic,
		})
	}
	return out, nil
}

type searchArchive struct {
	BVID    string `json:"bvid"`
	Title   string `json:"title"`
	Pubdate int64  `json:"pubdate"`
	Mid     int64  `json:"mid"`
	Pic     string `json:"pic"`
	Author  string `json:"author"`
}

// SearchVideos searches public video uploads; filter by mid at the caller.
func (c *Client) SearchVideos(ctx context.Context, keyword string) ([]Archive, error) {
	data, err := c.get(ctx,
		"/x/web-interface/search/type?search_type=video&order=pubdate&page=1&page_size=50&keyword="+
			url.QueryEscape(keyword),
		1800*time.Millisecond, 1, "https://search.bilibili.com/", true)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Result []searchArchive `json:"result"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	out := make([]Archive, 0, len(payload.Result))
	for _, row := range payload.Result {
		out = append(out, Archive{
			BVID: row.BVID, Title: StripHTML(row.Title), Pubdate: row.Pubdate,
			AuthorID: strconv.FormatInt(row.Mid, 10), Pic: row.Pic,
		})
	}
	return out, nil
}

// Dynamic is one official dynamic post.
type Dynamic struct {
	ID       string
	Text     string
	Pubdate  int64
	Pictures []string
}

type dynamicFeed struct {
	Items []struct {
		ID      string `json:"id_str"`
		IDAlt   uint64 `json:"id"`
		Modules struct {
			Author struct {
				PubTS FlexInt64 `json:"pub_ts"`
			} `json:"module_author"`
			Dynamic struct {
				Desc struct {
					Text string `json:"text"`
				} `json:"desc"`
				Major struct {
					Opus struct {
						Title   string `json:"title"`
						Summary struct {
							Text string `json:"text"`
						} `json:"summary"`
						Pics []struct {
							URL string `json:"url"`
						} `json:"pics"`
					} `json:"opus"`
					Archive struct {
						Title string `json:"title"`
						Desc  string `json:"desc"`
						Cover string `json:"cover"`
					} `json:"archive"`
					Draw struct {
						Items []struct {
							Src string `json:"src"`
						} `json:"items"`
					} `json:"draw"`
				} `json:"major"`
			} `json:"module_dynamic"`
		} `json:"modules"`
	} `json:"items"`
}

// Dynamics lists recent official dynamics; optional because some accounts
// answer -352 — the caller treats an error as "no dynamics".
func (c *Client) Dynamics(ctx context.Context, mid string) ([]Dynamic, error) {
	data, err := c.get(ctx,
		"/x/polymer/web-dynamic/v1/feed/space?host_mid="+url.QueryEscape(mid)+
			"&offset=&timezone_offset=-480",
		2500*time.Millisecond, 1,
		"https://space.bilibili.com/"+url.QueryEscape(mid)+"/dynamic", true)
	if err != nil {
		return nil, err
	}
	var payload dynamicFeed
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	out := make([]Dynamic, 0, len(payload.Items))
	for _, item := range payload.Items {
		dyn := Dynamic{ID: item.ID, Pubdate: int64(item.Modules.Author.PubTS)}
		if dyn.ID == "" {
			dyn.ID = strconv.FormatUint(item.IDAlt, 10)
		}
		d := item.Modules.Dynamic
		switch {
		case d.Desc.Text != "":
			dyn.Text = strings.TrimSpace(d.Desc.Text)
		case d.Major.Opus.Summary.Text != "":
			dyn.Text = strings.TrimSpace(d.Major.Opus.Summary.Text)
		case d.Major.Opus.Title != "":
			dyn.Text = strings.TrimSpace(d.Major.Opus.Title)
		case d.Major.Archive.Title != "":
			dyn.Text = strings.TrimSpace(d.Major.Archive.Title)
		case d.Major.Archive.Desc != "":
			dyn.Text = strings.TrimSpace(d.Major.Archive.Desc)
		}
		for _, pic := range d.Major.Opus.Pics {
			if pic.URL != "" {
				dyn.Pictures = append(dyn.Pictures, pic.URL)
			}
		}
		for _, item := range d.Major.Draw.Items {
			if item.Src != "" {
				dyn.Pictures = append(dyn.Pictures, item.Src)
			}
		}
		if d.Major.Archive.Cover != "" {
			dyn.Pictures = append(dyn.Pictures, d.Major.Archive.Cover)
		}
		out = append(out, dyn)
	}
	return out, nil
}

// Download fetches a binary asset (cover image) with a bilibili referer.
// It bypasses the API serial chain: the image CDN has its own limits.
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", defaultReferer)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 12<<20))
}

// LiveRoom is the slice of room/v1/Room/get_info the collector needs.
type LiveRoom struct {
	RoomID     string
	Title      string
	LiveStatus int // 0 idle, 1 streaming
}

// LiveRoomInfo reads the public room card from the live domain. This domain
// has its own, weaker risk control than api.bilibili.com and keeps answering
// from datacenter IPs while the dynamics feed is blocked.
func (c *Client) LiveRoomInfo(ctx context.Context, roomID string) (LiveRoom, error) {
	data, err := c.get(ctx,
		"https://api.live.bilibili.com/room/v1/Room/get_info?room_id="+url.QueryEscape(roomID),
		1500*time.Millisecond, 2,
		"https://live.bilibili.com/"+url.QueryEscape(roomID), false)
	if err != nil {
		return LiveRoom{}, err
	}
	var payload struct {
		RoomID     int    `json:"room_id"`
		Title      string `json:"title"`
		LiveStatus int    `json:"live_status"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return LiveRoom{}, err
	}
	return LiveRoom{
		RoomID:     strconv.Itoa(payload.RoomID),
		Title:      StripHTML(payload.Title),
		LiveStatus: payload.LiveStatus,
	}, nil
}

// liveAnnouncementPair matches the sys_msg embedded in a live room's HTML:
// "uid":…,"ctime":"2026-10-21 12:00:30","content":"《原神》7.2版本前瞻特别
// 节目将于10月23日晚20:00开启！"
var liveAnnouncementPair = regexp.MustCompile(
	`"ctime":"(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})","content":"((?:[^"\\]|\\.)+)"`)

func decodeJSONString(raw string) string {
	var out string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &out); err != nil {
		return raw
	}
	return out
}

// LiveRoomAnnouncement fetches the room's web page and extracts the newest
// official announcement text with its publication time. The page embeds the
// reservation message ("将于10月23日晚20:00开启") as plain HTML-escaped JSON,
// reachable without any API cookie — the teaser source that survives
// dynamics-feed risk control.
func (c *Client) LiveRoomAnnouncement(ctx context.Context, roomID string) (text string, published time.Time, err error) {
	pageURL := "https://live.bilibili.com/" + roomID
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if reqErr != nil {
		return "", time.Time{}, reqErr
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://live.bilibili.com/")
	resp, doErr := c.http.Do(req)
	if doErr != nil {
		return "", time.Time{}, doErr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("live room %s: HTTP %d", roomID, resp.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return "", time.Time{}, readErr
	}

	var bestText string
	var bestAt time.Time
	for _, m := range liveAnnouncementPair.FindAllStringSubmatch(string(body), -1) {
		content := strings.TrimSpace(decodeJSONString(m[2]))
		if !strings.ContainsAny(content, "0123456789") {
			continue
		}
		at, parseErr := time.ParseInLocation("2006-01-02 15:04:05", m[1], liveCST)
		if parseErr != nil {
			at = time.Time{}
		}
		if bestText == "" || at.After(bestAt) {
			bestText, bestAt = content, at
		}
	}
	if bestText == "" {
		return "", time.Time{}, nil
	}
	return bestText, bestAt.In(liveCST), nil
}

// liveCST is the fixed national-server zone used by the live-room page times.
var liveCST = time.FixedZone("UTC+8", 8*3600)

// ArticleURL maps a column/opus id to its public page.
func ArticleURL(id string) string {
	if strings.HasPrefix(strings.ToLower(id), "cv") {
		return "https://www.bilibili.com/read/" + id
	}
	// The list endpoint returns the numeric column id; opus ids are 18–19
	// digit snowflakes.
	if len(id) <= 12 && isDigits(id) {
		return "https://www.bilibili.com/read/cv" + id
	}
	return "https://www.bilibili.com/opus/" + id
}

func VideoURL(bvid string) string { return "https://www.bilibili.com/video/" + bvid }

func DynamicURL(id string) string { return "https://t.bilibili.com/" + id }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var (
	brTag      = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockTag   = regexp.MustCompile(`(?i)</(?:p|div|li|h\d)>`)
	anyTag     = regexp.MustCompile(`<[^>]+>`)
	spaceTabs  = regexp.MustCompile(`[ \t]+\n`)
	newlines   = regexp.MustCompile(`\n{3,}`)
	wideSpaces = regexp.MustCompile(`[ \t]{2,}`)
)

// StripHTML converts column HTML to readable text, keeping paragraph breaks.
func StripHTML(input string) string {
	if input == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'",
	)
	out := brTag.ReplaceAllString(input, "\n")
	out = blockTag.ReplaceAllString(out, "\n")
	out = anyTag.ReplaceAllString(out, " ")
	out = replacer.Replace(out)
	out = strings.ReplaceAll(out, "\r", "")
	out = spaceTabs.ReplaceAllString(out, "\n")
	out = newlines.ReplaceAllString(out, "\n\n")
	out = wideSpaces.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}
