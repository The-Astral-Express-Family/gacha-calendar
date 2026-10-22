// Package server wires the HTTP surface: JSON API under /api, collected
// covers under /covers, fallback visuals under /fallback, the SPA under /,
// and a no-store /healthz for load balancers and the CDN.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/api"
	"github.com/The-Astral-Express-Family/gacha-calendar/backend/web/assets"
)

const indexHTML = "index.html"

type staticFS struct {
	http.FileSystem
}

// Open rejects directory listings so unknown paths fall through to the SPA.
func (s staticFS) Open(name string) (http.File, error) {
	file, err := s.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.IsDir() {
		file.Close()
		return nil, fs.ErrNotExist
	}
	return file, nil
}

func cacheControl(maxAge time.Duration, immutable bool) string {
	value := "public, max-age=" + strconv.Itoa(int(maxAge.Seconds()))
	if immutable {
		value += ", immutable"
	}
	return value
}

// New builds the root handler.
func New(deps api.Deps, coversDir string, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// JSON API (never cached).
	api.Register(mux, deps)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})

	// Collected cover images: long cache, hashed filenames.
	coversServer := http.StripPrefix("/covers/", http.FileServer(staticFS{http.Dir(coversDir)}))
	mux.Handle("GET /covers/", cacheMiddleware(cacheControl(24*time.Hour, false), coversServer))

	// Fallback visuals: content is static per release.
	fallbackRoot, err := fs.Sub(assets.Fallback, assets.FallbackRoot)
	if err != nil {
		logger.Error("fallback assets missing", "error", err)
	} else {
		mux.Handle("GET /fallback/", cacheMiddleware(cacheControl(24*time.Hour, false),
			http.StripPrefix("/fallback/", http.FileServer(staticFS{http.FS(fallbackRoot)}))))
	}

	// SPA: hashed build assets are immutable; index.html revalidates fast.
	distRoot, err := fs.Sub(assets.Dist, assets.DistRoot)
	if err != nil {
		logger.Error("dist assets missing", "error", err)
	} else {
		spaAssets := http.StripPrefix("/", http.FileServer(staticFS{http.FS(distRoot)}))
		mux.Handle("GET /assets/", cacheMiddleware(cacheControl(365*24*time.Hour, true), spaAssets))
		spa := func(w http.ResponseWriter, r *http.Request) {
			index, err := fs.ReadFile(distRoot, indexHTML)
			if err != nil {
				http.Error(w, "frontend build missing", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
			_, _ = w.Write(index)
		}
		mux.HandleFunc("GET /{$}", spa)
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			// Unknown non-asset paths render the SPA (client-side routes).
			if strings.Contains(r.URL.Path, ".") {
				http.NotFound(w, r)
				return
			}
			spa(w, r)
		})
	}

	return logRequests(logger, mux)
}

func cacheMiddleware(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Skip per-request noise for cheap static hits; API and misses count.
		if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/healthz") {
			logger.Info("http", "method", r.Method, "path", r.URL.Path,
				"remote", api.ClientIP(r), "duration_ms", time.Since(start).Milliseconds())
		}
	})
}
