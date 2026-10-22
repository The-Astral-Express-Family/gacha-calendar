package bili

import (
	"context"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCorrespondPath(t *testing.T) {
	if bits := correspondPub.N.BitLen(); bits != 1024 {
		t.Fatalf("correspond key is %d bits, want the documented 1024-bit key", bits)
	}
	first, err := correspondPath(1700000000000)
	if err != nil {
		t.Fatalf("correspondPath: %v", err)
	}
	raw, err := hex.DecodeString(first)
	if err != nil {
		t.Fatalf("correspondPath is not lowercase hex: %v", err)
	}
	if len(raw) != correspondPub.Size() {
		t.Fatalf("ciphertext is %d bytes, want the %d-byte RSA block", len(raw), correspondPub.Size())
	}
	// OAEP is randomized: two calls must never share a ciphertext.
	second, _ := correspondPath(1700000000000)
	if first == second {
		t.Fatal("correspondPath repeated a ciphertext; OAEP seeding is broken")
	}
	// a zero timestamp falls back to the local clock
	fallback, err := correspondPath(0)
	if err != nil || fallback == "" {
		t.Fatalf("correspondPath(0) = (%q, %v), want a local-clock path", fallback, err)
	}
}

func TestCookieValue(t *testing.T) {
	cookie := "SESSDATA=a%2Cb; bili_jct=jct; DedeUserID=42"
	if got := cookieValue(cookie, "bili_jct"); got != "jct" {
		t.Fatalf("cookieValue bili_jct = %q", got)
	}
	if got := cookieValue(cookie, "SESSDATA"); got != "a%2Cb" {
		t.Fatalf("cookieValue SESSDATA = %q", got)
	}
	if got := cookieValue(cookie, "missing"); got != "" {
		t.Fatalf("cookieValue missing = %q, want empty", got)
	}
}

// stubRefreshFlow serves the whole passport rotation round: cookie/info →
// correspond → cookie/refresh → confirm/refresh. It asserts the handshake
// details as it goes and reports violations through t.
func stubRefreshFlow(t *testing.T, allowRefresh bool, navLogin bool, navExpire int64) {
	t.Helper()
	stubBili(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/x/web-interface/nav":
			if !navLogin {
				_, _ = w.Write([]byte(`{"code":0,"data":{"isLogin":false}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"isLogin":true,"mid":7,"uname":"u","cookie_info":{"expire":` +
				strconv.FormatInt(navExpire, 10) + `}}}`))
		case r.URL.Path == "/x/passport-login/web/cookie/info":
			refresh := "false"
			if allowRefresh {
				refresh = "true"
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"refresh":` + refresh + `,"timestamp":1}}`))
		case strings.HasPrefix(r.URL.Path, "/correspond/1/"):
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "SESSDATA=old") {
				t.Errorf("correspond cookie = %q, want the session cookie", got)
			}
			_, _ = w.Write([]byte(`<html><body><div id="1-name" hidden>abcdef0123456789abcdef0123456789</div></body></html>`))
		case r.URL.Path == "/x/passport-login/web/cookie/refresh":
			_ = r.ParseForm()
			if got := r.PostForm.Get("csrf"); got != "oldjct" {
				t.Errorf("refresh csrf = %q, want the old bili_jct", got)
			}
			if got := r.PostForm.Get("refresh_csrf"); got != "abcdef0123456789abcdef0123456789" {
				t.Errorf("refresh_csrf = %q, want the correspond div value", got)
			}
			if got := r.PostForm.Get("source"); got != "main_web" {
				t.Errorf("source = %q, want main_web", got)
			}
			if got := r.PostForm.Get("refresh_token"); got != "rt-old" {
				t.Errorf("refresh_token = %q, want the persisted token", got)
			}
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "SESSDATA=old") {
				t.Errorf("refresh cookie = %q, want the old session", got)
			}
			expiry := time.Unix(1862000000, 0).UTC()
			for _, ck := range []*http.Cookie{
				{Name: "SESSDATA", Value: "new%2Csess", Expires: expiry},
				{Name: "bili_jct", Value: "newjct"},
				{Name: "DedeUserID", Value: "7"},
				{Name: "DedeUserID__ckMd5", Value: "ck"},
				{Name: "buvid3", Value: "noise"}, // not a credential, dropped
			} {
				http.SetCookie(w, ck)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"refresh_token":"rt-new"}}`))
		case r.URL.Path == "/x/passport-login/web/confirm/refresh":
			_ = r.ParseForm()
			if got := r.PostForm.Get("csrf"); got != "newjct" {
				t.Errorf("confirm csrf = %q, want the NEW bili_jct", got)
			}
			if got := r.PostForm.Get("refresh_token"); got != "rt-old" {
				t.Errorf("confirm refresh_token = %q, want the OLD refresh_token", got)
			}
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "SESSDATA=new") {
				t.Errorf("confirm cookie = %q, want the new session", got)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func TestRefreshCookieRotation(t *testing.T) {
	stubRefreshFlow(t, true, true, 0)
	client := New(quietLogger())
	session := &Session{
		Cookie:       "SESSDATA=old; bili_jct=oldjct; DedeUserID=7",
		Mid:          7,
		RefreshToken: "rt-old",
	}
	rotated, err := client.RefreshCookie(context.Background(), session)
	if err != nil {
		t.Fatalf("RefreshCookie: %v", err)
	}
	if rotated == nil {
		t.Fatal("expected a rotation, got nil")
	}
	wantCookie := "SESSDATA=new%2Csess; bili_jct=newjct; DedeUserID=7; DedeUserID__ckMd5=ck"
	if rotated.Cookie != wantCookie {
		t.Fatalf("cookie = %q, want %q", rotated.Cookie, wantCookie)
	}
	if rotated.RefreshToken != "rt-new" {
		t.Fatalf("refresh_token = %q, want rt-new", rotated.RefreshToken)
	}
	if want := time.Unix(1862000000, 0).UTC(); !rotated.ExpiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", rotated.ExpiresAt, want)
	}
	if rotated.Mid != 7 || rotated.LastRefreshedAt.IsZero() {
		t.Fatalf("identity must survive rotation: %+v", rotated)
	}
	// the input session stays untouched (the caller decides whether to save)
	if session.Cookie != "SESSDATA=old; bili_jct=oldjct; DedeUserID=7" || !session.LastRefreshedAt.IsZero() {
		t.Fatalf("input session mutated: %+v", session)
	}
}

func TestRefreshCookieNotInWindow(t *testing.T) {
	stubRefreshFlow(t, false, true, 0)
	client := New(quietLogger())
	session := &Session{Cookie: "SESSDATA=old; bili_jct=oldjct", RefreshToken: "rt-old"}
	rotated, err := client.RefreshCookie(context.Background(), session)
	if err != nil {
		t.Fatalf("RefreshCookie: %v", err)
	}
	if rotated != nil {
		t.Fatalf("expected no rotation outside the window, got %+v", rotated)
	}
}

func TestSessionRefresherRefreshOnce(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "bili-session.json"))
	client := New(quietLogger())
	refresher := NewSessionRefresher(client, store, quietLogger())

	// no stored session → nothing to do
	if outcome, err := refresher.RefreshOnce(context.Background(), true); err != nil || outcome != RefreshNoSession {
		t.Fatalf("empty store = (%q, %v), want no_session", outcome, err)
	}

	// a session inside its final month rotates, persists and arms the client
	due := time.Now().UTC().Add(10 * 24 * time.Hour).Truncate(time.Second)
	stubRefreshFlow(t, true, true, due.Unix())
	if err := store.Save(&Session{
		Cookie: "SESSDATA=old; bili_jct=oldjct; DedeUserID=7",
		Mid:    7, Uname: "u",
		RefreshToken: "rt-old",
		ObtainedAt:   time.Now().UTC().Add(-150 * 24 * time.Hour),
		ExpiresAt:    due,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	client.ClearSessionCookie()
	outcome, err := refresher.RefreshOnce(context.Background(), true)
	if err != nil || outcome != RefreshRotated {
		t.Fatalf("due session = (%q, %v), want rotated", outcome, err)
	}
	saved, err := store.Load()
	if err != nil || saved == nil {
		t.Fatalf("Load after rotation: (%v, %v)", saved, err)
	}
	if !strings.Contains(saved.Cookie, "SESSDATA=new") || saved.RefreshToken != "rt-new" {
		t.Fatalf("store still holds the old credential: %+v", saved)
	}
	if saved.ExpiresAt.IsZero() || saved.LastRefreshedAt.IsZero() {
		t.Fatalf("rotation metadata missing: %+v", saved)
	}
	if !client.HasSessionCookie() {
		t.Fatal("rotated cookie not installed in the client")
	}

	// a comfortably distant expiry does not rotate
	far := time.Now().UTC().Add(100 * 24 * time.Hour).Truncate(time.Second)
	stubRefreshFlow(t, true, true, far.Unix())
	if err := store.Save(&Session{
		Cookie: "SESSDATA=old; bili_jct=oldjct", Mid: 7,
		RefreshToken: "rt-old", ObtainedAt: time.Now().UTC(), ExpiresAt: far,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if outcome, err := refresher.RefreshOnce(context.Background(), true); err != nil || outcome != RefreshNotDue {
		t.Fatalf("distant expiry = (%q, %v), want not_due", outcome, err)
	}

	// a logged-out session is reported dead and dropped from the client
	stubRefreshFlow(t, true, false, 0)
	client.SetSessionCookie("SESSDATA=old; bili_jct=oldjct")
	if outcome, err := refresher.RefreshOnce(context.Background(), true); err != nil || outcome != RefreshDead {
		t.Fatalf("logged out = (%q, %v), want dead_session", outcome, err)
	}
	if client.HasSessionCookie() {
		t.Fatal("dead session must be dropped from the client")
	}
}
