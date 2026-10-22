package bili

// SESSDATA renewal via the passport web cookie-refresh flow. A QR login does
// not have to die at its ~180-day expiry: inside the refresh window passport
// rotates the credential cookies for their current holder — cookie/info →
// correspond page → cookie/refresh → confirm/refresh — with no scan and no
// captcha. SessionRefresher drives that round on a schedule, so a long-lived
// service (and a session file carried across a server migration) stays
// logged in indefinitely while passport cooperates.
//
// Passport contract worth remembering here:
//   - rotation needs the current bili_jct as csrf, the correspond page's
//     hidden refresh_csrf and the persisted refresh_token
//   - the new cookies arrive as Set-Cookie on the refresh reply together
//     with a new refresh_token; the new cookie is live immediately
//   - confirm/refresh retires the OLD refresh_token AND the old cookie tied
//     to it. Skipping confirm is safe for the new cookie — the old token
//     simply stays valid — so a failed confirm is a warning, never a
//     rollback

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// correspondPublicKey is Bilibili's fixed 1024-bit RSA key for the correspond
// step (from the passport web cookie-refresh documentation; the same key the
// web client embeds).
const correspondPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDLgd2OAkcGVtoE3ThUREbio0Eg
Uc/prcajMKXvkCKFCWhJYJcLkcM2DKKcSeFpD/j6Boy538YXnR6VhcuUJOhH2x71
nzPjfdTcqMz7djHum0qSZA0AyCBDABUqCrfNgCiJ00Ra7GmRj+YCK1NJEuewlb40
JNrRuoEUXpabUzGB8QIDAQAB
-----END PUBLIC KEY-----`

var correspondPub = mustParseCorrespondKey()

func mustParseCorrespondKey() *rsa.PublicKey {
	block, _ := pem.Decode([]byte(correspondPublicKey))
	if block == nil {
		panic("bili: correspond public key is not valid PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("bili: correspond public key: " + err.Error())
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		panic("bili: correspond public key is not RSA")
	}
	return key
}

// correspondNameRe captures the hidden refresh_csrf from the correspond page:
// <div id="1-name" hidden>8d8b4082…</div>
var correspondNameRe = regexp.MustCompile(`(?s)<div[^>]*\bid="1-name"[^>]*>\s*([0-9a-f]+)\s*</div>`)

// correspondPath encrypts "refresh_<millis>" with RSA-OAEP-SHA256 the way
// the web client does; the lowercase-hex ciphertext is the correspond page's
// lookup key. Passport hands out the millis timestamp in the cookie/info
// reply — prefer it, falling back to the local clock.
func correspondPath(timestampMillis int64) (string, error) {
	if timestampMillis <= 0 {
		timestampMillis = time.Now().UnixMilli()
	}
	plaintext := []byte("refresh_" + strconv.FormatInt(timestampMillis, 10))
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, correspondPub, plaintext, nil)
	if err != nil {
		return "", fmt.Errorf("correspond oaep: %w", err)
	}
	return hex.EncodeToString(ciphertext), nil
}

// RefreshCookie performs one passport rotation round for the session's
// cookie. It returns an updated copy of the session when passport rotated,
// or (nil, nil) when passport reports the cookie is not inside its refresh
// window. The stored session file is not touched — the caller decides.
func (c *Client) RefreshCookie(ctx context.Context, session *Session) (*Session, error) {
	csrf := cookieValue(session.Cookie, "bili_jct")
	if csrf == "" {
		return nil, fmt.Errorf("stored cookie carries no bili_jct; rotation impossible, re-scan required")
	}
	data, err := c.getWith(ctx,
		passportBase+"/x/passport-login/web/cookie/info?csrf="+url.QueryEscape(csrf),
		session.Cookie, 1500*time.Millisecond, 1, defaultReferer)
	if err != nil {
		return nil, err
	}
	var info struct {
		Refresh   bool  `json:"refresh"`
		Timestamp int64 `json:"timestamp"` // millis; the correspond plaintext seed
	}
	if err := unmarshalStrict(data, &info); err != nil {
		return nil, err
	}
	if !info.Refresh {
		return nil, nil
	}

	path, pathErr := correspondPath(info.Timestamp)
	if pathErr != nil {
		return nil, pathErr
	}
	page, err := c.fetchPage(ctx, webBase+"/correspond/1/"+path, session.Cookie)
	if err != nil {
		return nil, err
	}
	match := correspondNameRe.FindSubmatch(page)
	if match == nil {
		return nil, &Error{Code: 0, Message: "correspond 页面未携带 refresh_csrf", URL: "correspond"}
	}

	form := url.Values{
		"csrf":          {csrf},
		"refresh_csrf":  {string(match[1])},
		"source":        {"main_web"},
		"refresh_token": {session.RefreshToken},
	}
	data, cookies, err := c.postFormJSON(ctx,
		passportBase+"/x/passport-login/web/cookie/refresh",
		session.Cookie, defaultReferer, form)
	if err != nil {
		return nil, err
	}
	var payload struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := unmarshalStrict(data, &payload); err != nil {
		return nil, err
	}
	newCookie, expiresAt := credentialCookie(cookies)
	if newCookie == "" {
		return nil, fmt.Errorf("passport rotation returned no credential cookies")
	}

	// Retire the OLD refresh_token along with the old cookie tied to it.
	// Best-effort by passport contract (see the file comment): the new cookie
	// already works regardless, so a failure is only logged.
	if session.RefreshToken != "" {
		confirm := url.Values{
			"csrf":          {cookieValue(newCookie, "bili_jct")},
			"refresh_token": {session.RefreshToken},
		}
		if _, _, confirmErr := c.postFormJSON(ctx,
			passportBase+"/x/passport-login/web/confirm/refresh",
			newCookie, defaultReferer, confirm); confirmErr != nil {
			c.log.Warn("bilibili old refresh_token retirement skipped", "error", confirmErr.Error())
		}
	}

	updated := *session
	updated.Cookie = newCookie
	if payload.RefreshToken != "" {
		updated.RefreshToken = payload.RefreshToken
	}
	if !expiresAt.IsZero() {
		updated.ExpiresAt = expiresAt
	}
	updated.LastRefreshedAt = time.Now().UTC()
	c.log.Info("bilibili session cookie rotated",
		"expires_at", updated.ExpiresAt.Format(time.RFC3339))
	return &updated, nil
}

// Refresh outcomes surfaced to logs and the admin endpoint.
const (
	RefreshNoSession   = "no_session"    // nothing stored; nothing to renew
	RefreshHeldOff     = "held_off"      // rotated recently (loop only, not forced)
	RefreshDead        = "dead_session"  // nav reports logged out — re-scan needed
	RefreshNavFailed   = "nav_failed"    // nav check unreachable; retry next tick
	RefreshNotDue      = "not_due"       // expiry comfortably ahead
	RefreshNotInWindow = "not_in_window" // passport declined (cookie/info)
	RefreshFailed      = "failed"        // rotation attempt errored
	RefreshRotated     = "rotated"       // new cookie saved and activated
)

const (
	// refreshWindow: rotations are attempted once the cookie's final month
	// begins; passport accepts roughly inside the same window.
	refreshWindow = 30 * 24 * time.Hour
	// refreshInterval is the background check cadence.
	refreshInterval = 12 * time.Hour
	// refreshStartDelay lets the collector settle before the first probe.
	refreshStartDelay = time.Minute
	// refreshHoldoff throttles re-rotation after a success (manual force
	// bypasses it): passport dislikes rapid rotation attempts.
	refreshHoldoff = 24 * time.Hour
)

// SessionRefresher keeps the stored login ahead of SESSDATA's ~180-day
// lifetime: it nav-checks the stored cookie, records the authoritative
// expiry passport reports, and rotates the cookie inside the final month. A
// rotated cookie is saved over the session file and swapped into the client
// live — neither long uptimes nor a server migration need a fresh QR scan.
type SessionRefresher struct {
	client   *Client
	sessions *SessionStore
	log      *slog.Logger
	now      func() time.Time
}

func NewSessionRefresher(client *Client, sessions *SessionStore, logger *slog.Logger) *SessionRefresher {
	return &SessionRefresher{client: client, sessions: sessions, log: logger, now: time.Now}
}

// Run probes shortly after start and then on the cadence until ctx ends.
func (r *SessionRefresher) Run(ctx context.Context) {
	timer := time.NewTimer(refreshStartDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		outcome, err := r.RefreshOnce(ctx, false)
		switch {
		case err != nil:
			r.log.Warn("bilibili session renewal attempt failed", "outcome", outcome, "error", err.Error())
		case outcome == RefreshRotated, outcome == RefreshDead:
			// logged inside RefreshOnce
		case outcome == RefreshNoSession, outcome == RefreshNotDue, outcome == RefreshHeldOff:
			// quiet: the healthy steady states
		default:
			r.log.Info("bilibili session renewal checked", "outcome", outcome)
		}
		timer.Reset(refreshInterval)
	}
}

// RefreshOnce runs one renewal attempt; force bypasses the recent-rotation
// holdoff for the manual admin endpoint.
func (r *SessionRefresher) RefreshOnce(ctx context.Context, force bool) (string, error) {
	session, err := r.sessions.Load()
	if err != nil {
		return RefreshFailed, fmt.Errorf("read session: %w", err)
	}
	if session == nil {
		return RefreshNoSession, nil
	}
	now := r.now()
	if !force && !session.LastRefreshedAt.IsZero() && now.Sub(session.LastRefreshedAt) < refreshHoldoff {
		return RefreshHeldOff, nil
	}

	// nav is the source of truth: it proves the cookie still logs in and
	// reports the authoritative expiry (which fills sessions saved without
	// one — e.g. the pre-rotation ticket flow).
	navCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	account, navErr := r.client.NavAccount(navCtx, session.Cookie)
	cancel()
	if navErr != nil {
		var rejected *Error
		if errors.As(navErr, &rejected) && rejected.Code == -101 {
			r.client.ClearSessionCookie()
			r.log.Warn("bilibili session rejected (nav -101); re-scan required", "mid", session.Mid)
			return RefreshDead, nil
		}
		return RefreshNavFailed, fmt.Errorf("nav check: %w", navErr)
	}
	if !account.LoggedIn {
		r.client.ClearSessionCookie()
		r.log.Warn("bilibili session is logged out; re-scan required", "mid", session.Mid)
		return RefreshDead, nil
	}
	if !account.ExpiresAt.IsZero() && !account.ExpiresAt.Equal(session.ExpiresAt) {
		updated := *session
		updated.ExpiresAt = account.ExpiresAt
		if saveErr := r.sessions.Save(&updated); saveErr != nil {
			r.log.Warn("record nav expiry", "error", saveErr.Error())
		}
		session = &updated
	}
	// Rotate once the final month begins; with an unknown expiry let
	// passport arbitrate through cookie/info.
	if !session.ExpiresAt.IsZero() && now.Before(session.ExpiresAt.Add(-refreshWindow)) {
		return RefreshNotDue, nil
	}

	rotated, err := r.client.RefreshCookie(ctx, session)
	if err != nil {
		return RefreshFailed, err
	}
	if rotated == nil {
		return RefreshNotInWindow, nil
	}
	if err := r.sessions.Save(rotated); err != nil {
		// Still install the live cookie: it works now, and the next tick
		// retries the save once the disk issue clears.
		r.client.SetSessionCookie(rotated.Cookie)
		r.log.Error("save rotated bilibili session failed", "error", err.Error())
		return RefreshFailed, fmt.Errorf("save rotated session: %w", err)
	}
	r.client.SetSessionCookie(rotated.Cookie)
	r.log.Info("bilibili session rotated",
		"mid", rotated.Mid, "uname", rotated.Uname,
		"expires_at", rotated.ExpiresAt.Format(time.RFC3339))
	return RefreshRotated, nil
}
