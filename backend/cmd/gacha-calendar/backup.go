package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/config"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/store"
)

// backupTargetPattern guards the inlined VACUUM INTO argument: SQLite does
// not bind parameters for the snapshot filename, so restrict it to plain
// paths the operator controls.
var backupTargetPattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

func runBackup(logger *slog.Logger, target string) error {
	if !backupTargetPattern.MatchString(target) {
		return fmt.Errorf("target path contains unsupported characters")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		// VACUUM INTO refuses to overwrite; rotate the old snapshot away.
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	db, err := store.Open(cfg.DBPath, logger)
	if err != nil {
		return err
	}
	if err := db.Exec(fmt.Sprintf("VACUUM INTO '%s'", target)).Error; err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	logger.Info("backup written", "target", target)
	return nil
}
