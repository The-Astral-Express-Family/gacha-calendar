package collect

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/model"
)

// fakeCovers records downloads and always succeeds.
type fakeCovers struct{ saved []string }

func (f *fakeCovers) SaveGame(ctx context.Context, gameID, image string) string {
	return ""
}

func (f *fakeCovers) SavePost(ctx context.Context, postID uint, image string) string {
	f.saved = append(f.saved, image)
	return fmt.Sprintf("/covers/posts/%d.deadbeef.jpg", postID)
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Post{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

func testCollector(db *gorm.DB) *Collector {
	return &Collector{
		db:     db,
		covers: &fakeCovers{},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func post(t *testing.T, db *gorm.DB, row model.Post) {
	t.Helper()
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create post: %v", err)
	}
}

func findPost(t *testing.T, db *gorm.DB, externalID string) model.Post {
	t.Helper()
	var row model.Post
	if err := db.First(&row, "external_id = ?", externalID).Error; err != nil {
		t.Fatalf("load post %s: %v", externalID, err)
	}
	return row
}

func TestHealPostCovers(t *testing.T) {
	db := testDB(t)
	c := testCollector(db)
	now := time.Now().UTC()

	// Backfilled article with neither cover field; its id is in the list.
	post(t, db, model.Post{GameID: "genshin", SourceType: "article", ExternalID: "111",
		Title: "7.1更新说明", PublishTime: now.Add(-48 * time.Hour), FetchedAt: now})
	// Video post with a remote cover URL only.
	post(t, db, model.Post{GameID: "genshin", SourceType: "video", ExternalID: "BV1xx",
		Title: "前瞻回放", PublishTime: now.Add(-24 * time.Hour), FetchedAt: now,
		CoverURL: "https://i0.hdslb.com/bfs/archive/v1.jpg"})
	// Already complete: must stay untouched.
	post(t, db, model.Post{GameID: "genshin", SourceType: "article", ExternalID: "222",
		Title: "7.0更新说明", PublishTime: now.Add(-72 * time.Hour), FetchedAt: now,
		CoverURL: "https://i0.hdslb.com/x.jpg", CoverPath: "/covers/posts/9.abc.jpg"})
	// Article whose id is not on page one of the list: no image available.
	post(t, db, model.Post{GameID: "genshin", SourceType: "article", ExternalID: "999",
		Title: "6.9更新说明", PublishTime: now.Add(-96 * time.Hour), FetchedAt: now})
	// Another game's post: out of scope.
	post(t, db, model.Post{GameID: "wuwa", SourceType: "video", ExternalID: "BV2yy",
		Title: "别家", PublishTime: now, FetchedAt: now,
		CoverURL: "https://i0.hdslb.com/bfs/archive/v2.jpg"})

	articles := []bili.Article{
		{ID: "111", Title: "7.1更新说明", ImageURLs: []string{"https://i0.hdslb.com/bfs/new_dyn/a1.jpg"}},
		{ID: "222", Title: "7.0更新说明"}, // list row without images
	}
	c.healPostCovers(context.Background(), games.Game{ID: games.Genshin, Name: "原神"}, articles)

	if row := findPost(t, db, "111"); row.CoverURL != "https://i0.hdslb.com/bfs/new_dyn/a1.jpg" || row.CoverPath == "" {
		t.Errorf("article 111 not healed: url=%q path=%q", row.CoverURL, row.CoverPath)
	}
	if row := findPost(t, db, "BV1xx"); row.CoverPath == "" || row.CoverURL != "https://i0.hdslb.com/bfs/archive/v1.jpg" {
		t.Errorf("video BV1xx not localized: url=%q path=%q", row.CoverURL, row.CoverPath)
	}
	if row := findPost(t, db, "222"); row.CoverPath != "/covers/posts/9.abc.jpg" {
		t.Errorf("complete post 222 clobbered: path=%q", row.CoverPath)
	}
	if row := findPost(t, db, "999"); row.CoverURL != "" || row.CoverPath != "" {
		t.Errorf("post 999 should stay imageless: url=%q path=%q", row.CoverURL, row.CoverPath)
	}
	if row := findPost(t, db, "BV2yy"); row.CoverPath != "" {
		t.Errorf("other game's post touched: path=%q", row.CoverPath)
	}
}

func TestHealPostCoversCap(t *testing.T) {
	db := testDB(t)
	c := testCollector(db)
	c.covers = &fakeCovers{}
	now := time.Now().UTC()
	for i := 0; i < maxCoverHealPerGame+4; i++ {
		post(t, db, model.Post{GameID: "hsr", SourceType: "video",
			ExternalID: fmt.Sprintf("BV%d", i), Title: "v",
			PublishTime: now.Add(time.Duration(i) * time.Hour), FetchedAt: now,
			CoverURL: fmt.Sprintf("https://i0.hdslb.com/bfs/archive/%d.jpg", i)})
	}
	c.healPostCovers(context.Background(), games.Game{ID: games.HSR, Name: "崩坏：星穹铁道"}, nil)
	if got := len(c.covers.(*fakeCovers).saved); got != maxCoverHealPerGame {
		t.Errorf("downloads = %d, want %d", got, maxCoverHealPerGame)
	}
	var withPath int64
	db.Model(&model.Post{}).Where("cover_path != ''").Count(&withPath)
	if withPath != maxCoverHealPerGame {
		t.Errorf("localized posts = %d, want %d", withPath, maxCoverHealPerGame)
	}
}
