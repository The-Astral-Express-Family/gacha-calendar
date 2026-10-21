// Package covers downloads official post images to local files so pages never
// hotlink the Bilibili image CDN (which rejects foreign referers).
//
// Files are content-addressed: <base>.<8-byte-sha256><ext> is immutable and
// safe for long CDN caching; a stable alias <base><ext> always mirrors the
// newest image for a game.
package covers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
)

// Downloader stores cover images under a fixed directory layout:
//
//	<dir>/<game>.<hash>.<ext>      newest event main visual per game
//	<dir>/posts/<postID>.<hash>.<ext>  per-post image (update-notice banners)
type Downloader struct {
	dir string
	get func(ctx context.Context, url string) ([]byte, error)
	log *slog.Logger
}

func New(dir string, client *bili.Client, logger *slog.Logger) *Downloader {
	return &Downloader{dir: dir, get: client.Download, log: logger}
}

func extFor(url string, data []byte) string {
	lower := strings.ToLower(url)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif"} {
		if strings.Contains(lower, ext) {
			if ext == ".jpeg" {
				return ".jpg"
			}
			return ext
		}
	}
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return ".gif"
	default:
		return ".jpg"
	}
}

// store writes data to <dir>/<relBase>.<hash><ext> and returns the web path
// "/covers/<relBase>.<hash><ext>". Existing identical content is a no-op.
func (d *Downloader) store(relBase, image string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Join(d.dir, filepath.Dir(relBase)), 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:8])
	ext := extFor(image, data)
	name := fmt.Sprintf("%s.%s%s", relBase, hash, ext)
	dst := filepath.Join(d.dir, filepath.ToSlash(name))
	if current, err := os.ReadFile(dst); err == nil && len(current) == len(data) {
		currentSum := sha256.Sum256(current)
		if currentSum == sum {
			return "/covers/" + name, nil
		}
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return "/covers/" + name, nil
}

// SaveGame stores the newest main visual for a game. It returns the immutable
// hashed web path, or "" on failure.
func (d *Downloader) SaveGame(ctx context.Context, gameID, image string) string {
	if image == "" {
		return ""
	}
	data, err := d.get(ctx, image)
	if err != nil {
		d.log.Warn("cover download failed", "game", gameID, "error", err.Error())
		return ""
	}
	path, err := d.store(string(gameID), image, data)
	if err != nil {
		d.log.Warn("cover write failed", "game", gameID, "error", err.Error())
		return ""
	}
	// Stable alias mirrors the newest image for predictable references.
	alias := filepath.Join(d.dir, string(gameID)+extFor(image, data))
	if err := os.WriteFile(alias, data, 0o644); err != nil {
		d.log.Warn("cover alias write failed", "game", gameID, "error", err.Error())
	}
	return path
}

// SavePost stores the image of one post; returns the hashed web path or "".
func (d *Downloader) SavePost(ctx context.Context, postID uint, image string) string {
	if image == "" {
		return ""
	}
	data, err := d.get(ctx, image)
	if err != nil {
		d.log.Warn("post cover download failed", "post", postID, "error", err.Error())
		return ""
	}
	path, err := d.store(fmt.Sprintf("posts/%d", postID), image, data)
	if err != nil {
		return ""
	}
	return path
}
