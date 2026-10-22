// Package games holds the static knowledge about the tracked games,
// ported from the dsh-schedule-gacha plugin: official Bilibili account ids,
// title patterns that identify a version-update notice, and the scheduling
// defaults used only when announcements do not state a time.
package games

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GameID identifies a tracked game.
type GameID string

const (
	Genshin  GameID = "genshin"
	HSR      GameID = "hsr"
	ZZZ      GameID = "zzz"
	WuWa     GameID = "wuwa"
	Endfield GameID = "endfield"
	NTE      GameID = "nte"
)

// PreviewMode decides how an officially published preview video maps back to
// the live broadcast:
//   - "previous-friday": HoYo publishes the replay the next day; the stream is
//     the most recent Friday 19:30 before publication.
//   - "same-day": WuWa / NTE / Endfield publish at or just after the stream,
//     so the publication date is the stream date.
type PreviewMode string

const (
	PreviewPreviousFriday PreviewMode = "previous-friday"
	PreviewSameDay        PreviewMode = "same-day"
)

type Game struct {
	ID           GameID
	Name         string
	Mid          string // official Bilibili account id
	CycleDays    int
	UpdateStart  string // "06:00" — usual maintenance start (CST)
	UpdateEnd    string // "11:00" — usual maintenance end (CST)
	PreviewStart string // "19:30"
	PreviewEnd   string // "21:00"
	PreviewMode  PreviewMode
	// HSR/ZZZ publish an explicit version end date; a truncated summary must
	// not silently downgrade to the arithmetic cadence.
	ExpectVersionEnd bool
	// Endfield has run 4-, 6- and 7-week versions; a date explicitly marked
	// as a content update 21–35 days out beats the arithmetic cycle.
	PhaseHint     bool
	UpdateTitle   *regexp.Regexp // identifies a version-update notice
	ExcludeTitle  *regexp.Regexp // compensations, FAQs, optimization notes…
	SeriesKeyword string         // official collection keyword
	SearchKeyword string         // search fallback keyword
	// Vendor groups same-publisher games (the three miHoYo titles). When two
	// of them would land a preview on the same evening, the publishers either
	// stagger by 30 minutes or push one stream to the next day; the planner
	// mirrors that on inferred slots.
	Vendor string
	// StaggerRank orders same-vendor games when resolving an inferred
	// preview collision (lower keeps the slot).
	StaggerRank int
	// Announcement watch: how many days before the expected preview the
	// teaser post typically appears (drives the poll schedule).
	TeaserLeadDays int
	// LiveRoomID is the official Bilibili live room. Its embedded system
	// announcement states the scheduled preview stream ("将于10月23日晚20:00
	// 开启") — an official teaser source that keeps working while the
	// dynamics feed is risk-controlled.
	LiveRoomID string
	// Accent colors for the generated fallback visuals.
	ColorFrom string
	ColorTo   string
}

// IsPreviewTeaserTitle reports whether a post title announces an upcoming
// preview program (the "前瞻预告" posts that state the stream date/time).
// Replays use 回放/回顾, teasers of scenes/characters are excluded.
var teaserNoise = regexp.MustCompile(`回放|回顾|短片|PV|预告片|实录|角色|场景`)

func IsPreviewTeaserTitle(title string) bool {
	return (strings.Contains(title, "前瞻") || strings.Contains(title, "直播")) &&
		(strings.Contains(title, "预告") || strings.Contains(title, "定档") ||
			strings.Contains(title, "节目")) &&
		!teaserNoise.MatchString(title)
}

var All = []Game{
	{
		ID: Genshin, Name: "原神", Mid: "401742377", CycleDays: 42,
		UpdateStart: "06:00", UpdateEnd: "11:00", PreviewStart: "19:30", PreviewEnd: "21:00",
		PreviewMode:   PreviewPreviousFriday,
		UpdateTitle:   regexp.MustCompile(`(\d+\.\d+)\s*版本更新说明`),
		ExcludeTitle:  regexp.MustCompile(`性能|设备|FAQ|补偿|优化|问题修复`),
		SeriesKeyword: "前瞻", SearchKeyword: "原神 前瞻",
		Vendor: "mihoyo", StaggerRank: 1, TeaserLeadDays: 4, LiveRoomID: "21987615",
		ColorFrom: "#3b82f6", ColorTo: "#1e3a8a",
	},
	{
		ID: HSR, Name: "崩坏：星穹铁道", Mid: "1340190821", CycleDays: 42,
		UpdateStart: "06:00", UpdateEnd: "11:00", PreviewStart: "19:30", PreviewEnd: "21:00",
		PreviewMode: PreviewPreviousFriday, ExpectVersionEnd: true,
		UpdateTitle:   regexp.MustCompile(`(\d+\.\d+)\s*版本[「『].*?[」』]\s*版本更新说明|(\d+\.\d+)\s*版本更新说明`),
		ExcludeTitle:  regexp.MustCompile(`补偿|延长|优化|问题修复|FAQ`),
		SeriesKeyword: "前瞻", SearchKeyword: "崩坏星穹铁道 前瞻",
		Vendor: "mihoyo", StaggerRank: 2, TeaserLeadDays: 4, LiveRoomID: "27263119",
		ColorFrom: "#a78bfa", ColorTo: "#4c1d95",
	},
	{
		ID: ZZZ, Name: "绝区零", Mid: "1636034895", CycleDays: 42,
		UpdateStart: "06:00", UpdateEnd: "11:00", PreviewStart: "19:30", PreviewEnd: "21:00",
		PreviewMode: PreviewPreviousFriday, ExpectVersionEnd: true,
		UpdateTitle:   regexp.MustCompile(`(\d+\.\d+)\s*版本[「『].*?[」』]\s*更新(?:公告|说明)|(\d+\.\d+)\s*版本更新(?:公告|说明)`),
		ExcludeTitle:  regexp.MustCompile(`FAQ|性能|设备|优化|问题修复`),
		SeriesKeyword: "前瞻", SearchKeyword: "绝区零 前瞻",
		Vendor: "mihoyo", StaggerRank: 3, TeaserLeadDays: 4, LiveRoomID: "32805602",
		ColorFrom: "#f59e0b", ColorTo: "#7c2d12",
	},
	{
		ID: WuWa, Name: "鸣潮", Mid: "1955897084", CycleDays: 42,
		UpdateStart: "04:00", UpdateEnd: "11:00", PreviewStart: "19:30", PreviewEnd: "21:00",
		PreviewMode:   PreviewSameDay,
		UpdateTitle:   regexp.MustCompile(`(\d+\.\d+)\s*版本(?:内容说明|更新维护预告)`),
		ExcludeTitle:  regexp.MustCompile(`补偿|优化|问题修复|FAQ`),
		SeriesKeyword: "前瞻", SearchKeyword: "鸣潮 前瞻",
		Vendor: "kuro", StaggerRank: 1, TeaserLeadDays: 5, LiveRoomID: "27354807",
		ColorFrom: "#22d3ee", ColorTo: "#155e75",
	},
	{
		ID: Endfield, Name: "明日方舟：终末地", Mid: "1265652806", CycleDays: 42,
		UpdateStart: "06:00", UpdateEnd: "12:00", PreviewStart: "19:30", PreviewEnd: "21:00",
		PreviewMode: PreviewSameDay, PhaseHint: true,
		UpdateTitle:   regexp.MustCompile(`版本更新说明`),
		ExcludeTitle:  regexp.MustCompile(`研发通讯|补偿|优化|问题修复|FAQ|测试`),
		SeriesKeyword: "前瞻", SearchKeyword: "明日方舟 终末地 前瞻",
		Vendor: "hypergryph", StaggerRank: 1, TeaserLeadDays: 6, LiveRoomID: "1921300321",
		ColorFrom: "#84cc16", ColorTo: "#365314",
	},
	{
		ID: NTE, Name: "异环", Mid: "3546636978489848", CycleDays: 42,
		UpdateStart: "06:00", UpdateEnd: "11:00", PreviewStart: "19:30", PreviewEnd: "21:00",
		PreviewMode:   PreviewSameDay,
		UpdateTitle:   regexp.MustCompile(`(\d+\.\d+)\s*版本[「『].*?[」』]\s*更新公告`),
		ExcludeTitle:  regexp.MustCompile(`停服维护|补偿|优化|问题修复|FAQ`),
		SeriesKeyword: "前瞻", SearchKeyword: "异环 前瞻",
		Vendor: "nte", StaggerRank: 1, TeaserLeadDays: 5, LiveRoomID: "1890951177",
		ColorFrom: "#f472b6", ColorTo: "#831843",
	},
}

var byID = func() map[GameID]Game {
	m := make(map[GameID]Game, len(All))
	for _, g := range All {
		m[g.ID] = g
	}
	return m
}()

// Get returns the static game record.
func Get(id GameID) (Game, bool) {
	g, ok := byID[id]
	return g, ok
}

// IDs returns the tracked game ids in display order.
func IDs() []GameID {
	ids := make([]GameID, 0, len(All))
	for _, g := range All {
		ids = append(ids, g.ID)
	}
	return ids
}

// CST is the business timezone: national-server standard time, no DST.
var CST = time.FixedZone("UTC+8", 8*3600)

// Minutes parses an "HH:MM" wall-clock string into minutes since midnight.
func Minutes(value string) (int, bool) {
	hourStr, minuteStr, found := strings.Cut(value, ":")
	if !found {
		return 0, false
	}
	hour, err := strconv.Atoi(hourStr)
	if err != nil {
		return 0, false
	}
	minute, err := strconv.Atoi(minuteStr)
	if err != nil {
		return 0, false
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// UpdateWindow returns the default maintenance window in minutes since midnight.
func (g Game) UpdateWindow() (start, end int) {
	s, _ := Minutes(g.UpdateStart)
	e, _ := Minutes(g.UpdateEnd)
	return s, e
}

// PreviewWindow returns the default preview window in minutes since midnight.
func (g Game) PreviewWindow() (start, end int) {
	s, _ := Minutes(g.PreviewStart)
	e, _ := Minutes(g.PreviewEnd)
	return s, e
}

// PreviewWindowMinute is the default preview start in minutes since midnight.
func (g Game) PreviewWindowMinute() int {
	s, _ := Minutes(g.PreviewStart)
	return s
}

// BumpVersion maps "7.1" → "7.2"; empty stays empty (Endfield has no numeric version).
func BumpVersion(version string) string {
	if version == "" {
		return ""
	}
	majorStr, minorStr, found := strings.Cut(strings.TrimSpace(version), ".")
	if !found {
		return ""
	}
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return ""
	}
	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d.%d", major, minor+1)
}
