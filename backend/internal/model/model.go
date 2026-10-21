// Package model defines the GORM entities. All timestamps are stored and
// served in UTC; the national-server (Asia/Shanghai) reading of a time is a
// presentation concern handled by the API metadata and the frontend.
package model

import "time"

// GameState is the per-game runtime row: static fields are re-seeded from
// code on every start, collection status fields are updated by the collector.
type GameState struct {
	ID            string `gorm:"primaryKey;size:16"` // game id, e.g. "genshin"
	Name          string
	Mid           string
	CycleDays     int
	Enabled       bool   // participates in collection cycles
	Status        string `gorm:"size:16;default:idle"` // idle|ok|partial|error
	LastRefreshAt *time.Time
	LastError     string
	// LastArticleID is the external id of the newest parsed version-update
	// notice; the same id skips re-parsing (rate-limit protection).
	LastArticleID string `gorm:"size:32"`
	// CoverPath is the newest downloaded main visual for the game
	// ("/covers/<game>.<hash>.<ext>"), empty when none was fetched yet.
	CoverPath string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Post is one raw official post ("推文"): a column article, a dynamic or an
// uploaded video. Posts are immutable snapshots; re-collection updates
// FetchedAt but never rewrites PublishTime.
type Post struct {
	ID          uint   `gorm:"primaryKey"`
	GameID      string `gorm:"size:16;index:idx_posts_game_publish"`
	SourceType  string `gorm:"size:16;uniqueIndex:idx_posts_external"` // article|dynamic|video
	ExternalID  string `gorm:"size:32;uniqueIndex:idx_posts_external"`
	URL         string
	Title       string
	Summary     string // list summary / dynamic text (what the parser saw)
	Content     string // full text, when a full-column read was necessary
	Author      string
	PublishTime time.Time `gorm:"index:idx_posts_game_publish"`
	FetchedAt   time.Time
	CoverURL    string // original remote image, empty when none
	CoverPath   string // local path under /covers, empty when none
	// True when the collector treated this post as the newest version-update
	// notice for its game.
	IsUpdateNotice bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Version is one parsed historical version: the announcement facts of a
// version that already went live (or is about to). It is the calibration base
// for predicting the next version.
type Version struct {
	ID             uint      `gorm:"primaryKey"`
	GameID         string    `gorm:"size:16;uniqueIndex:idx_versions_game_update,priority:1"`
	Version        string    `gorm:"size:16"`
	VersionName    string    `gorm:"size:64"`
	UpdateStart    time.Time `gorm:"uniqueIndex:idx_versions_game_update,priority:2"`
	UpdateEnd      *time.Time
	VersionEnd     *time.Time // explicit "version ends at" fact
	PreviewStart   *time.Time
	PreviewEnd     *time.Time
	PhaseUpdate    *time.Time // Endfield: announced next-phase content update
	AnnouncementID *uint      // the Post this was parsed from
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Prediction is one generated plan for a game's next version. Every cycle
// writes a new row and marks the previous one superseded, so the list
// endpoints can paginate both the live plan and the prediction history.
type Prediction struct {
	ID                uint       `gorm:"primaryKey"`
	GameID            string     `gorm:"size:16;index:idx_predictions_game_generated"`
	Version           string     `gorm:"size:16"`
	UpdateStart       *time.Time `gorm:"index"`
	UpdateEnd         *time.Time
	PreviewStart      *time.Time `gorm:"index"`
	PreviewEnd        *time.Time
	UpdateConfidence  string `gorm:"size:16"` // official|inferred|agent
	PreviewConfidence string `gorm:"size:16"`
	UpdateEvidence    string
	PreviewEvidence   string
	AnnouncementID    *uint
	GeneratedAt       time.Time `gorm:"index:idx_predictions_game_generated"`
	// Active is true only for the newest non-superseded plan per game.
	Active bool `gorm:"index"`
	// SupersededBy points at the prediction that replaced this one.
	SupersededBy *uint
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Override is the Agent/human correction layer for one game. It layers over
// the rule-derived plan and is auto-invalidated by a newer official post.
type Override struct {
	ID           uint   `gorm:"primaryKey"`
	GameID       string `gorm:"size:16;uniqueIndex"`
	Version      string `gorm:"size:16"`
	UpdateStart  *time.Time
	UpdateEnd    *time.Time
	PreviewStart *time.Time
	PreviewEnd   *time.Time
	Note         string
	AppliedAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CollectState is a single-row table with service-level collection facts.
type CollectState struct {
	ID           uint `gorm:"primaryKey"` // always 1
	LastCycleAt  *time.Time
	LastCycleOK  int // games with status ok
	LastCycleAll int // games attempted
	LastError    string
	UpdatedAt    time.Time
}
