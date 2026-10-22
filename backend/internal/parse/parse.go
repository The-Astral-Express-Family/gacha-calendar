// Package parse ports the pure announcement-parsing and scheduling rules of
// the dsh-schedule-gacha plugin. No network, no storage, no clock reads:
// every function takes the text/timestamp it needs, so the six official
// announcement formats can be unit tested offline. All dates are handled as
// UTC+8 wall time and returned as time.Time in the fixed CST zone — never in
// the host zone.
package parse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
)

const day = 24 * time.Hour

// CST is the fixed national-server zone (UTC+8, no DST).
var CST = games.CST

// CSTISO constructs a validated UTC+8 time. The zero time and false are
// returned for impossible dates.
func CSTISO(year, month, dayOfMonth, hour, minute int) (time.Time, bool) {
	if month < 1 || month > 12 || dayOfMonth < 1 || dayOfMonth > 31 ||
		hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, false
	}
	probe := time.Date(year, time.Month(month), dayOfMonth, hour, minute, 0, 0, CST)
	// time.Date normalizes overflow (e.g. Feb 30 → Mar 2); reject that.
	if probe.Year() != year || int(probe.Month()) != month || probe.Day() != dayOfMonth {
		return time.Time{}, false
	}
	return probe, true
}

// Parts are the CST calendar fields of an instant.
type Parts struct {
	Year, Month, Day, Hour, Minute, Weekday int // Weekday: 0=Sunday … 6=Saturday
}

// CSTParts converts an instant to CST calendar fields; ok is false for zero times.
func CSTParts(t time.Time) (Parts, bool) {
	if t.IsZero() {
		return Parts{}, false
	}
	p := t.In(CST)
	return Parts{
		Year: p.Year(), Month: int(p.Month()), Day: p.Day(),
		Hour: p.Hour(), Minute: p.Minute(), Weekday: int(p.Weekday()),
	}, true
}

func partsFromRef(t time.Time) *Parts {
	p, ok := CSTParts(t)
	if !ok {
		return nil
	}
	return &p
}

// NormalizeCST re-expresses any offset-ful instant in CST without changing it.
func NormalizeCST(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(CST)
}

func addDays(t time.Time, days int) time.Time {
	if t.IsZero() {
		return t
	}
	p, _ := CSTParts(t)
	return time.Date(p.Year, time.Month(p.Month), p.Day+days, p.Hour, p.Minute, 0, 0, CST)
}

func addHours(t time.Time, hours int) time.Time {
	if t.IsZero() {
		return t
	}
	p, _ := CSTParts(t)
	return time.Date(p.Year, time.Month(p.Month), p.Day, p.Hour+hours, p.Minute, 0, 0, CST)
}

func atMinutes(t time.Time, minutes int) time.Time {
	p, ok := CSTParts(t)
	if !ok {
		return time.Time{}
	}
	out, _ := CSTISO(p.Year, p.Month, p.Day, minutes/60, minutes%60)
	return out
}

// AtMinutes re-expresses a CST instant at the given wall-clock minutes since
// midnight, keeping the calendar day.
func AtMinutes(t time.Time, minutes int) time.Time { return atMinutes(t, minutes) }

func dayDiff(from, to time.Time) (int, bool) {
	if from.IsZero() || to.IsZero() {
		return 0, false
	}
	return int(mathRound(to.Sub(from).Hours() / 24)), true
}

// DayDiffPublic exposes the rounded CST day distance for the collector's
// backfill matching.
func DayDiffPublic(from, to time.Time) (int, bool) { return dayDiff(from, to) }

// CSTEndAtDefault returns the usual preview end for a start instant (the
// game's default window end on the same calendar day).
func CSTEndAtDefault(start time.Time, game games.Game) (time.Time, bool) {
	_, endMinutes := game.PreviewWindow()
	end := atMinutes(start, endMinutes)
	if end.IsZero() {
		return time.Time{}, false
	}
	return end, true
}

func mathRound(v float64) float64 {
	if v < 0 {
		return float64(int(v - 0.5))
	}
	return float64(int(v + 0.5))
}

// resolveYear resolves a possibly year-less announcement date against the post
// date. A date more than half a year behind the post belongs to the next year.
func resolveYear(month, explicitYear int, haveExplicit bool, ref *Parts) (int, bool) {
	if haveExplicit {
		return explicitYear, true
	}
	if ref == nil {
		return 0, false
	}
	delta := month - ref.Month
	switch {
	case delta < -6:
		return ref.Year + 1, true
	case delta > 6:
		return ref.Year - 1, true
	default:
		return ref.Year, true
	}
}

var dateTimeRe = regexp.MustCompile(
	`(?:(\d{4})\s*[年/.]\s*)?(\d{1,2})\s*[月/.]\s*(\d{1,2})\s*日?(?:\s*[（(][^）)]*[）)])?\s*(?:晚上|晚间)?\s*(\d{1,2})\s*[:：]\s*(\d{2})`)

type DateTimeItem struct {
	At    time.Time
	Index int
	Text  string
}

// DateTimes returns every dated instant in a text, in source order.
func DateTimes(text string, published time.Time) []DateTimeItem {
	ref := partsFromRef(published)
	var out []DateTimeItem
	for _, m := range dateTimeRe.FindAllStringSubmatchIndex(text, -1) {
		year, haveYear := 0, false
		if m[2] >= 0 {
			year, _ = strconv.Atoi(text[m[2]:m[3]])
			haveYear = true
		}
		month, _ := strconv.Atoi(text[m[4]:m[5]])
		dayOfMonth, _ := strconv.Atoi(text[m[6]:m[7]])
		hour, _ := strconv.Atoi(text[m[8]:m[9]])
		minute, _ := strconv.Atoi(text[m[10]:m[11]])
		resolved, ok := resolveYear(month, year, haveYear, ref)
		if !ok {
			continue
		}
		at, ok := CSTISO(resolved, month, dayOfMonth, hour, minute)
		if !ok {
			continue
		}
		out = append(out, DateTimeItem{At: at, Index: m[0], Text: text[m[0]:m[1]]})
	}
	return out
}

func contextOf(text string, index, length, radius int) string {
	start := index - radius
	if start < 0 {
		start = 0
	}
	end := index + length + radius
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

// firstNear returns the first dated instant whose surrounding text matches near.
func firstNear(text string, published time.Time, near *regexp.Regexp, limit int) *DateTimeItem {
	for _, item := range DateTimes(text, published) {
		if limit >= 0 && item.Index > limit {
			break
		}
		if near.MatchString(contextOf(text, item.Index, len(item.Text), 60)) {
			return &item
		}
	}
	return nil
}

func labelledDate(text, label string, published time.Time) (time.Time, bool) {
	pattern := regexp.MustCompile(
		label + `[^\d\n]{0,24}?(?:(\d{4})\s*[年/.]\s*)?` +
			`(\d{1,2})\s*[月/.]\s*(\d{1,2})\s*日?\s*` +
			`(?:(?:晚上|晚间)\s*)?(\d{1,2})\s*[:：]\s*(\d{2})`)
	m := pattern.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	ref := partsFromRef(published)
	month, _ := strconv.Atoi(m[2])
	year, haveYear := 0, false
	if m[1] != "" {
		year, _ = strconv.Atoi(m[1])
		haveYear = true
	}
	resolved, ok := resolveYear(month, year, haveYear, ref)
	if !ok {
		return time.Time{}, false
	}
	dayOfMonth, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	return CSTISO(resolved, month, dayOfMonth, hour, minute)
}

var versionWithWord = regexp.MustCompile(`(\d+\.\d+)\s*版本`)
var quotedName = regexp.MustCompile(`[「『]([^」』]{1,40})[」』]`)
var quotedNameAfterVersion = regexp.MustCompile(`版本[「『]([^」』]{1,40})[」』]`)
var quotedNoise = regexp.MustCompile(`版本|更新|公告|说明|预告|通讯`)

type VersionInfo struct {
	Version     string
	VersionName string
}

// ExtractVersion pulls the numeric label (e.g. "4.6") plus the quoted version
// name from the title (falling back to the body opening).
func ExtractVersion(title, body string) VersionInfo {
	info := VersionInfo{}
	if len(body) > 800 {
		body = body[:800]
	}
	for _, source := range []string{title, body} {
		if m := versionWithWord.FindStringSubmatch(source); m != nil {
			info.Version = m[1]
			break
		}
	}
	quoted := quotedName.FindStringSubmatch(title)
	if quoted == nil {
		quoted = quotedNameAfterVersion.FindStringSubmatch(title)
	}
	if quoted != nil && !quotedNoise.MatchString(quoted[1]) {
		info.VersionName = strings.TrimSpace(quoted[1])
	}
	return info
}

// ExtractUpdateStart finds the official maintenance start. The label is far
// more reliable than "first date".
func ExtractUpdateStart(text string, published time.Time, game games.Game) (time.Time, bool) {
	if t, ok := labelledDate(text, `(?:更新开始时间|更新时间)`, published); ok {
		return t, true
	}
	if t, ok := labelledDate(text, `(?:更新维护时间|停服维护时间|维护时间)`, published); ok {
		return t, true
	}

	// NTE writes the date without a year before the range.
	nte := regexp.MustCompile(
		`更新维护时间[^\d\n]{0,24}(\d{1,2})\s*月\s*(\d{1,2})\s*日\s*(\d{1,2})\s*[:：]\s*(\d{2})`).
		FindStringSubmatch(text)
	if nte != nil {
		ref := partsFromRef(published)
		month, _ := strconv.Atoi(nte[1])
		year, ok := resolveYear(month, 0, false, ref)
		if ok {
			dayOfMonth, _ := strconv.Atoi(nte[2])
			hour, _ := strconv.Atoi(nte[3])
			minute, _ := strconv.Atoi(nte[4])
			if at, valid := CSTISO(year, month, dayOfMonth, hour, minute); valid {
				return at, true
			}
		}
	}

	// "将于9月24日06:00 – 11:00进行版本更新维护"
	rangeHint := regexp.MustCompile(`(?:~|～|-|–|—|至)\s*\d{1,2}\s*[:：]\s*\d{2}`)
	for _, item := range DateTimes(text, published) {
		around := contextOf(text, item.Index, len(item.Text), 70)
		if regexp.MustCompile(`(?:进行)?(?:版本)?更新维护|版本更新`).MatchString(around) &&
			rangeHint.MatchString(around) {
			return item.At, true
		}
	}

	// Last resort: the first update-related date in the opening section.
	if opened := firstNear(text, published, regexp.MustCompile(`更新|维护`), 1800); opened != nil {
		return opened.At, true
	}
	return time.Time{}, false
}

var expectedHours = regexp.MustCompile(`预计\s*(\d+)\s*个?小时(?:内)?完成`)
var windowEnd = regexp.MustCompile(`(?:~|～|-|–|—|至)\s*(\d{1,2})\s*[:：]\s*(\d{2})`)

// ExtractUpdateEnd finds the maintenance window end; falls back to the game's
// usual opening hour.
func ExtractUpdateEnd(text string, updateStart time.Time, game games.Game) time.Time {
	if updateStart.IsZero() {
		return time.Time{}
	}
	// The announcement may use `2026/09/30`, `2026年9月30日` or `9月30日`.
	// Find the maintenance start by its CST calendar day, not by one spelling.
	p, _ := CSTParts(updateStart)
	probe := text
	spellings := []string{
		fmt.Sprintf("%04d/%02d/%02d", p.Year, p.Month, p.Day),
		fmt.Sprintf("%d年%d月%d日", p.Year, p.Month, p.Day),
		fmt.Sprintf("%d月%d日", p.Month, p.Day),
	}
	for _, spelling := range spellings {
		if i := strings.Index(text, spelling); i >= 0 {
			end := i + 180
			if end > len(text) {
				end = len(text)
			}
			probe = text[i:end]
			break
		}
	}
	if m := expectedHours.FindStringSubmatch(probe); m != nil {
		hours, _ := strconv.Atoi(m[1])
		return addHours(updateStart, hours)
	}
	if m := windowEnd.FindStringSubmatch(probe); m != nil {
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		end := atMinutes(updateStart, hour*60+minute)
		if !end.After(updateStart) {
			end = addDays(end, 1)
		}
		return end
	}
	return DefaultUpdateEnd(updateStart, game)
}

var versionEndPatterns = []*regexp.Regexp{
	regexp.MustCompile(`版本结束时间为?\s*(\d{4})\s*[年/.]\s*(\d{1,2})\s*[月/.]\s*(\d{1,2})\s*日?\s*(\d{1,2})\s*[:：]\s*(\d{2})`),
	regexp.MustCompile(`版本(?:的)?持续时间[^。\n]{0,80}?[-–—~～至]\s*(\d{4})\s*[年/.]\s*(\d{1,2})\s*[月/.]\s*(\d{1,2})\s*日?\s*(\d{1,2})\s*[:：]\s*(\d{2})`),
	regexp.MustCompile(`版本(?:将)?(?:于|在)\s*(\d{4})\s*[年/.]\s*(\d{1,2})\s*[月/.]\s*(\d{1,2})\s*日?\s*(\d{1,2})\s*[:：]\s*(\d{2})\s*结束`),
}

// ExtractVersionEnd returns the explicit "version ends at" fact, when the
// game publishes one.
func ExtractVersionEnd(text string) (time.Time, bool) {
	for _, pattern := range versionEndPatterns {
		if m := pattern.FindStringSubmatch(text); m != nil {
			year, _ := strconv.Atoi(m[1])
			month, _ := strconv.Atoi(m[2])
			dayOfMonth, _ := strconv.Atoi(m[3])
			hour, _ := strconv.Atoi(m[4])
			minute, _ := strconv.Atoi(m[5])
			if at, ok := CSTISO(year, month, dayOfMonth, hour, minute); ok {
				return at, true
			}
		}
	}
	return time.Time{}, false
}

var previewContext = regexp.MustCompile(`前瞻|直播|播出`)

// ExtractExplicitPreviewAt finds a fully dated preview time in a text.
func ExtractExplicitPreviewAt(text string, published time.Time) (time.Time, bool) {
	for _, item := range DateTimes(text, published) {
		if previewContext.MatchString(contextOf(text, item.Index, len(item.Text), 80)) {
			return item.At, true
		}
	}
	return time.Time{}, false
}

var previewDateOnly = regexp.MustCompile(`(?:前瞻|直播)[^。\n]{0,40}?(\d{1,2})\s*月\s*(\d{1,2})\s*日`)

// ExtractPreviewAt finds an explicit preview date/time from a teaser, article
// or dynamic; date-only teasers use the usual 19:30 slot.
func ExtractPreviewAt(text string, published time.Time, game games.Game) (time.Time, bool) {
	if at, ok := ExtractExplicitPreviewAt(text, published); ok {
		return at, true
	}
	if m := previewDateOnly.FindStringSubmatch(text); m != nil {
		ref := partsFromRef(published)
		month, _ := strconv.Atoi(m[1])
		year, ok := resolveYear(month, 0, false, ref)
		if !ok {
			return time.Time{}, false
		}
		dayOfMonth, _ := strconv.Atoi(m[2])
		start, _ := game.PreviewWindow()
		return CSTISO(year, month, dayOfMonth, start/60, start%60)
	}
	return time.Time{}, false
}

var phaseUpdateNear = regexp.MustCompile(`更新|版本`)
var phaseUpdateNoise = regexp.MustCompile(`活动时间|寻访|申领|开放时间|补偿|领取`)

// ExtractPhaseUpdate finds a date marked as a content/version update between
// day 21 and 35 after the current update (Endfield).
func ExtractPhaseUpdate(text string, updateStart time.Time, game games.Game) (time.Time, bool) {
	if !game.PhaseHint || updateStart.IsZero() {
		return time.Time{}, false
	}
	for _, item := range DateTimes(text, time.Time{}) {
		around := contextOf(text, item.Index, len(item.Text), 50)
		if !phaseUpdateNear.MatchString(around) || phaseUpdateNoise.MatchString(around) {
			continue
		}
		offset, ok := dayDiff(updateStart, item.At)
		if !ok || offset < 21 || offset > 35 {
			continue
		}
		// Use the usual maintenance hour, not the content-update hour in the text.
		p, _ := CSTParts(item.At)
		start, _ := game.UpdateWindow()
		candidate, valid := CSTISO(p.Year, p.Month, p.Day, start/60, start%60)
		if !valid {
			continue
		}
		candidateOffset, ok := dayDiff(updateStart, candidate)
		if !ok || candidateOffset < 21 || candidateOffset > 35 {
			continue
		}
		return candidate, true
	}
	return time.Time{}, false
}

var nextUpdateNear = regexp.MustCompile(`(?:上线|更新|开启|维护)`)
var nextUpdateDateOnly = []*regexp.Regexp{
	regexp.MustCompile(`(\d{1,2})\s*月\s*(\d{1,2})\s*日[^。\n]{0,30}?(?:上线|更新|开启|维护)`),
	regexp.MustCompile(`(?:上线|更新|开启|维护)[^。\n]{0,20}?(\d{1,2})\s*月\s*(\d{1,2})\s*日`),
}

// NextUpdateAnnouncement is a "next version goes live on ..." sentence found
// in a dynamic or teaser.
type NextUpdateAnnouncement struct {
	Version string
	At      time.Time
}

func ExtractNextUpdateAnnouncement(text string, published time.Time, game games.Game) *NextUpdateAnnouncement {
	version := ""
	if m := versionWithWord.FindStringSubmatch(text); m != nil {
		version = m[1]
	}
	for _, item := range DateTimes(text, published) {
		if nextUpdateNear.MatchString(contextOf(text, item.Index, len(item.Text), 70)) {
			return &NextUpdateAnnouncement{Version: version, At: item.At}
		}
	}
	// Date-only announcements ("将于11月11日更新") use the usual opening hour.
	for _, pattern := range nextUpdateDateOnly {
		if m := pattern.FindStringSubmatch(text); m != nil {
			ref := partsFromRef(published)
			month, _ := strconv.Atoi(m[1])
			year, ok := resolveYear(month, 0, false, ref)
			if !ok {
				return nil
			}
			dayOfMonth, _ := strconv.Atoi(m[2])
			start, _ := game.UpdateWindow()
			at, valid := CSTISO(year, month, dayOfMonth, start/60, start%60)
			if !valid {
				return nil
			}
			return &NextUpdateAnnouncement{Version: version, At: at}
		}
	}
	return nil
}

// PreviewByRule returns the Friday in [update-14d, update-8d] at the game's
// preview hour.
func PreviewByRule(updateStart time.Time, game games.Game) (time.Time, bool) {
	if updateStart.IsZero() {
		return time.Time{}, false
	}
	start, _ := game.PreviewWindow()
	for offset := -14; offset <= -8; offset++ {
		dayAt := addDays(updateStart, offset)
		if p, ok := CSTParts(dayAt); ok && p.Weekday == 5 {
			return atMinutes(dayAt, start), true
		}
	}
	return time.Time{}, false
}

var previewTitleNoise = regexp.MustCompile(`场景前瞻|角色前瞻|前瞻短片|前瞻PV|预告片|实录|[Pp][Vv]`)

// IsPreviewTitle reports whether a title is a real preview program, not a
// character/scene teaser that also says "前瞻".
func IsPreviewTitle(title string) bool {
	return (strings.Contains(title, "前瞻") || strings.Contains(title, "直播")) &&
		!previewTitleNoise.MatchString(title)
}

// Video is an official upload considered as a preview candidate.
type Video struct {
	BVID    string
	Title   string
	Pubdate time.Time
}

var looseVersion = regexp.MustCompile(`(\d+\.\d+)`)

func videoVersion(video Video, game games.Game) string {
	if m := versionWithWord.FindStringSubmatch(video.Title); m != nil {
		return m[1]
	}
	if m := looseVersion.FindStringSubmatch(video.Title); m != nil {
		return m[1]
	}
	return ""
}

// PreviewFromVideo maps an official preview video publication to the live
// stream. A teaser often states the stream time in its title; that wins.
// Otherwise HoYo uploads the replay after the stream (previous Friday), while
// WuWa / NTE / Endfield upload around the stream itself.
func PreviewFromVideo(video Video, game games.Game, updateStart time.Time) (time.Time, bool) {
	if video.Pubdate.IsZero() {
		return time.Time{}, false
	}
	if explicit, ok := ExtractPreviewAt(video.Title, video.Pubdate, game); ok &&
		(updateStart.IsZero() || explicit.Before(updateStart)) {
		return explicit, true
	}
	p, ok := CSTParts(video.Pubdate)
	if !ok {
		return time.Time{}, false
	}
	start, _ := game.PreviewWindow()
	dayAt := video.Pubdate
	if game.PreviewMode == games.PreviewPreviousFriday {
		back := (p.Weekday - 5 + 7) % 7
		dayAt = addDays(video.Pubdate, -back)
	}
	preview := atMinutes(dayAt, start)
	if !updateStart.IsZero() && !preview.Before(updateStart) {
		preview = addDays(preview, -7)
	}
	return preview, true
}

// AlignToWeekday moves a date to the requested weekday, at most ±3 days away.
func AlignToWeekday(t time.Time, weekday int) time.Time {
	p, ok := CSTParts(t)
	if !ok {
		return time.Time{}
	}
	delta := (weekday - p.Weekday + 7) % 7
	if delta > 3 {
		delta -= 7
	}
	return addDays(t, delta)
}

// DefaultUpdateEnd applies the game's usual maintenance window to a start.
func DefaultUpdateEnd(updateStart time.Time, game games.Game) time.Time {
	_, endMinutes := game.UpdateWindow()
	end := atMinutes(updateStart, endMinutes)
	if !end.After(updateStart) {
		end = addDays(end, 1)
	}
	return end
}

// ArticleFacts is the complete parse of one official version article.
type ArticleFacts struct {
	Version     string
	VersionName string
	UpdateStart time.Time
	UpdateEnd   time.Time
	VersionEnd  time.Time // zero when absent
	PreviewAt   time.Time
	PhaseUpdate time.Time
}

func (f ArticleFacts) HasUpdateStart() bool { return !f.UpdateStart.IsZero() }

// ParseArticle parses one official version announcement (title + content).
func ParseArticle(title, content string, published time.Time, game games.Game) ArticleFacts {
	text := title + "\n" + content
	info := ExtractVersion(title, content)
	facts := ArticleFacts{Version: info.Version, VersionName: info.VersionName}

	if at, ok := ExtractUpdateStart(content, published, game); ok {
		facts.UpdateStart = at
	}
	if !facts.UpdateStart.IsZero() {
		facts.UpdateEnd = ExtractUpdateEnd(content, facts.UpdateStart, game)
	}
	if rawEnd, ok := ExtractVersionEnd(content); ok {
		// A "version duration" must span at least two weeks; anything shorter
		// is an event/banner window that happens to use the same dash notation.
		if rawEnd.After(facts.UpdateStart) {
			if diff, ok := dayDiff(facts.UpdateStart, rawEnd); ok && diff >= 14 {
				facts.VersionEnd = rawEnd
			}
		}
	}
	if at, ok := ExtractPreviewAt(text, published, game); ok {
		facts.PreviewAt = at
	}
	if !facts.UpdateStart.IsZero() {
		if at, ok := ExtractPhaseUpdate(content, facts.UpdateStart, game); ok {
			facts.PhaseUpdate = at
		}
	}
	return facts
}

// Announcement is a short official text (dynamic, teaser title) considered by
// the planner. Newest first.
type Announcement struct {
	Text    string
	Pubdate time.Time
	URL     string
}

type announcementFacts struct {
	Version   string
	Update    *NextUpdateAnnouncement
	PreviewAt time.Time
}

func parseAnnouncement(text string, published time.Time, game games.Game) announcementFacts {
	facts := announcementFacts{Version: ExtractVersion("", text).Version}
	facts.Update = ExtractNextUpdateAnnouncement(text, published, game)
	if at, ok := ExtractPreviewAt(text, published, game); ok {
		facts.PreviewAt = at
	}
	return facts
}

// MergeArticle is the summary-first merge: the 255-character list summary
// reliably carries the maintenance start and the version end date, while a
// full column may open with a *previous* phase's dates (Endfield). The full
// text therefore only fills fields the summary did not produce.
func MergeArticle(summary, full *ArticleFacts) ArticleFacts {
	if summary == nil {
		return *full
	}
	merged := *summary
	if merged.Version == "" {
		merged.Version = full.Version
	}
	if merged.VersionName == "" {
		merged.VersionName = full.VersionName
	}
	if merged.UpdateStart.IsZero() {
		merged.UpdateStart = full.UpdateStart
	}
	if merged.UpdateEnd.IsZero() {
		merged.UpdateEnd = full.UpdateEnd
	}
	if merged.VersionEnd.IsZero() {
		merged.VersionEnd = full.VersionEnd
	}
	if merged.PreviewAt.IsZero() {
		merged.PreviewAt = full.PreviewAt
	}
	if merged.PhaseUpdate.IsZero() {
		merged.PhaseUpdate = full.PhaseUpdate
	}
	return merged
}

// Plan is the decided next-version schedule with its evidence trail.
type Plan struct {
	Version           string
	VersionName       string
	UpdateStart       time.Time
	UpdateEnd         time.Time
	PreviewStart      time.Time
	PreviewEnd        time.Time
	UpdateConfidence  string // official|inferred|agent
	PreviewConfidence string // official|inferred|agent|none
	UpdateEvidence    string
	PreviewEvidence   string
	// IsPhaseUpdate marks Endfield-style mid-version content updates: a real
	// calendar milestone, but not a new version — normally no preview stream.
	IsPhaseUpdate bool
}

// HasPreview reports whether the plan expects a preview program.
func (p *Plan) HasPreview() bool {
	return p != nil && !p.PreviewStart.IsZero()
}

// PlanNext decides the next version's update and preview times:
//
//  1. official end of the current version
//  2. a content-update date in an irregular cadence (Endfield) — a phase
//     update, not a new version: no preview is fabricated for it
//  3. explicit "next version goes live" announcement
//  4. cadence arithmetic (calibrated stats when available, else the static
//     cycle length) aligned to the modal weekday
//  5. a version whose maintenance already ended is history: roll forward
//  6. preview: explicit announcement (validated against the update window) >
//     official preview video > calibrated rule > Friday rule; inferred slots
//     avoid sensitive dates, and same-vendor collisions are staggered by the
//     caller via StaggerVendorPreviews
func PlanNext(game games.Game, current *ArticleFacts, now time.Time,
	videos []Video, announcements []Announcement, cadence *Cadence) *Plan {
	if current == nil {
		return nil
	}
	version := current.Version
	versionName := current.VersionName
	baseStart := current.UpdateStart

	type candidate struct {
		version      string
		updateStart  time.Time
		confidence   string
		evidence     string
		isPhase      bool
		suppressLive bool // phase update: no preview stream to predict
	}
	var chosen *candidate

	if !current.VersionEnd.IsZero() {
		chosen = &candidate{
			version:     games.BumpVersion(version),
			updateStart: current.VersionEnd,
			confidence:  "official",
			evidence:    "版本结束时间",
		}
	}

	if chosen == nil && !current.PhaseUpdate.IsZero() &&
		current.PhaseUpdate.After(now.Add(-2*day)) {
		chosen = &candidate{
			// A phase update belongs to the current version — the number is
			// not bumped and no preview is invented for it.
			version:      version,
			updateStart:  current.PhaseUpdate,
			confidence:   "inferred",
			evidence:     "公告中的阶段内容更新时间",
			isPhase:      true,
			suppressLive: true,
		}
	}

	if chosen == nil {
		for _, item := range announcements {
			parsed := parseAnnouncement(item.Text, item.Pubdate, game)
			// An old dynamic about the version that is already live must not
			// be mistaken for the next one.
			if parsed.Update == nil || parsed.Update.At.IsZero() {
				continue
			}
			if !baseStart.IsZero() && !parsed.Update.At.After(baseStart) {
				continue
			}
			v := parsed.Update.Version
			if v == "" {
				v = games.BumpVersion(version)
			}
			chosen = &candidate{
				version:     v,
				updateStart: parsed.Update.At,
				confidence:  "official",
				evidence:    "官方更新公告",
			}
			break
		}
	}

	if chosen == nil && !baseStart.IsZero() {
		weekday := EffectiveUpdateWeekday(baseStart, cadence)
		chosen = &candidate{
			version:     games.BumpVersion(version),
			updateStart: AlignToWeekday(addDays(baseStart, EffectiveCycleDays(game, cadence)), weekday),
			confidence:  "inferred",
			evidence:    fmt.Sprintf("%d 天版本节奏", EffectiveCycleDays(game, cadence)),
		}
	}

	if chosen == nil || chosen.updateStart.IsZero() {
		return nil
	}

	// A version whose maintenance already ended is history: roll forward.
	for guard := 0; guard < 16; guard++ {
		end := DefaultUpdateEnd(chosen.updateStart, game)
		if chosen.updateStart.After(now) || !now.After(end) {
			break
		}
		baseStart = chosen.updateStart
		version = chosen.version
		versionName = ""
		weekday := EffectiveUpdateWeekday(baseStart, cadence)
		chosen = &candidate{
			version:     games.BumpVersion(version),
			updateStart: AlignToWeekday(addDays(baseStart, EffectiveCycleDays(game, cadence)), weekday),
			confidence:  "inferred",
			evidence:    fmt.Sprintf("%d 天版本节奏", EffectiveCycleDays(game, cadence)),
		}
	}

	updateStart := chosen.updateStart
	updateEnd := DefaultUpdateEnd(updateStart, game)

	// Surface (but do not move) an inferred update that lands on a sensitive
	// date — publishers do shift maintenance around those windows.
	updateEvidence := chosen.evidence
	if chosen.confidence == "inferred" {
		if name, blocked := SensitiveOn(updateStart); blocked {
			updateEvidence = fmt.Sprintf("%s（临近%s，官方可能调整）", chosen.evidence, name)
		}
	}

	// Preview: explicit announcement > official preview video > calibrated
	// rule > Friday rule. Phase updates predict no stream unless an explicit
	// announcement names one.
	var previewStart, previewEnd time.Time
	previewConfidence := ""
	previewEvidence := ""

	for _, item := range announcements {
		parsed := parseAnnouncement(item.Text, item.Pubdate, game)
		if parsed.PreviewAt.IsZero() {
			continue
		}
		if m := versionWithWord.FindStringSubmatch(item.Text); m != nil &&
			chosen.version != "" && m[1] != chosen.version {
			continue
		}
		// Sanity window: the stream must sit within the month before the
		// update it belongs to, and not deep in the past.
		if !parsed.PreviewAt.Before(updateStart) {
			continue
		}
		if !parsed.PreviewAt.After(now.Add(-2 * day)) {
			continue
		}
		if lead, ok := dayDiff(parsed.PreviewAt, updateStart); !ok || lead > 28 {
			continue
		}
		previewStart = parsed.PreviewAt
		if explicit, ok := ExtractExplicitPreviewAt(item.Text, item.Pubdate); ok && explicit.Equal(previewStart) {
			previewConfidence = "official"
			previewEvidence = "官方前瞻公告（明确日期与时间）"
		} else {
			previewConfidence = "inferred"
			previewEvidence = "官方前瞻日期，时刻按惯例推算"
		}
		break
	}

	if previewStart.IsZero() && !chosen.suppressLive {
		var matching []Video
		for _, video := range videos {
			if !IsPreviewTitle(video.Title) {
				continue
			}
			v := videoVersion(video, game)
			if chosen.version != "" && v != "" && v != chosen.version {
				continue
			}
			matching = append(matching, video)
		}
		// newest first
		for i := 0; i < len(matching)-1; i++ {
			for j := i + 1; j < len(matching); j++ {
				if matching[j].Pubdate.After(matching[i].Pubdate) {
					matching[i], matching[j] = matching[j], matching[i]
				}
			}
		}
		if len(matching) > 0 {
			video := matching[0]
			if at, ok := PreviewFromVideo(video, game, updateStart); ok &&
				at.Before(updateStart) && at.After(now.Add(-2*day)) {
				previewStart = at
				explicit, hasExplicit := ExtractExplicitPreviewAt(video.Title, video.Pubdate)
				if hasExplicit && explicit.Equal(previewStart) {
					previewConfidence = "official"
					previewEvidence = strings.TrimSpace("官方视频标题明确时间 " + video.BVID)
				} else {
					previewConfidence = "inferred"
					previewEvidence = strings.TrimSpace("官方视频发布日期回推（非确认时间） " + video.BVID)
				}
			}
		}
	}

	if previewStart.IsZero() && !chosen.suppressLive {
		if at, ok := PreviewByCadence(updateStart, game, cadence); ok {
			previewStart = at
			previewConfidence = "inferred"
			previewEvidence = "按历史前瞻节奏推算"
		}
	}

	if previewStart.IsZero() && !chosen.suppressLive {
		if at, ok := PreviewByRule(updateStart, game); ok {
			previewStart = at
			previewConfidence = "inferred"
			previewEvidence = "更新时间前两周的周五 " + game.PreviewStart
		}
	}

	if !previewStart.IsZero() && !previewStart.Before(updateStart) {
		if at, ok := PreviewByRule(updateStart, game); ok {
			previewStart = at
		}
		previewConfidence = "inferred"
		previewEvidence = "更新时间前两周的周五 " + game.PreviewStart
	}

	// Inferred slots step out of sensitive windows (official times never move).
	if previewConfidence == "inferred" && !previewStart.IsZero() {
		if shifted, name, moved := ShiftFromSensitive(previewStart); moved {
			previewStart = shifted
			previewEvidence = fmt.Sprintf("%s（避开%s）", previewEvidence, name)
		}
	}

	if previewStart.IsZero() {
		if chosen.suppressLive {
			previewConfidence = "none"
			previewEvidence = "阶段内容更新，通常无前瞻直播"
		}
	} else {
		_, endMinutes := game.PreviewWindow()
		previewEnd = atMinutes(previewStart, endMinutes)
	}

	return &Plan{
		Version:           chosen.version,
		VersionName:       versionName,
		UpdateStart:       updateStart,
		UpdateEnd:         updateEnd,
		PreviewStart:      previewStart,
		PreviewEnd:        previewEnd,
		UpdateConfidence:  chosen.confidence,
		PreviewConfidence: previewConfidence,
		UpdateEvidence:    updateEvidence,
		PreviewEvidence:   previewEvidence,
		IsPhaseUpdate:     chosen.isPhase,
	}
}
