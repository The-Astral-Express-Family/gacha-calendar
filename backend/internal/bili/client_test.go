package bili

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func feedPage(items string, hasMore bool, offset string) string {
	return fmt.Sprintf(`{"code":0,"data":{"has_more":%t,"offset":%q,"items":[%s]}}`, hasMore, offset, items)
}

func dynItem(id string, pubTS int64, text string) string {
	return fmt.Sprintf(`{"id_str":%q,"modules":{"module_author":{"pub_ts":%d},`+
		`"module_dynamic":{"desc":{"text":%q},"major":{}}}}`, id, pubTS, text)
}

// feedHits counts only dynamics-feed requests (the anonymous cookie bootstrap
// also passes through the stub).
func feedHits(t *testing.T, hits *int) func(*http.Request) bool {
	t.Helper()
	return func(r *http.Request) bool {
		if r.URL.Path == "/x/polymer/web-dynamic/v1/feed/space" {
			*hits++
			return true
		}
		return false
	}
}

// TestRefererForImage: the bilibili CDN keeps its referer, publisher CDNs
// (hycdn) get none — a bilibili referer draws a 403 there.
func TestRefererForImage(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://i0.hdslb.com/bfs/new_dyn/a.jpg", defaultReferer},
		{"https://i1.hdslb.com/bfs/archive/b.webp", defaultReferer},
		{"https://web.hycdn.cn/upload/image/20261014/x.jpg", ""},
		{"https://sdk-webstatic.mihoyo.com/upload/ann/2026/b.png", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		if got := refererForImage(c.url); got != c.want {
			t.Errorf("refererForImage(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestDynamicsPaging walks the feed cursor: two screens, then has_more=false.
func TestDynamicsPaging(t *testing.T) {
	hits := 0
	isFeed := feedHits(t, &hits)
	now := time.Now().Unix()
	stubBili(t, func(w http.ResponseWriter, r *http.Request) {
		if !isFeed(r) {
			_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"x","b_4":"y"}}`))
			return
		}
		switch r.URL.Query().Get("offset") {
		case "":
			_, _ = w.Write([]byte(feedPage(
				dynItem("a", now-60, "最新一条")+","+dynItem("b", now-120, "第二条"), true, "cursor1")))
		case "cursor1":
			_, _ = w.Write([]byte(feedPage(
				dynItem("c", now-180, "第三条")+","+dynItem("d", now-240, "第四条"), false, "")))
		default:
			t.Errorf("unexpected offset %q", r.URL.Query().Get("offset"))
		}
	})
	client := New(quietLogger())
	dyn, err := client.Dynamics(context.Background(), "42")
	if err != nil {
		t.Fatalf("Dynamics: %v", err)
	}
	if len(dyn) != 4 || dyn[0].ID != "a" || dyn[3].ID != "d" {
		t.Fatalf("dynamics = %d items, ids %v", len(dyn), dyn)
	}
	if hits != 2 {
		t.Errorf("feed requests = %d, want 2", hits)
	}
}

// TestDynamicsPageWindowStopsEarly: a screen whose oldest item is beyond the
// 21-day window ends the walk even with has_more=true.
func TestDynamicsPageWindowStopsEarly(t *testing.T) {
	hits := 0
	isFeed := feedHits(t, &hits)
	now := time.Now().Unix()
	stubBili(t, func(w http.ResponseWriter, r *http.Request) {
		if !isFeed(r) {
			_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"x","b_4":"y"}}`))
			return
		}
		_, _ = w.Write([]byte(feedPage(
			dynItem("fresh", now, "新动态")+","+dynItem("old", now-30*24*3600, "旧动态"), true, "cursor1")))
	})
	client := New(quietLogger())
	dyn, err := client.Dynamics(context.Background(), "42")
	if err != nil {
		t.Fatalf("Dynamics: %v", err)
	}
	if len(dyn) != 2 {
		t.Fatalf("dynamics = %d items, want 2", len(dyn))
	}
	if hits != 1 {
		t.Errorf("feed requests = %d, want 1 (window should stop the walk)", hits)
	}
}

// TestDynamicsLaterPageFailureKeepsPartial: page 2 answering 412 must keep
// page 1's items instead of failing the whole call.
func TestDynamicsLaterPageFailureKeepsPartial(t *testing.T) {
	hits := 0
	isFeed := feedHits(t, &hits)
	now := time.Now().Unix()
	stubBili(t, func(w http.ResponseWriter, r *http.Request) {
		if !isFeed(r) {
			_, _ = w.Write([]byte(`{"code":0,"data":{"b_3":"x","b_4":"y"}}`))
			return
		}
		if r.URL.Query().Get("offset") == "" {
			_, _ = w.Write([]byte(feedPage(dynItem("a", now-60, "第一条"), true, "cursor1")))
			return
		}
		http.Error(w, "risk", http.StatusPreconditionFailed)
	})
	client := New(quietLogger())
	dyn, err := client.Dynamics(context.Background(), "42")
	if err != nil {
		t.Fatalf("Dynamics: %v (later-page failures must not fail the call)", err)
	}
	if len(dyn) != 1 || dyn[0].ID != "a" {
		t.Fatalf("dynamics = %v, want the first screen only", dyn)
	}
	if hits != 2 {
		t.Errorf("feed requests = %d, want 2", hits)
	}
}
