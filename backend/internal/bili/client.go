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
//   - an optional logged-in session (QR login, see login.go) is prepended to
//     the anonymous cookie; it lifts -352 on the dynamics feed and the login
//     wall on personal accounts' dynamics. A rejected session (-101) falls
//     back to anonymous for the rest of the process lifetime
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

// Hosts and headers are vars so tests can stub the bilibili side.
var (
	apiBase        = "https://api.bilibili.com"
	webBase        = "https://www.bilibili.com"
	defaultReferer = "https://www.bilibili.com/"
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// minRequestGap is the floor every request through the serial chain keeps
// from its predecessor (a var only so tests can shrink it).
var minRequestGap = 1200 * time.Millisecond

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
	session       string // logged-in cookie from QR login, "" when anonymous
}

// SetSessionCookie installs a logged-in session cookie (see login.go).
func (c *Client) SetSessionCookie(cookie string) {
	c.mu.Lock()
	c.session = cookie
	c.mu.Unlock()
}

// ClearSessionCookie drops the in-process session (logout or -101 rejection).
func (c *Client) ClearSessionCookie() {
	c.mu.Lock()
	c.session = ""
	c.mu.Unlock()
}

// HasSessionCookie reports whether requests currently carry a login.
func (c *Client) HasSessionCookie() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session != ""
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

// cookieMode picks how fetch() resolves the Cookie header per attempt.
type cookieMode int

const (
	cookieAuto  cookieMode = iota // login session (if any) + anonymous buvid
	cookieNone                    // passport endpoints and the live domain
	cookieFixed                   // exactly the caller-supplied cookie (nav)
)

// fetch performs one logical GET with retries, respecting the serial gap.
func (c *Client) fetch(ctx context.Context, path string, mode cookieMode, fixed string, gap time.Duration, retries int, referer string) (json.RawMessage, error) {
	full := path
	if !strings.HasPrefix(path, "http") {
		full = apiBase + path
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		cookie := ""
		switch mode {
		case cookieAuto:
			cookie = c.ensureCookie(ctx)
		case cookieFixed:
			cookie = fixed
		}
		data, err := c.rawJSON(ctx, full, cookie, referer, gap)
		if err == nil {
			return data, nil
		}
		lastErr = err
		// -101 means the endpoint wanted a login and ours (if any) was
		// rejected: drop the session and retry this attempt anonymously.
		var rejected *Error
		if mode == cookieAuto && errors.As(err, &rejected) && rejected.Code == -101 && c.HasSessionCookie() {
			c.ClearSessionCookie()
			c.log.Warn("bilibili session rejected (-101), continuing anonymously", "path", cleanPath(path))
			attempt--
			continue
		}
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

// get is the normal mode: session + anonymous buvid.
func (c *Client) get(ctx context.Context, path string, gap time.Duration, retries int, referer string) (json.RawMessage, error) {
	return c.fetch(ctx, path, cookieAuto, "", gap, retries, referer)
}

// getAnon sends no cookie at all.
func (c *Client) getAnon(ctx context.Context, path string, gap time.Duration, retries int, referer string) (json.RawMessage, error) {
	return c.fetch(ctx, path, cookieNone, "", gap, retries, referer)
}

// getWith sends exactly the given cookie.
func (c *Client) getWith(ctx context.Context, path, cookie string, gap time.Duration, retries int, referer string) (json.RawMessage, error) {
	return c.fetch(ctx, path, cookieFixed, cookie, gap, retries, referer)
}

func cleanPath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// rawJSON runs one HTTP request through the serial chain.
func (c *Client) rawJSON(ctx context.Context, full, cookie, referer string, gap time.Duration) (json.RawMessage, error) {
	if !c.reserveSlot(ctx, gap) {
		return nil, ctx.Err()
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

// reserveSlot waits out the serial chain's minimum distance from the previous
// request and takes the slot; false means ctx ended first.
func (c *Client) reserveSlot(ctx context.Context, gap time.Duration) bool {
	if gap < minRequestGap {
		gap = minRequestGap
	}
	c.mu.Lock()
	wait := time.Until(c.lastRequest.Add(gap))
	c.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
		}
	}
	c.mu.Lock()
	c.lastRequest = time.Now()
	c.mu.Unlock()
	return true
}

// postFormJSON sends one form POST through the serial chain and hands back
// the envelope data plus the response cookies. No retries and no -101
// fallback on purpose: the passport rotation endpoints consume one-time
// tokens, so a failed attempt must surface instead of silently repeating.
func (c *Client) postFormJSON(ctx context.Context, full, cookie, referer string, form url.Values) (json.RawMessage, []*http.Cookie, error) {
	if !c.reserveSlot(ctx, minRequestGap) {
		return nil, nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("Origin", webBase)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, &Error{Code: 0, Message: err.Error(), URL: cleanPath(full)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, &Error{Code: 0, Message: err.Error(), URL: cleanPath(full)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, &Error{Code: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), URL: cleanPath(full)}
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, &Error{Code: 0, Message: "B 站返回了非 JSON 内容", URL: cleanPath(full)}
	}
	if envelope.Code != 0 {
		return nil, nil, &Error{Code: envelope.Code, Message: envelope.Message, URL: cleanPath(full)}
	}
	return envelope.Data, resp.Cookies(), nil
}

// fetchPage downloads one HTML page through the serial chain; the correspond
// page answers HTML, not a JSON envelope.
func (c *Client) fetchPage(ctx context.Context, full, cookie string) ([]byte, error) {
	if !c.reserveSlot(ctx, minRequestGap) {
		return nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", defaultReferer)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Code: 0, Message: err.Error(), URL: cleanPath(full)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Code: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), URL: cleanPath(full)}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// ensureCookie builds the request cookie: the anonymous buvid (fetched once,
// best-effort) with a logged-in session prepended when one is installed. It
// never returns an empty string while a session exists — a failed buvid
// bootstrap must not drop the login.
func (c *Client) ensureCookie(ctx context.Context) string {
	c.mu.Lock()
	if c.cookieFetched {
		base := c.cookie
		session := c.session
		c.mu.Unlock()
		return mergeCookie(session, base)
	}
	c.mu.Unlock()

	data, err := c.getAnon(ctx, "/x/frontend/finger/spi", 0, 1, defaultReferer)
	if err != nil {
		c.log.Warn("anonymous buvid cookie unavailable", "error", err.Error())
	} else {
		var spi struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		}
		if err := json.Unmarshal(data, &spi); err != nil || spi.B3 == "" {
			c.log.Warn("spi response unusable")
		} else {
			cookie := "buvid3=" + spi.B3
			if spi.B4 != "" {
				cookie += "; buvid4=" + spi.B4
			}
			c.mu.Lock()
			c.cookie = cookie
			c.cookieFetched = true
			c.mu.Unlock()
		}
	}

	c.mu.Lock()
	base := c.cookie
	session := c.session
	c.mu.Unlock()
	return mergeCookie(session, base)
}

// mergeCookie joins the session and anonymous cookie parts, skipping empties.
func mergeCookie(session, base string) string {
	switch {
	case session == "":
		return base
	case base == "":
		return session
	default:
		return session + "; " + base
	}
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
		"https://space.bilibili.com/"+url.QueryEscape(mid)+"/article")
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
		2800*time.Millisecond, 4, ArticleURL(id))
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
		"https://space.bilibili.com/"+url.QueryEscape(mid)+"/upload/video")
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
		1800*time.Millisecond, 1, "https://search.bilibili.com/")
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
	HasMore bool   `json:"has_more"`
	Offset  string `json:"offset"`
}

// dynamicsPageWindow is how far back the feed paging walks: preview teasers
// land at most ~3 weeks ahead of a stream, so older screens carry nothing the
// planner needs.
const dynamicsPageWindow = 21 * 24 * time.Hour

// dynamicsMaxPages bounds the cursor walk for accounts that post many
// dynamics a day (the official Genshin account pushes a week-old teaser
// beyond the sixth screen).
const dynamicsMaxPages = 10

// Dynamics lists recent official dynamics, walking the feed cursor a few
// screens deep. Optional because some accounts answer -352 — the caller
// treats an error as "no dynamics"; later-page failures keep the screens
// already fetched.
func (c *Client) Dynamics(ctx context.Context, mid string) ([]Dynamic, error) {
	var out []Dynamic
	offset := ""
	for page := 0; page < dynamicsMaxPages; page++ {
		data, err := c.get(ctx,
			"/x/polymer/web-dynamic/v1/feed/space?host_mid="+url.QueryEscape(mid)+
				"&offset="+url.QueryEscape(offset)+"&timezone_offset=-480",
			2500*time.Millisecond, 1,
			"https://space.bilibili.com/"+url.QueryEscape(mid)+"/dynamic")
		if err != nil {
			if page == 0 {
				return nil, err
			}
			break
		}
		var payload dynamicFeed
		if err := json.Unmarshal(data, &payload); err != nil {
			if page == 0 {
				return nil, err
			}
			break
		}
		out = append(out, parseDynamics(payload)...)
		done := !payload.HasMore || payload.Offset == "" || len(payload.Items) == 0
		if !done {
			var oldest int64
			for i := range payload.Items {
				ts := int64(payload.Items[i].Modules.Author.PubTS)
				if ts > 0 && (oldest == 0 || ts < oldest) {
					oldest = ts
				}
			}
			if oldest > 0 && time.Now().Unix()-oldest > int64(dynamicsPageWindow/time.Second) {
				done = true
			}
		}
		if done {
			break
		}
		offset = payload.Offset
	}
	return out, nil
}

func parseDynamics(payload dynamicFeed) []Dynamic {
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
	return out
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
	LiveStatus int    // 0 idle, 1 streaming
	Cover      string // room banner (user_cover): the preview key visual
}

// LiveRoomInfo reads the public room card from the live domain. This domain
// has its own, weaker risk control than api.bilibili.com and keeps answering
// from datacenter IPs while the dynamics feed is blocked.
func (c *Client) LiveRoomInfo(ctx context.Context, roomID string) (LiveRoom, error) {
	data, err := c.getAnon(ctx,
		"https://api.live.bilibili.com/room/v1/Room/get_info?room_id="+url.QueryEscape(roomID),
		1500*time.Millisecond, 2,
		"https://live.bilibili.com/"+url.QueryEscape(roomID))
	if err != nil {
		return LiveRoom{}, err
	}
	var payload struct {
		RoomID     int    `json:"room_id"`
		Title      string `json:"title"`
		LiveStatus int    `json:"live_status"`
		UserCover  string `json:"user_cover"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return LiveRoom{}, err
	}
	return LiveRoom{
		RoomID:     strconv.Itoa(payload.RoomID),
		Title:      StripHTML(payload.Title),
		LiveStatus: payload.LiveStatus,
		Cover:      payload.UserCover,
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
