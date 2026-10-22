// gacha-calendar serves the gacha version-calendar API and SPA.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/api"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/collect"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/config"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/covers"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/server"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/store"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "error", err)
		os.Exit(1)
	}

	// `gacha-calendar backup <target.db>` writes a consistent snapshot via
	// SQLite's VACUUM INTO (administrative engine command, not data SQL).
	// Used by the systemd backup timer; the server needs no sqlite3 CLI.
	if len(os.Args) >= 2 && os.Args[1] == "backup" {
		if len(os.Args) != 3 {
			logger.Error("usage: gacha-calendar backup <target.db>")
			os.Exit(2)
		}
		if err := runBackup(logger, os.Args[2]); err != nil {
			logger.Error("backup", "error", err)
			os.Exit(1)
		}
		return
	}

	// `gacha-calendar bili-login` runs the interactive QR login and writes
	// the session file; it shares the env config with the service.
	if len(os.Args) >= 2 && os.Args[1] == "bili-login" {
		if err := runBiliLogin(logger, cfg); err != nil {
			logger.Error("bili-login", "error", err)
			os.Exit(1)
		}
		return
	}

	db, err := store.Open(cfg.DBPath, logger)
	if err != nil {
		logger.Error("store", "error", err)
		os.Exit(1)
	}

	client := bili.New(logger)
	sessions := bili.NewSessionStore(cfg.SessionPath)
	if session, err := sessions.Load(); err != nil {
		logger.Warn("bilibili session file unreadable, continuing anonymously", "error", err.Error())
	} else if session != nil {
		if session.Expired(time.Now()) {
			logger.Warn("bilibili session past its expiry on disk; the refresher will still probe passport",
				"expired_at", session.ExpiresAt.Format(time.RFC3339),
				"hint", "若自动续期失败（登录已失效），在服务器重新执行 `gacha-calendar bili-login`")
		} else {
			client.SetSessionCookie(session.Cookie)
			logger.Info("bilibili session restored", "mid", session.Mid, "uname", session.Uname)
		}
	}
	downloader := covers.New(cfg.CoversDir, client, logger)
	collector := collect.New(db, client, downloader, logger)
	// Keeps the login ahead of its ~180d expiry: inside the final month the
	// stored cookie is rotated through passport, saved back to the session
	// file and swapped into the client live — no re-scan, no restart.
	refresher := bili.NewSessionRefresher(client, sessions, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go refresher.Run(ctx)
	if cfg.CollectOnStart {
		collector.Start(ctx, collect.ScheduleOptions{
			BaseInterval:  cfg.RefreshInterval,
			WatchInterval: cfg.WatchInterval,
			PrimeInterval: cfg.PrimeInterval,
			TickInterval:  cfg.TickInterval,
		}, cfg.StartupDelay)
	}
	// Guided first initialization: backfill the version history and derive
	// the calibration stats when the database is still thin.
	if cfg.BootstrapOnStart && collector.NeedsBootstrap() {
		logger.Info("version history is thin, starting guided bootstrap")
		collector.RequestBootstrap()
	}

	handler := server.New(api.Deps{
		DB:              db,
		Collector:       collector,
		Bili:            client,
		BiliSessions:    sessions,
		BiliRefresher:   refresher,
		AdminToken:      cfg.AdminToken,
		RefreshInterval: cfg.RefreshInterval,
		Version:         version,
		Log:             logger,
	}, cfg.CoversDir, logger)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", cfg.Addr, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "error", err)
	}
	logger.Info("bye")
}
