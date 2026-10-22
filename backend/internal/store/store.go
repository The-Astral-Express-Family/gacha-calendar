// Package store opens the SQLite database with GORM, runs AutoMigrate and
// seeds the static game rows. WAL mode plus a single writer connection keep
// concurrent readers safe while serializing writes.
package store

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/model"
)

// Open prepares the database file, migrates the schema and seeds games.
func Open(path string, logger *slog.Logger) (*gorm.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.New(logWriter{logger}, gormlogger.Config{
			SlowThreshold: 500 * time.Millisecond,
			LogLevel:      gormlogger.Warn,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY
	// entirely. Readers queue behind writes but the write volume is tiny.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// PRAGMAs below are database engine configuration (required by the
	// deployment spec), not data access — all data access goes through GORM.
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	if err := db.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := db.AutoMigrate(
		&model.GameState{},
		&model.Post{},
		&model.Version{},
		&model.Prediction{},
		&model.GameStats{},
		&model.Override{},
		&model.CollectState{},
	); err != nil {
		return nil, fmt.Errorf("automigrate: %w", err)
	}

	if err := seedGames(db); err != nil {
		return nil, fmt.Errorf("seed games: %w", err)
	}
	return db, nil
}

// seedGames upserts the static game registry, refreshing config fields while
// preserving collection status.
func seedGames(db *gorm.DB) error {
	for _, g := range games.All {
		row := model.GameState{
			ID:        string(g.ID),
			Name:      g.Name,
			Mid:       g.Mid,
			CycleDays: g.CycleDays,
			Enabled:   true,
		}
		if err := db.Where(model.GameState{ID: row.ID}).
			Assign(model.GameState{Name: row.Name, Mid: row.Mid, CycleDays: row.CycleDays}).
			FirstOrCreate(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

type logWriter struct{ logger *slog.Logger }

func (w logWriter) Printf(format string, args ...any) {
	w.logger.Warn("gorm", "msg", fmt.Sprintf(format, args...))
}
