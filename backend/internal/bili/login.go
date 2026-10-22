package bili

// QR-code login against passport.bilibili.com. The web QR flow needs no
// captcha: generate hands out a one-time qrcode_key, the phone app confirms
// the scan against an already-trusted session, and the poll holder receives
// the cookies. For this read-only collector the point is a logged-in
// SESSDATA, which lifts the -352 risk control on the dynamics feed and the
// "login wall" some personal accounts put on their dynamics page.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// passportBase is a var so tests can point the login flow at a stub server.
var passportBase = "https://passport.bilibili.com"

const (
	// qrLifetime mirrors the web client: a qrcode_key works for ~180 s,
	// afterwards poll answers 86038 and a fresh QR must be shown.
	qrLifetime = 180 * time.Second
)

// QRCode is one login QR round.
type QRCode struct {
	Key string
	URL string // the payload to encode into the QR image
}

// QRGenerate starts a login round. Passport answers anonymously; sending a
// stale session cookie here would be wrong.
func (c *Client) QRGenerate(ctx context.Context) (*QRCode, error) {
	data, err := c.getAnon(ctx,
		passportBase+"/x/passport-login/web/qrcode/generate",
		0, 1, defaultReferer)
	if err != nil {
		return nil, err
	}
	var payload struct {
		URL         string `json:"url"`
		QRCodeKey   string `json:"qrcode_key"`
		ExpireInSec int    `json:"expire_seconds"`
	}
	if err := unmarshalStrict(data, &payload); err != nil {
		return nil, err
	}
	if payload.QRCodeKey == "" || payload.URL == "" {
		return nil, fmt.Errorf("passport returned an unusable QR payload")
	}
	return &QRCode{Key: payload.QRCodeKey, URL: payload.URL}, nil
}

// QR state values reported by the poll endpoint's nested data.code.
const (
	qrWaiting = 86101 // not scanned yet
	qrScanned = 86090 // scanned, waiting for confirmation on the phone
	qrExpired = 86038 // key expired, generate a new QR
)

// QRState is one poll result.
type QRState struct {
	Status       string // waiting | scanned | expired | success
	Cookie       string // "SESSDATA=…; bili_jct=…; …" on success
	RefreshToken string
	ExpiresAt    time.Time // SESSDATA expiry as advertised at login (may be zero)
}

// QRPoll asks passport whether the QR was scanned. The outer envelope is
// always code 0; the QR status lives in data.code, so this never returns an
// error for "not scanned yet".
func (c *Client) QRPoll(ctx context.Context, key string) (*QRState, error) {
	data, err := c.getAnon(ctx,
		passportBase+"/x/passport-login/web/qrcode/poll?qrcode_key="+url.QueryEscape(key),
		0, 0, defaultReferer)
	if err != nil {
		return nil, err
	}
	var payload struct {
		URL          string `json:"url"`
		RefreshToken string `json:"refresh_token"`
		Code         int    `json:"code"`
	}
	if err := unmarshalStrict(data, &payload); err != nil {
		return nil, err
	}
	switch payload.Code {
	case qrWaiting:
		return &QRState{Status: "waiting"}, nil
	case qrScanned:
		return &QRState{Status: "scanned"}, nil
	case qrExpired:
		return &QRState{Status: "expired"}, nil
	case 0:
		cookie, expiresAt, err := c.loginCookie(ctx, payload.URL)
		if err != nil {
			return nil, err
		}
		return &QRState{
			Status: "success", Cookie: cookie,
			RefreshToken: payload.RefreshToken, ExpiresAt: expiresAt,
		}, nil
	default:
		return nil, &Error{Code: payload.Code, Message: "扫码状态未知", URL: "passport-login/web/qrcode/poll"}
	}
}

// loginCookie extracts the credential cookie from the success url, handling
// both response generations:
//
//   - pre-2026-08: …/crossDomain?SESSDATA=…&bili_jct=… (cookies in the query)
//   - since 2026-08: …/x/passport-login/web/crossDomain?ticket=… — the ticket
//     link must be fetched with a browser UA; the cookies arrive as Set-Cookie
//     response headers on the (unfollowed) reply.
//
// It must run inside the successful poll call: the ticket and the qrcode_key
// are single-use.
func (c *Client) loginCookie(ctx context.Context, rawURL string) (string, time.Time, error) {
	if cookie, expiresAt, ok := cookieFromQuery(rawURL); ok {
		return cookie, expiresAt, nil
	}
	return c.cookieFromTicket(ctx, rawURL)
}

// cookieFromTicket exchanges the single-use crossDomain ticket for cookies.
func (c *Client) cookieFromTicket(ctx context.Context, ticketURL string) (string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ticketURL, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("cross-domain ticket url: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", passportBase+"/")
	// The ticket reply is a redirect/HTML page that carries the cookies on
	// itself; following it would land on a cookie-less page.
	noRedirect := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fetch cross-domain ticket: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	cookie, expiresAt := credentialCookie(resp.Cookies())
	if cookie == "" {
		return "", time.Time{}, fmt.Errorf("cross-domain ticket at %s carried no login cookies", cleanTicketURL(ticketURL))
	}
	c.log.Info("bilibili login cookies exchanged via crossDomain ticket")
	return cookie, expiresAt, nil
}

// credentialCookie flattens response cookies into the stored credential
// string (loginCookieKeys order) plus the advertised SESSDATA expiry. Shared
// by the login ticket exchange and the passport cookie refresh.
func credentialCookie(cookies []*http.Cookie) (string, time.Time) {
	jar := map[string]*http.Cookie{}
	for _, cookie := range cookies {
		jar[cookie.Name] = cookie
	}
	var cookie string
	var expiresAt time.Time
	for _, key := range loginCookieKeys {
		entry, ok := jar[key]
		if !ok || entry.Value == "" {
			continue
		}
		if cookie != "" {
			cookie += "; "
		}
		cookie += key + "=" + entry.Value
		if key == "SESSDATA" && !entry.Expires.IsZero() {
			expiresAt = entry.Expires.UTC()
		}
	}
	return cookie, expiresAt
}

// cookieValue extracts one entry from a stored "a=1; b=2" cookie string.
func cookieValue(cookie, name string) string {
	for _, part := range strings.Split(cookie, ";") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(part), name+"="); ok {
			return value
		}
	}
	return ""
}

// cleanTicketURL strips the single-use ticket value before it can land in
// logs or error messages.
func cleanTicketURL(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}

// loginCookieKeys are the credential cookies worth keeping from the
// cross-domain redirect; everything else in that URL (gourl, callback, …) is
// navigation noise.
var loginCookieKeys = []string{"SESSDATA", "bili_jct", "DedeUserID", "DedeUserID__ckMd5", "sid"}

// cookieFromQuery parses the pre-2026-08 success url
// (…/crossDomain?SESSDATA=…&bili_jct=…&Expires=…); ok=false means the url
// carries no credential params — it is a ticket link instead.
func cookieFromQuery(rawURL string) (cookie string, expiresAt time.Time, ok bool) {
	parsed, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		return "", time.Time{}, false
	}
	query := parsed.Query()
	for _, key := range loginCookieKeys {
		if value := query.Get(key); value != "" {
			if cookie != "" {
				cookie += "; "
			}
			cookie += key + "=" + value
		}
	}
	if cookie == "" {
		return "", time.Time{}, false
	}
	// `Expires` is the SESSDATA lifetime in epoch seconds.
	if secs, convErr := strconv.ParseInt(query.Get("Expires"), 10, 64); convErr == nil && secs > 0 {
		expiresAt = time.Unix(secs, 0).UTC()
	}
	return cookie, expiresAt, true
}

// Account is the live session state read from /x/web-interface/nav.
type Account struct {
	LoggedIn  bool
	Mid       int64
	Uname     string
	ExpiresAt time.Time // zero when passport did not say
}

// NavAccount validates a cookie against the nav endpoint. Anonymous calls
// answer isLogin=false, so "not logged in" is a result, not an error.
func (c *Client) NavAccount(ctx context.Context, cookie string) (*Account, error) {
	data, err := c.getWith(ctx, apiBase+"/x/web-interface/nav", cookie,
		1500*time.Millisecond, 1, defaultReferer)
	if err != nil {
		return nil, err
	}
	var payload struct {
		IsLogin    bool   `json:"isLogin"`
		Mid        int64  `json:"mid"`
		Uname      string `json:"uname"`
		CookieInfo struct {
			Expire int64 `json:"expire"`
		} `json:"cookie_info"`
	}
	if err := unmarshalStrict(data, &payload); err != nil {
		return nil, err
	}
	account := &Account{LoggedIn: payload.IsLogin, Mid: payload.Mid, Uname: payload.Uname}
	if payload.CookieInfo.Expire > 0 {
		account.ExpiresAt = time.Unix(payload.CookieInfo.Expire, 0).UTC()
	}
	return account, nil
}

// QRPNG renders the login payload as a PNG for the admin endpoint.
func QRPNG(payloadURL string, size int) ([]byte, error) {
	qr, err := qrcode.New(payloadURL, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	return qr.PNG(size)
}

// QRASCII renders the login payload as terminal half-block text for the CLI
// subcommand (SSH sessions without port forwarding).
func QRASCII(payloadURL string) (string, error) {
	qr, err := qrcode.New(payloadURL, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return qr.ToSmallString(false), nil
}

func unmarshalStrict(data []byte, target any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty payload")
	}
	return json.Unmarshal(data, target)
}
