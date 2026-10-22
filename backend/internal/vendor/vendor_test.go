package vendor

import (
	"testing"
	"time"
)

// Real payload fragments captured 2026-10-22.

func TestEndfieldNewsParse(t *testing.T) {
	page := `xxx\"cid\":\"4774\",\"tab\":\"news\",\"sticky\":true,` +
		`\"title\":\"「丹青渡」新版本导览上线，浏览领嵌晶玉！\",\"author\":\"\",` +
		`\"displayTime\":1791289380,\"cover\":\"https://web.hycdn.cn/x.png\",\"extraCover\":\"\",` +
		`\"brief\":\"「丹青渡」新版本导览专题网页上线，浏览即可领取奖励。\"}` +
		`{\"cid\":\"9326\",\"tab\":\"news\",\"sticky\":false,` +
		`\"title\":\"「丹青渡」版本创作征集活动进行中\",\"author\":\"\",` +
		`\"displayTime\":1792126800,\"cover\":\"https://web.hycdn.cn/y.png\",` +
		`\"brief\":\"活动期间创作相关作品即可参与。\"}`
	posts := parseEndfieldPage(page)
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	if posts[0].ExternalID != "ef-4774" {
		t.Errorf("id = %s", posts[0].ExternalID)
	}
	if !time.Unix(1791289380, 0).Equal(posts[0].PublishedAt) {
		t.Errorf("published = %s", posts[0].PublishedAt)
	}
	if posts[0].URL != "https://endfield.hypergryph.com/news/4774" {
		t.Errorf("url = %s", posts[0].URL)
	}
	if posts[0].Brief == "" {
		t.Error("brief not extracted")
	}
	if posts[0].Cover != "https://web.hycdn.cn/x.png" {
		t.Errorf("cover = %q", posts[0].Cover)
	}
	if posts[1].Cover != "https://web.hycdn.cn/y.png" {
		t.Errorf("cover[1] = %q", posts[1].Cover)
	}
}

func TestZZZAnnouncementParse(t *testing.T) {
	payload := `{"retcode":0,"message":"OK","data":{"list":[{"list":[
		{"ann_id":1340,"title":"<p style=\"white-space: pre-wrap;\">已知问题及游戏优化说明</p>","subtitle":"","type":3,"start_time":"2026-10-21 06:00:00"},
		{"ann_id":1388,"title":"<p>3.4版本前瞻直播预告</p>","subtitle":"11月20日 19:30","banner":"https://sdk-webstatic.mihoyo.com/upload/ann/2026/10/20/b.jpg","type":2,"start_time":"2026-11-13 10:00:00"}
	]}]}}`
	posts := parseMihoyoPayload([]byte(payload))
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	if posts[1].Title != "3.4版本前瞻直播预告" {
		t.Errorf("title = %q", posts[1].Title)
	}
	if posts[1].Cover != "https://sdk-webstatic.mihoyo.com/upload/ann/2026/10/20/b.jpg" {
		t.Errorf("cover = %q", posts[1].Cover)
	}
	want := time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC) // 10:00 CST
	if !posts[1].PublishedAt.Equal(want) {
		t.Errorf("published = %s, want %s", posts[1].PublishedAt, want)
	}
}
