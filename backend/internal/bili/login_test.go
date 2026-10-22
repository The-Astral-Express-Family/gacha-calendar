package bili

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCookieFromQuery(t *testing.T) {
	const raw = "https://passport.biligame.com/crossDomain?DedeUserID=123456" +
		"&DedeUserID__ckMd5=abc&Expires=1790000000&SESSDATA=sess%2Fdata" +
		"&bili_jct=jct&gourl=https%3A%2F%2Fwww.bilibili.com&callback=close"
	cookie, expiresAt, ok := cookieFromQuery(raw)
	if !ok {
		t.Fatal("expected the legacy query format to be recognized")
	}
	want := "SESSDATA=sess/data; bili_jct=jct; DedeUserID=123456; DedeUserID__ckMd5=abc"
	if cookie != want {
		t.Fatalf("cookie = %q, want %q", cookie, want)
	}
	if wantExp := time.Unix(1790000000, 0).UTC(); !expiresAt.Equal(wantExp) {
		t.Fatalf("expiresAt = %v, want %v", expiresAt, wantExp)
	}
	// A ticket link carries no credential params.
	if _, _, ok := cookieFromQuery("https://passport.biligame.com/x/passport-login/web/crossDomain?ticket=t123"); ok {
		t.Fatal("ticket link must not parse as the legacy format")
	}
}

func TestMergeCookie(t *testing.T) {
	cases := []struct{ session, base, want string }{
		{"", "", ""},
		{"SESSDATA=x", "", "SESSDATA=x"},
		{"", "buvid3=y", "buvid3=y"},
		{"SESSDATA=x", "buvid3=y; buvid4=z", "SESSDATA=x; buvid3=y; buvid4=z"},
	}
	for _, c := range cases {
		if got := mergeCookie(c.session, c.base); got != c.want {
			t.Errorf("mergeCookie(%q, %q) = %q, want %q", c.session, c.base, got, c.want)
		}
	}
}

func TestSessionStoreRoundtrip(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "bili-session.json"))

	if session, err := store.Load(); err != nil || session != nil {
		t.Fatalf("Load on missing file = (%v, %v), want (nil, nil)", session, err)
	}

	saved := &Session{
		Cookie:     "SESSDATA=x; bili_jct=y",
		Mid:        42,
		Uname:      "测试用户",
		ObtainedAt: time.Unix(1700000000, 0).UTC(),
		ExpiresAt:  time.Unix(1800000000, 0).UTC(),
	}
	if err := store.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Cookie != saved.Cookie || loaded.Mid != saved.Mid || loaded.Uname != saved.Uname {
		t.Fatalf("roundtrip mismatch: %+v", loaded)
	}
	if !loaded.ObtainedAt.Equal(saved.ObtainedAt) || !loaded.ExpiresAt.Equal(saved.ExpiresAt) {
		t.Fatalf("timestamps mismatch: %+v", loaded)
	}
	if loaded.Expired(time.Unix(1750000000, 0)) {
		t.Fatal("session reported expired before its expiry")
	}
	if !loaded.Expired(time.Unix(1850000000, 0)) {
		t.Fatal("session not reported expired after its expiry")
	}

	// Windows ignores Unix permission bits on file creation; the 0600 mode
	// only materializes on the Linux deployment target.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.Path())
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("session file mode = %o, want 600", perm)
		}
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if session, err := store.Load(); err != nil || session != nil {
		t.Fatalf("Load after Clear = (%v, %v), want (nil, nil)", session, err)
	}
}

// stubBili swaps the package hosts for a stub server and restores them; it
// also shrinks the serial-chain floor so multi-request flows stay fast.
func stubBili(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	previousPassport, previousAPI, previousWeb := passportBase, apiBase, webBase
	previousGap := minRequestGap
	passportBase, apiBase, webBase = server.URL, server.URL, server.URL
	minRequestGap = time.Millisecond
	t.Cleanup(func() {
		passportBase, apiBase, webBase = previousPassport, previousAPI, previousWeb
		minRequestGap = previousGap
		server.Close()
	})
	return server
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestQRPollStateMachine walks the poll responses the way bilibili sends
// them: the outer envelope stays code 0 while the QR state hides inside
// data.code.
func TestQRPollStateMachine(t *testing.T) {
	responses := []string{
		`{"code":0,"message":"OK","data":{"url":"","refresh_token":"","timestamp":0,"code":86101,"message":"未扫码"}}`,
		`{"code":0,"message":"OK","data":{"url":"","refresh_token":"","timestamp":0,"code":86090,"message":"已扫码"}}`,
		`{"code":0,"message":"OK","data":{"url":"` + stubCrossDomain + `","refresh_token":"rt123","timestamp":1,"code":0,"message":"0"}}`,
	}
	step := 0
	stubBili(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(responses[step]))
		step++
	})
	client := New(quietLogger())
	ctx := context.Background()

	state, err := client.QRPoll(ctx, "key")
	if err != nil || state.Status != "waiting" {
		t.Fatalf("poll 1 = (%v, %v), want waiting", state, err)
	}
	state, err = client.QRPoll(ctx, "key")
	if err != nil || state.Status != "scanned" {
		t.Fatalf("poll 2 = (%v, %v), want scanned", state, err)
	}
	state, err = client.QRPoll(ctx, "key")
	if err != nil || state.Status != "success" {
		t.Fatalf("poll 3 = (%v, %v), want success", state, err)
	}
	if want := "SESSDATA=sess; bili_jct=jct; DedeUserID=1; DedeUserID__ckMd5=ck"; state.Cookie != want {
		t.Fatalf("cookie = %q, want %q", state.Cookie, want)
	}
	if state.RefreshToken != "rt123" {
		t.Fatalf("refresh_token = %q", state.RefreshToken)
	}
}

const stubCrossDomain = `https://passport.biligame.com/crossDomain?SESSDATA=sess&bili_jct=jct&DedeUserID=1&DedeUserID__ckMd5=ck&gourl=x`

// TestQRPollTicketFormat covers the 2026-08+ success response: data.url is a
// single-use crossDomain ticket link whose reply delivers the cookies via
// Set-Cookie headers.
func TestQRPollTicketFormat(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/x/passport-login/web/crossDomain") {
			if r.Header.Get("User-Agent") == "" {
				t.Error("ticket exchange must send a browser UA")
			}
			expiry := time.Unix(1850000000, 0).UTC()
			for _, ck := range []*http.Cookie{
				{Name: "SESSDATA", Value: "sess%2Cxyz", Expires: expiry},
				{Name: "bili_jct", Value: "jct"},
				{Name: "DedeUserID", Value: "7"},
				{Name: "DedeUserID__ckMd5", Value: "ck"},
				{Name: "buvid3", Value: "noise"}, // not a credential, dropped
			} {
				http.SetCookie(w, ck)
			}
			w.Header().Set("Location", "https://www.bilibili.com/")
			w.WriteHeader(http.StatusFound)
			_, _ = w.Write([]byte("<html></html>"))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"OK","data":{"url":"` +
			server.URL + `/x/passport-login/web/crossDomain?ticket=t123","refresh_token":"rt9","code":0}}`))
	}))
	previousPassport, previousAPI := passportBase, apiBase
	passportBase, apiBase = server.URL, server.URL
	t.Cleanup(func() {
		passportBase, apiBase = previousPassport, previousAPI
		server.Close()
	})

	client := New(quietLogger())
	state, err := client.QRPoll(context.Background(), "key")
	if err != nil {
		t.Fatalf("QRPoll: %v", err)
	}
	if state.Status != "success" {
		t.Fatalf("status = %q, want success", state.Status)
	}
	want := "SESSDATA=sess%2Cxyz; bili_jct=jct; DedeUserID=7; DedeUserID__ckMd5=ck"
	if state.Cookie != want {
		t.Fatalf("cookie = %q, want %q", state.Cookie, want)
	}
	if state.RefreshToken != "rt9" {
		t.Fatalf("refresh_token = %q", state.RefreshToken)
	}
	if wantExp := time.Unix(1850000000, 0).UTC(); !state.ExpiresAt.Equal(wantExp) {
		t.Fatalf("expiresAt = %v, want %v", state.ExpiresAt, wantExp)
	}
}

func TestNavAccount(t *testing.T) {
	stubBili(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "SESSDATA=sess" {
			t.Errorf("nav cookie header = %q, want the fixed session", got)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"isLogin":true,"mid":42,"uname":"测试用户","cookie_info":{"expire":1800000000}}}`))
	})
	client := New(quietLogger())
	account, err := client.NavAccount(context.Background(), "SESSDATA=sess")
	if err != nil {
		t.Fatalf("NavAccount: %v", err)
	}
	if !account.LoggedIn || account.Mid != 42 || account.Uname != "测试用户" {
		t.Fatalf("account = %+v", account)
	}
	if want := time.Unix(1800000000, 0).UTC(); !account.ExpiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", account.ExpiresAt, want)
	}
}
