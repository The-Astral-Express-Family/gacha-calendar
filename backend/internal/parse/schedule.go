// Scheduling policy rules that layer on top of the raw announcement facts:
// sensitive-date avoidance, same-vendor preview staggering and the
// statistics-derived cadence that replaces the hardcoded 42-day default once
// the bootstrap backfill has enough history. All pure, all CST wall-clock.
package parse

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
)

// --- sensitive dates -------------------------------------------------------
//
// Publishers reschedule previews (and sometimes maintenance) around major
// political dates and anniversaries that dominate the news cycle. Officially
// announced times are never moved — a published announcement is fact. Only
// inferred slots get shifted, with the reason kept in the evidence trail.
//
// Recurring windows are month/day based; lunar festivals (春节 etc.) vary per
// year and are intentionally not modelled here — an Override covers them when
// they matter.

type sensitiveWindow struct {
	Month, Day      int // window start (inclusive)
	LengthDays      int
	Name            string
	NoToleranceDays int // extra days before/after treated as risky
}

var annualSensitive = []sensitiveWindow{
	{Month: 3, Day: 1, LengthDays: 12, Name: "全国两会", NoToleranceDays: 1},
	{Month: 6, Day: 3, LengthDays: 3, Name: "舆论敏感期", NoToleranceDays: 0},
	{Month: 7, Day: 7, LengthDays: 1, Name: "七七事变纪念日", NoToleranceDays: 1},
	{Month: 9, Day: 18, LengthDays: 1, Name: "九一八事变纪念日", NoToleranceDays: 1},
	{Month: 9, Day: 28, LengthDays: 10, Name: "国庆前后", NoToleranceDays: 0},
	{Month: 12, Day: 13, LengthDays: 1, Name: "国家公祭日", NoToleranceDays: 1},
}

// SensitiveOn reports the sensitive window covering a CST calendar day, if any.
func SensitiveOn(t time.Time) (string, bool) {
	p, ok := CSTParts(t)
	if !ok {
		return "", false
	}
	for _, w := range annualSensitive {
		start := time.Date(p.Year, time.Month(w.Month), w.Day, 0, 0, 0, 0, CST)
		if start.Month() != time.Month(w.Month) || start.Day() != w.Day {
			continue // e.g. Feb 29 normalization
		}
		for offset := -w.NoToleranceDays; offset < w.LengthDays+w.NoToleranceDays; offset++ {
			dayAt := addDays(start, offset)
			if dp, ok := CSTParts(dayAt); ok && dp.Year == p.Year &&
				dp.Month == p.Month && dp.Day == p.Day {
				return w.Name, true
			}
		}
	}
	return "", false
}

// ShiftFromSensitive moves an inferred date forward day by day (at most 3)
// until it leaves every sensitive window; it returns the original when the
// whole neighbourhood is blocked. The clock time is preserved.
func ShiftFromSensitive(t time.Time) (time.Time, string, bool) {
	if _, blocked := SensitiveOn(t); !blocked {
		return t, "", false
	}
	name, _ := SensitiveOn(t)
	for offset := 1; offset <= 3; offset++ {
		candidate := addDays(t, offset)
		if _, blocked := SensitiveOn(candidate); !blocked {
			return candidate, name, true
		}
	}
	return t, "", false
}

// --- same-vendor preview staggering ----------------------------------------
//
// The miHoYo trio schedules its three Friday-evening special programs so they
// never overlap: when two would collide, one moves 30 minutes and a third
// (or a hard collision) is pushed to the next day. The planner applies the
// same arithmetic to inferred previews only — official announcements already
// carry the publisher's own resolution.

// StaggerVendorPreviews adjusts inferred previews so same-vendor games never
// share a slot. plans is mutated in place; ordering inside a vendor group is
// the games' StaggerRank (lower keeps the slot).
func StaggerVendorPreviews(plans map[games.GameID]*Plan) {
	byID := map[games.GameID]games.Game{}
	for id := range plans {
		if g, ok := games.Get(id); ok {
			byID[id] = g
		}
	}
	vendors := map[string][]games.GameID{}
	for id, g := range byID {
		if g.Vendor == "" || plans[id] == nil {
			continue
		}
		if plans[id].PreviewStart.IsZero() || plans[id].PreviewConfidence != "inferred" {
			continue
		}
		vendors[g.Vendor] = append(vendors[g.Vendor], id)
	}
	for _, ids := range vendors {
		if len(ids) < 2 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool {
			if byID[ids[i]].StaggerRank != byID[ids[j]].StaggerRank {
				return byID[ids[i]].StaggerRank < byID[ids[j]].StaggerRank
			}
			return ids[i] < ids[j]
		})
		taken := map[string]bool{} // "YYYY-MM-DD HH:MM" slots
		for _, id := range ids {
			plan := plans[id]
			slot := plan.PreviewStart.In(CST).Format("2006-01-02 15:04")
			if !taken[slot] {
				taken[slot] = true
				continue
			}
			// Second same-vendor game on the same minute: +30 minutes.
			staggered := plan.PreviewStart.Add(30 * time.Minute)
			slot2 := staggered.In(CST).Format("2006-01-02 15:04")
			if !taken[slot2] {
				taken[slot2] = true
				applyStaggeredPreview(byID[id], plan, staggered,
					"同厂商前瞻撞档，按惯例顺延 30 分钟")
				continue
			}
			// Hard collision: next day at the usual preview hour.
			next := atMinutes(addDays(plan.PreviewStart, 1), byID[id].PreviewWindowMinute())
			slot3 := next.In(CST).Format("2006-01-02 15:04")
			if !taken[slot3] {
				taken[slot3] = true
				applyStaggeredPreview(byID[id], plan, next,
					"同厂商前瞻撞档，按惯例推迟至次日")
			}
		}
	}
}

func applyStaggeredPreview(game games.Game, plan *Plan, at time.Time, reason string) {
	plan.PreviewStart = at
	_, endMinutes := game.PreviewWindow()
	plan.PreviewEnd = atMinutes(at, endMinutes)
	plan.PreviewEvidence = strings.TrimSpace(
		fmt.Sprintf("%s（原 %s）", reason, at.In(CST).Format("01-02 15:04")))
	if plan.PreviewConfidence == "" {
		plan.PreviewConfidence = "inferred"
	}
}

// --- statistics-derived cadence --------------------------------------------

// Cadence is what the bootstrap backfill derives per game from its parsed
// version history. Zero SampleCount means "no history, use static defaults".
type Cadence struct {
	SampleCount        int // versions used
	CycleDays          int // median update-to-update distance
	UpdateWeekday      int // modal update weekday (0=Sunday)
	PreviewLeadDays    int // median update - preview distance
	PreviewWeekday     int // modal preview weekday
	PreviewStartMinute int // modal preview minute-of-day
	AnnounceHour       int // modal hour of update-notice publication
}

// EffectiveCycleDays prefers calibrated history over the static default.
func EffectiveCycleDays(game games.Game, cadence *Cadence) int {
	if cadence != nil && cadence.SampleCount >= 3 && cadence.CycleDays >= 14 {
		return cadence.CycleDays
	}
	return game.CycleDays
}

// EffectiveUpdateWeekday prefers calibrated history over "keep the current
// version's weekday".
func EffectiveUpdateWeekday(current time.Time, cadence *Cadence) int {
	if cadence != nil && cadence.SampleCount >= 3 {
		return cadence.UpdateWeekday
	}
	if p, ok := CSTParts(current); ok {
		return p.Weekday
	}
	return 3
}

// PreviewByCadence returns the calibrated preview slot: the modal weekday at
// the modal hour, as close to the modal lead distance as that allows. It
// needs a real preview history — a zero lead (no preview times backfilled)
// must not degenerate into "a few days before the update".
func PreviewByCadence(updateStart time.Time, game games.Game, cadence *Cadence) (time.Time, bool) {
	if updateStart.IsZero() || cadence == nil || cadence.SampleCount < 3 {
		return time.Time{}, false
	}
	if cadence.PreviewLeadDays < 2 || cadence.PreviewWeekday < 0 || cadence.PreviewWeekday > 6 {
		return time.Time{}, false
	}
	minute := cadence.PreviewStartMinute
	if minute <= 0 {
		minute = game.PreviewWindowMinute()
	}
	for offset := -cadence.PreviewLeadDays - 3; offset <= -cadence.PreviewLeadDays+3; offset++ {
		dayAt := addDays(updateStart, offset)
		if p, ok := CSTParts(dayAt); ok && p.Weekday == cadence.PreviewWeekday {
			return atMinutes(dayAt, minute), true
		}
	}
	return time.Time{}, false
}

// MedianInt returns the median of a small int slice and whether it exists.
func MedianInt(values []int) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	return sorted[len(sorted)/2], true
}

// ModeInt returns the most frequent value (smallest on ties).
func ModeInt(values []int) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	counts := map[int]int{}
	for _, v := range values {
		counts[v]++
	}
	best, bestCount := 0, -1
	for v, n := range counts {
		if n > bestCount || (n == bestCount && v < best) {
			best, bestCount = v, n
		}
	}
	return best, true
}
