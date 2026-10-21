package main

// bilicheck is a manual probe for Bilibili endpoint availability; it is not
// part of the service binary.
import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/bili"
)

func main() {
	logger := slog.New(slog.NewTextHandler(nilWriter{}, nil))
	client := bili.New(logger)
	ctx := context.Background()
	for _, mid := range []string{"401742377", "1340190821"} {
		rows, err := client.FeatureVideos(ctx, mid, "前瞻")
		fmt.Println("series", mid, len(rows), err)
		for i, row := range rows {
			if i >= 3 {
				break
			}
			fmt.Println("  ", row.BVID, row.Title, time.Unix(row.Pubdate, 0).Format("2006-01-02"), row.AuthorID)
		}
	}
	rows, err := client.SearchVideos(ctx, "原神 版本前瞻特别节目")
	fmt.Println("search", len(rows), err)
	count := 0
	for _, row := range rows {
		if row.AuthorID == "401742377" {
			fmt.Println("  ", row.BVID, row.Title, time.Unix(row.Pubdate, 0).Format("2006-01-02"))
			count++
			if count >= 3 {
				break
			}
		}
	}
	dyn, err := client.Dynamics(ctx, "401742377")
	fmt.Println("dynamics", len(dyn), err)
}

type nilWriter struct{}

func (nilWriter) Write(p []byte) (int, error) { return len(p), nil }
