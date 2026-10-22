// Package vendor reads the game publishers' own announcement systems — the
// fallback that keeps working when Bilibili's API endpoints are risk-
// controlled. Two anonymous sources are wired:
//
//   - miHoYo's in-game announcement feed (getAnnList) — verified working for
//     nap_cn (ZZZ); the hk4e_cn / hkrpg_cn variants no longer answer
//     anonymously, so those games stay on the Bilibili column + live-room
//     pair (two different endpoints, one of which is not behind the shared
//     api.bilibili.com risk control).
//   - Hypergryph's official Endfield site — a Next.js SSR page whose embedded
//     payload carries the full news list (research letters, update notes)
//     with titles, unix timestamps and briefs.
//
// Kuro's 库街区 API and TapTap's web API both require app tokens and are
// intentionally not used.
package vendor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// cst is the national-server zone every vendor system speaks.
var cst = time.FixedZone("UTC+8", 8*3600)

// Post is one announcement from a publisher's own system.
type Post struct {
	ExternalID  string
	Title       string
	Subtitle    string
	Brief       string
	Cover       string // banner image URL, empty when none
	URL         string
	PublishedAt time.Time // UTC
}

// Client is a tiny plain HTTP getter on its own connection pool — these
// hosts are not Bilibili and do not share its rate limits.
type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) get(ctx context.Context, url, referer string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// --- miHoYo in-game announcement feed ---------------------------------------

type annEnvelope struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    struct {
		List []struct {
			List []struct {
				AnnID     int    `json:"ann_id"`
				Title     string `json:"title"`
				Subtitle  string `json:"subtitle"`
				Banner    string `json:"banner"`
				Type      int    `json:"type"`
				StartTime string `json:"start_time"`
				TagLabel  string `json:"tag_label"`
			} `json:"list"`
		} `json:"list"`
	} `json:"data"`
}

// FetchMihoyoAnnouncements reads the in-game announcement feed for one game
// business. Works for nap_cn; hk4e_cn/hkrpg_cn endpoints currently hang or
// 404 for anonymous callers on every host variant tried.
func (c *Client) FetchMihoyoAnnouncements(ctx context.Context, gameBiz, game, region string) ([]Post, error) {
	url := ("https://api-takumi.mihoyo.com/common/" + gameBiz +
		"/announcement/api/getAnnList?game=" + game + "&game_biz=" + gameBiz +
		"&lang=zh-cn&bundle_id=" + gameBiz + "&platform=pc&region=" + region +
		"&level=60&channel_id=1")
	body, err := c.get(ctx, url, "https://webstatic.mihoyo.com/", 4<<20)
	if err != nil {
		return nil, err
	}
	var payload annEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Retcode != 0 {
		return nil, fmt.Errorf("getAnnList %s: retcode %d (%s)", gameBiz, payload.Retcode, payload.Message)
	}
	return parseMihoyoPayload(body), nil
}

// parseMihoyoPayload maps the getAnnList response onto vendor posts.
func parseMihoyoPayload(body []byte) []Post {
	var payload annEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	var out []Post
	for _, group := range payload.Data.List {
		for _, item := range group.List {
			published, parseErr := time.ParseInLocation("2006-01-02 15:04:05", item.StartTime, cst)
			if parseErr != nil {
				published = time.Time{}
			}
			out = append(out, Post{
				ExternalID:  "ann-" + strconv.Itoa(item.AnnID),
				Title:       stripTags(item.Title),
				Subtitle:    stripTags(item.Subtitle),
				Cover:       item.Banner,
				PublishedAt: published.UTC(),
			})
		}
	}
	return out
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	return strings.TrimSpace(tagRe.ReplaceAllString(s, ""))
}

// --- Hypergryph Endfield official site --------------------------------------
//
// The site is a Next.js SSR page; the news list rides inside the RSC payload
// as escaped JSON: \"cid\":\"4774\",\"tab\":\"news\",\"sticky\":true,
// \"title\":\"…\",\"author\":\"\",\"displayTime\":1791289380,\"cover\":\"…\",
// \"brief\":\"…\"

var endfieldNewsItem = regexp.MustCompile(
	`\\?"cid\\?":\\?"(\d+)\\?",\\?"tab\\?":\\?"(news|notice)\\?",\\?"sticky\\?":(?:true|false),` +
		`\\?"title\\?":\\?"((?:[^"\\]|\\.)*?)\\?",\\?"author\\?":\\?"(?:[^"\\]|\\.)*?\\?",` +
		`\\?"displayTime\\?":(\d+)`)

var endfieldBrief = regexp.MustCompile(
	`\\?"brief\\?":\\?"((?:[^"\\]|\\.)*?)\\?"`)

// endfieldCover matches the plain "cover" field ("extraCover" does not
// match: its quote sits before "extra", not before "cover").
var endfieldCover = regexp.MustCompile(
	`\\?"cover\\?":\\?"((?:[^"\\]|\\.)*?)\\?"`)

func decodeEscaped(raw string) string {
	var out string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &out); err != nil {
		return raw
	}
	return out
}

// FetchEndfieldNews parses the official site's embedded news list.
func (c *Client) FetchEndfieldNews(ctx context.Context) ([]Post, error) {
	body, err := c.get(ctx, "https://endfield.hypergryph.com/", "", 8<<20)
	if err != nil {
		return nil, err
	}
	return parseEndfieldPage(string(body)), nil
}

// parseEndfieldPage extracts news items from the RSC payload embedded in the
// official site's HTML.
func parseEndfieldPage(page string) []Post {
	var out []Post
	for _, loc := range endfieldNewsItem.FindAllStringSubmatchIndex(page, -1) {
		cid := page[loc[2]:loc[3]]
		title := decodeEscaped(page[loc[6]:loc[7]])
		published := time.Unix(0, 0)
		if ts, err := strconv.ParseInt(page[loc[8]:loc[9]], 10, 64); err == nil {
			published = time.Unix(ts, 0)
		}
		brief := ""
		cover := ""
		// The cover and brief follow this item's displayTime; search a
		// bounded window after the match for the nearest of each.
		tail := page[loc[1]:min(len(page), loc[1]+1400)]
		if b := endfieldBrief.FindStringSubmatch(tail); b != nil {
			brief = decodeEscaped(b[1])
		}
		if cv := endfieldCover.FindStringSubmatch(tail); cv != nil {
			cover = decodeEscaped(cv[1])
		}
		out = append(out, Post{
			ExternalID:  "ef-" + cid,
			Title:       title,
			Brief:       brief,
			Cover:       cover,
			URL:         "https://endfield.hypergryph.com/news/" + cid,
			PublishedAt: published.UTC(),
		})
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
