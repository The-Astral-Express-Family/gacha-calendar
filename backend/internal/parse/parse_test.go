package parse

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/The-Astral-Express-Family/gacha-calendar/backend/internal/games"
)

func game(id games.GameID) games.Game {
	g, _ := games.Get(id)
	return g
}

func utcMoment(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %s: %v", value, err)
	}
	return parsed
}

// The plugin's offline suite, ported 1:1 plus the 2026-10-21 live-capture
// snapshot from the plugin docs (§9) used as calibration data.

func TestHSR46ArticleParse(t *testing.T) {
	published := utcMoment(t, "2026-09-24T04:00:00Z")
	facts := ParseArticle(
		"4.6版本「长夜未明」版本更新说明",
		"亲爱的开拓者：\n■更新时间 2026/09/28 06:00\n4.6版本的持续时间为 2026/09/28 4.6版本更新后 - 2026/11/11 06:00。",
		published, game(games.HSR))
	assertISO(t, "version", facts.Version, "4.6")
	assertISO(t, "updateStart", facts.UpdateStart.Format(time.RFC3339), "2026-09-28T06:00:00+08:00")
	assertISO(t, "updateEnd", facts.UpdateEnd.Format(time.RFC3339), "2026-09-28T11:00:00+08:00")
	assertISO(t, "versionEnd", facts.VersionEnd.Format(time.RFC3339), "2026-11-11T06:00:00+08:00")
}

func TestHSR46PlanNext(t *testing.T) {
	published := utcMoment(t, "2026-09-24T04:00:00Z")
	current := ParseArticle(
		"4.6版本「长夜未明」版本更新说明",
		"亲爱的开拓者：\n■更新时间 2026/09/28 06:00\n4.6版本的持续时间为 2026/09/28 4.6版本更新后 - 2026/11/11 06:00。",
		published, game(games.HSR))
	plan := PlanNext(game(games.HSR), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil, nil, nil)
	if plan == nil {
		t.Fatal("plan is nil")
	}
	assertISO(t, "version", plan.Version, "4.7")
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-11-11T06:00:00+08:00")
	assertISO(t, "updateEnd", plan.UpdateEnd.Format(time.RFC3339), "2026-11-11T11:00:00+08:00")
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-30T19:30:00+08:00")
	assertISO(t, "updateConfidence", plan.UpdateConfidence, "official")
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "inferred")
}

func TestGenshin71Cadence(t *testing.T) {
	published := utcMoment(t, "2026-09-21T04:00:00Z")
	current := ParseArticle(
		"7.1版本更新说明",
		"〓更新时间〓\n2026/09/23 06:00\n亲爱的旅行者，7.1版本更新即将开启。",
		published, game(games.Genshin))
	assertISO(t, "updateStart", current.UpdateStart.Format(time.RFC3339), "2026-09-23T06:00:00+08:00")
	if !current.VersionEnd.IsZero() {
		t.Fatalf("versionEnd = %s, want zero", current.VersionEnd)
	}
	plan := PlanNext(game(games.Genshin), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil, nil, nil)
	if plan == nil {
		t.Fatal("plan is nil")
	}
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-11-04T06:00:00+08:00")
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-23T19:30:00+08:00")
	assertISO(t, "updateConfidence", plan.UpdateConfidence, "inferred")
}

func TestZZZ32Rolling(t *testing.T) {
	published := utcMoment(t, "2026-09-04T04:00:00Z")
	current := ParseArticle(
		"3.2版本「」更新公告",
		"【更新开始时间】 2026/09/09 06:00\n3.2版本结束时间为2026/10/21 06:00",
		published, game(games.ZZZ))
	assertISO(t, "versionEnd", current.VersionEnd.Format(time.RFC3339), "2026-10-21T06:00:00+08:00")

	during := PlanNext(game(games.ZZZ), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil, nil, nil)
	assertISO(t, "during version", during.Version, "3.3")
	assertISO(t, "during update", during.UpdateStart.Format(time.RFC3339), "2026-10-21T06:00:00+08:00")

	after := PlanNext(game(games.ZZZ), &current, utcMoment(t, "2026-10-21T04:00:00Z"), nil, nil, nil)
	assertISO(t, "after version", after.Version, "3.4")
	assertISO(t, "after update", after.UpdateStart.Format(time.RFC3339), "2026-12-02T06:00:00+08:00")
	assertISO(t, "after preview", after.PreviewStart.Format(time.RFC3339), "2026-11-20T19:30:00+08:00")
}

func TestZZZ33RealSummary(t *testing.T) {
	// 2026-10-21 07:00 published cv53529311, list summary truncated to exactly
	// 255 chars and still carries the maintenance window and version end date.
	summary := "亲爱的绳匠： 以下是3.3版本——「重返天空的旅程」的更新与补偿内容。 【更新开始时间】 2026/10/21 06:00（UTC+8） 预计5个小时完成。 【补偿说明】 • 停服更新补偿 补偿内容：菲林*300 补偿对象：2026/10/21 06:00（UTC+8）前绳网等级≥4级的绳匠。 ※领取时间截至3.3版本结束前，3.3版本结束时间为2026/12/02 06:00（UTC+8），该版本共持续42天，请绳匠注意。 • 问题修复补偿 补偿内容：菲林*300 补偿对象：2026/10/21 06:"
	if n := utf8.RuneCountInString(summary); n != 255 {
		t.Fatalf("summary length = %d runes, want 255", n)
	}
	published := utcMoment(t, "2026-10-20T23:00:00Z")
	current := ParseArticle("3.3版本「重返天空的旅程」更新公告", summary, published, game(games.ZZZ))
	assertISO(t, "version", current.Version, "3.3")
	assertISO(t, "versionName", current.VersionName, "重返天空的旅程")
	assertISO(t, "updateStart", current.UpdateStart.Format(time.RFC3339), "2026-10-21T06:00:00+08:00")
	assertISO(t, "updateEnd", current.UpdateEnd.Format(time.RFC3339), "2026-10-21T11:00:00+08:00")
	assertISO(t, "versionEnd", current.VersionEnd.Format(time.RFC3339), "2026-12-02T06:00:00+08:00")

	plan := PlanNext(game(games.ZZZ), &current, utcMoment(t, "2026-10-20T23:00:00Z"), nil, nil, nil)
	assertISO(t, "version", plan.Version, "3.4")
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-12-02T06:00:00+08:00")
	assertISO(t, "updateEnd", plan.UpdateEnd.Format(time.RFC3339), "2026-12-02T11:00:00+08:00")
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-11-20T19:30:00+08:00")
	assertISO(t, "updateConfidence", plan.UpdateConfidence, "official")
}

func TestWuWa37Window(t *testing.T) {
	published := utcMoment(t, "2026-09-26T04:00:00Z")
	current := ParseArticle(
		"3.7版本内容说明",
		"✦更新维护时间：2026年9月30日04:00 ~ 2026年9月30日11:00（UTC+8）",
		published, game(games.WuWa))
	assertISO(t, "updateStart", current.UpdateStart.Format(time.RFC3339), "2026-09-30T04:00:00+08:00")
	assertISO(t, "updateEnd", current.UpdateEnd.Format(time.RFC3339), "2026-09-30T11:00:00+08:00")

	plan := PlanNext(game(games.WuWa), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil, nil, nil)
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-11-11T04:00:00+08:00")
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-30T19:30:00+08:00")
}

func TestEndfieldPhaseUpdate(t *testing.T) {
	published := utcMoment(t, "2026-10-12T04:00:00Z")
	current := ParseArticle(
		"「丹青渡」版本更新说明",
		"版本更新维护将于 2026/10/15 06:00 - 2026/10/15 12:00（UTC+8）进行。\n2026/11/12 12:00 将开启新一期内容更新。",
		published, game(games.Endfield))
	assertISO(t, "updateStart", current.UpdateStart.Format(time.RFC3339), "2026-10-15T06:00:00+08:00")
	assertISO(t, "phaseUpdate", current.PhaseUpdate.Format(time.RFC3339), "2026-11-12T06:00:00+08:00")

	plan := PlanNext(game(games.Endfield), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil, nil, nil)
	if plan == nil {
		t.Fatal("plan is nil")
	}
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-11-12T06:00:00+08:00")
	// A phase update is mid-version content, not a new version: the version is
	// not bumped and no preview stream is fabricated for it.
	if !plan.IsPhaseUpdate {
		t.Error("IsPhaseUpdate = false, want true")
	}
	if !plan.PreviewStart.IsZero() {
		t.Errorf("previewStart = %s, want none for a phase update", plan.PreviewStart)
	}
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "none")
	assertISO(t, "previewEvidence", plan.PreviewEvidence, "阶段内容更新，通常无前瞻直播")
}

func TestEndfieldPhaseExplicitPreviewStillWins(t *testing.T) {
	published := utcMoment(t, "2026-10-12T04:00:00Z")
	current := ParseArticle(
		"「丹青渡」版本更新说明",
		"版本更新维护将于 2026/10/15 06:00 - 2026/10/15 12:00（UTC+8）进行。\n2026/11/12 12:00 将开启新一期内容更新。",
		published, game(games.Endfield))
	plan := PlanNext(game(games.Endfield), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil,
		[]Announcement{{
			Text:    "《明日方舟：终末地》新一期内容前瞻直播定于11月7日19:30播出。",
			Pubdate: utcMoment(t, "2026-10-20T04:00:00Z"),
		}}, nil)
	if !plan.IsPhaseUpdate {
		t.Error("IsPhaseUpdate = false, want true")
	}
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-11-07T19:30:00+08:00")
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "official")
}

func TestSensitiveDateShiftsInferredPreview(t *testing.T) {
	// Update 2026-12-16: the Friday rule lands on 12-13, the National Memorial
	// Day — an inferred slot must be pushed to the next day.
	published := utcMoment(t, "2026-11-20T04:00:00Z")
	current := ParseArticle(
		"4.8版本更新说明",
		"■更新时间 2026/11/25 06:00\n4.8版本的持续时间为 2026/11/25 4.8版本更新后 - 2027/01/06 06:00。",
		published, game(games.HSR))
	plan := PlanNext(game(games.HSR), &current, utcMoment(t, "2026-11-20T16:00:00Z"), nil, nil, nil)
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2027-01-06T06:00:00+08:00")
	// Friday rule: 2026-12-25; 12-26 is inside the Memorial Day tolerance (12-13
	// is not hit here) — construct the collision directly instead.
	if _, blocked := SensitiveOn(cst(t, "2026-12-13T19:30:00+08:00")); !blocked {
		t.Fatal("12-13 should be sensitive")
	}
	shifted, _, moved := ShiftFromSensitive(cst(t, "2026-12-13T19:30:00+08:00"))
	if !moved {
		t.Fatal("expected a shift off 12-13")
	}
	// 12-14 sits inside the memorial-day tolerance (±1 day); the first free
	// day is 12-15.
	assertISO(t, "shifted", shifted.Format(time.RFC3339), "2026-12-15T19:30:00+08:00")
}

func TestSensitiveWindowDetection(t *testing.T) {
	cases := []struct {
		at      string
		blocked bool
	}{
		{"2026-03-05T19:30:00+08:00", true},  // 两会
		{"2026-10-01T19:30:00+08:00", true},  // 国庆周
		{"2026-09-18T19:30:00+08:00", true},  // 九一八
		{"2026-12-13T19:30:00+08:00", true},  // 国家公祭日
		{"2026-10-24T19:30:00+08:00", false}, // plain Saturday
		{"2026-05-15T19:30:00+08:00", false},
	}
	for _, c := range cases {
		if _, blocked := SensitiveOn(cst(t, c.at)); blocked != c.blocked {
			t.Errorf("SensitiveOn(%s) = %v, want %v", c.at, blocked, c.blocked)
		}
	}
}

func TestVendorStaggerThirtyMinutes(t *testing.T) {
	plans := map[games.GameID]*Plan{
		games.Genshin: {PreviewStart: cst(t, "2026-10-23T19:30:00+08:00"), PreviewConfidence: "inferred"},
		games.HSR:     {PreviewStart: cst(t, "2026-10-23T19:30:00+08:00"), PreviewConfidence: "inferred"},
	}
	StaggerVendorPreviews(plans)
	assertISO(t, "genshin keeps slot",
		plans[games.Genshin].PreviewStart.Format(time.RFC3339), "2026-10-23T19:30:00+08:00")
	assertISO(t, "hsr +30min",
		plans[games.HSR].PreviewStart.Format(time.RFC3339), "2026-10-23T20:00:00+08:00")
	if plans[games.HSR].PreviewEnd.IsZero() {
		t.Error("staggered preview end not set")
	}
}

func TestVendorStaggerOfficialUntouched(t *testing.T) {
	plans := map[games.GameID]*Plan{
		games.Genshin: {PreviewStart: cst(t, "2026-10-23T19:30:00+08:00"), PreviewConfidence: "official"},
		games.HSR:     {PreviewStart: cst(t, "2026-10-23T19:30:00+08:00"), PreviewConfidence: "inferred"},
	}
	StaggerVendorPreviews(plans)
	// Only one inferred plan participates; no collision to resolve.
	assertISO(t, "hsr stays",
		plans[games.HSR].PreviewStart.Format(time.RFC3339), "2026-10-23T19:30:00+08:00")
}

func TestCadenceBasedPreview(t *testing.T) {
	// Calibrated HSR-like cadence: previews lead the update by 12 days on a
	// Friday at 19:30 — for update 2026-11-25 (Wednesday) that is 11-13.
	published := utcMoment(t, "2026-10-20T04:00:00Z")
	current := ParseArticle(
		"4.7版本更新说明",
		"■更新时间 2026/11/11 06:00",
		published, game(games.HSR))
	cadence := &Cadence{
		SampleCount:        8,
		CycleDays:          42,
		UpdateWeekday:      3,
		PreviewLeadDays:    12,
		PreviewWeekday:     5,
		PreviewStartMinute: 19*60 + 30,
	}
	plan := PlanNext(game(games.HSR), &current, utcMoment(t, "2026-11-15T16:00:00Z"), nil, nil, cadence)
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-12-23T06:00:00+08:00")
	// Lead 12d from 12-23 = 12-11 (Friday): matches the modal weekday directly.
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-12-11T19:30:00+08:00")
	assertISO(t, "previewEvidence", plan.PreviewEvidence, "按历史前瞻节奏推算")
}

func TestNTE14Cadence(t *testing.T) {
	published := utcMoment(t, "2026-09-20T04:00:00Z")
	current := ParseArticle(
		"1.4版本更新公告",
		"9月24日06:00 – 11:00进行版本更新维护",
		published, game(games.NTE))
	assertISO(t, "updateStart", current.UpdateStart.Format(time.RFC3339), "2026-09-24T06:00:00+08:00")
	assertISO(t, "updateEnd", current.UpdateEnd.Format(time.RFC3339), "2026-09-24T11:00:00+08:00")

	plan := PlanNext(game(games.NTE), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil, nil, nil)
	assertISO(t, "updateStart", plan.UpdateStart.Format(time.RFC3339), "2026-11-05T06:00:00+08:00")
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-23T19:30:00+08:00")
}

func TestHoYoVideoPreviousFriday(t *testing.T) {
	// Genshin 7.1 video published 2026-09-12 21:25 CST (Saturday); the stream
	// was Friday 09-11.
	preview, ok := PreviewFromVideo(Video{
		BVID: "BV1xx", Title: "《原神》7.1版本前瞻特别节目",
		Pubdate: utcMoment(t, "2026-09-12T13:25:00Z"),
	}, game(games.Genshin), cst(t, "2026-09-23T06:00:00+08:00"))
	if !ok {
		t.Fatal("no preview")
	}
	assertISO(t, "preview", preview.Format(time.RFC3339), "2026-09-11T19:30:00+08:00")
}

func TestSameDayVideo(t *testing.T) {
	preview, ok := PreviewFromVideo(Video{
		BVID: "BV1yy", Title: "《绝区零》3.3版本前瞻特别节目",
		Pubdate: utcMoment(t, "2026-10-09T12:20:00Z"),
	}, game(games.ZZZ), cst(t, "2026-10-21T06:00:00+08:00"))
	if !ok {
		t.Fatal("no preview")
	}
	assertISO(t, "preview", preview.Format(time.RFC3339), "2026-10-09T19:30:00+08:00")
}

func TestAnnouncedPreviewWins(t *testing.T) {
	published := utcMoment(t, "2026-09-24T04:00:00Z")
	current := ParseArticle(
		"4.6版本「长夜未明」版本更新说明",
		"■更新时间 2026/09/28 06:00\n4.6版本的持续时间为 2026/09/28 4.6版本更新后 - 2026/11/11 06:00。",
		published, game(games.HSR))
	plan := PlanNext(game(games.HSR), &current, utcMoment(t, "2026-10-20T16:00:00Z"), nil,
		[]Announcement{{
			Text:    "4.7版本前瞻特别节目将于10月31日19:00播出，敬请期待。",
			Pubdate: utcMoment(t, "2026-10-20T04:00:00Z"),
		}}, nil)
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-31T19:00:00+08:00")
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "official")
}

func TestDateOnlyUpdateAnnouncement(t *testing.T) {
	update := ExtractNextUpdateAnnouncement(
		"4.7版本将于11月11日更新，敬请期待。",
		utcMoment(t, "2026-10-20T04:00:00Z"), game(games.HSR))
	if update == nil {
		t.Fatal("no update parsed")
	}
	assertISO(t, "version", update.Version, "4.7")
	assertISO(t, "at", update.At.Format(time.RFC3339), "2026-11-11T06:00:00+08:00")
}

// Merchandise presale / marketing-event dynamics must never read as version
// update dates. The recycling-collab one below rolled WuWa's plan a whole
// version ahead (3.8 → 3.9 via the expired-candidate roll-forward).
func TestPresaleNotUpdateAnnouncement(t *testing.T) {
	texts := []string{
		"「FUN小乖系列坐姿公仔」预售即将开启！将于10月14日10:00开启预售，敬请期待。",
		"鸣潮丨浮金波纹异形交通卡将于10月14日开启预售，更多详细情报请关注官方店铺。",
		"系列周边上架通知：10月14日起陆续上架，先到先得。",
		"闲置“频率”回收计划，即将开启！鸣潮×@爱回收返航新生 联动活动即将于2026年10月16日正式开启，活动预约已上线。",
	}
	for _, text := range texts {
		if update := ExtractNextUpdateAnnouncement(text,
			utcMoment(t, "2026-10-10T04:00:00Z"), game(games.WuWa)); update != nil {
			t.Fatalf("marketing text parsed as update %v: %q", update.At, text)
		}
	}
}

func TestShortDurationNotVersionEnd(t *testing.T) {
	if at, ok := ExtractVersionEnd("活动持续时间 2026/10/01 - 2026/10/03。"); ok {
		t.Fatalf("versionEnd = %s, want none", at)
	}
}

func TestTimeZoneIndependent(t *testing.T) {
	at, ok := ExtractPreviewAt("前瞻直播定档10月30日", utcMoment(t, "2026-10-20T04:00:00Z"), game(games.HSR))
	if !ok {
		t.Fatal("no preview")
	}
	assertISO(t, "preview", at.Format(time.RFC3339), "2026-10-30T19:30:00+08:00")
}

func TestVideoTitleExplicitTime(t *testing.T) {
	// Published Saturday; the title names Saturday 19:30. The HoYo
	// "previous Friday" rule alone would wrongly answer 10-30.
	preview, ok := PreviewFromVideo(Video{
		BVID:    "BV1zz",
		Title:   "《崩坏：星穹铁道》4.7版本前瞻特别节目将于10月31日19:30播出",
		Pubdate: utcMoment(t, "2026-10-31T12:30:00Z"),
	}, game(games.HSR), cst(t, "2026-11-11T06:00:00+08:00"))
	if !ok {
		t.Fatal("no preview")
	}
	assertISO(t, "preview", preview.Format(time.RFC3339), "2026-10-31T19:30:00+08:00")
}

func TestCharacterTeaserFiltered(t *testing.T) {
	published := utcMoment(t, "2026-09-24T04:00:00Z")
	current := ParseArticle(
		"4.6版本「长夜未明」版本更新说明",
		"■更新时间 2026/09/28 06:00\n4.6版本的持续时间为 2026/09/28 4.6版本更新后 - 2026/11/11 06:00。",
		published, game(games.HSR))
	plan := PlanNext(game(games.HSR), &current, utcMoment(t, "2026-10-20T16:00:00Z"),
		[]Video{{
			BVID: "BV1role", Title: "《崩坏：星穹铁道》4.7版本角色前瞻",
			Pubdate: utcMoment(t, "2026-10-20T04:00:00Z"),
		}}, nil, nil)
	// Version matches but the title is filtered; falls back to the Friday rule.
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-30T19:30:00+08:00")
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "inferred")
}

// --- calibration snapshot: the six-game live capture from the plugin docs
// (2026-10-21), verifying the ported rules reproduce the observed values. ---

func TestCalibrationSnapshot20261021(t *testing.T) {
	now := utcMoment(t, "2026-10-20T16:00:00Z")
	type row struct {
		id          games.GameID
		title, body string
		published   string
		wantVersion string
		wantUpdate  string
		wantPreview string
	}
	rows := []row{
		{"genshin", "7.1版本更新说明",
			"〓更新时间〓\n2026/09/23 06:00\n亲爱的旅行者，7.1版本更新即将开启。",
			"2026-09-21T04:00:00Z", "7.2", "2026-11-04T06:00:00+08:00", "2026-10-23T19:30:00+08:00"},
		{"hsr", "4.6版本「长夜未明」版本更新说明",
			"■更新时间 2026/09/28 06:00\n4.6版本的持续时间为 2026/09/28 4.6版本更新后 - 2026/11/11 06:00。",
			"2026-09-24T04:00:00Z", "4.7", "2026-11-11T06:00:00+08:00", "2026-10-30T19:30:00+08:00"},
		{"wuwa", "3.7版本内容说明",
			"✦更新维护时间：2026年9月30日04:00 ~ 2026年9月30日11:00（UTC+8）",
			"2026-09-26T04:00:00Z", "3.8", "2026-11-11T04:00:00+08:00", "2026-10-30T19:30:00+08:00"},
		{"nte", "1.4版本更新公告",
			"9月24日06:00 – 11:00进行版本更新维护",
			"2026-09-20T04:00:00Z", "1.5", "2026-11-05T06:00:00+08:00", "2026-10-23T19:30:00+08:00"},
	}
	for _, r := range rows {
		current := ParseArticle(r.title, r.body, utcMoment(t, r.published), game(r.id))
		plan := PlanNext(game(r.id), &current, now, nil, nil, nil)
		if plan == nil {
			t.Fatalf("%s: plan is nil", r.id)
		}
		assertISO(t, string(r.id)+" version", plan.Version, r.wantVersion)
		assertISO(t, string(r.id)+" update", plan.UpdateStart.Format(time.RFC3339), r.wantUpdate)
		assertISO(t, string(r.id)+" preview", plan.PreviewStart.Format(time.RFC3339), r.wantPreview)
	}
}

func TestBumpVersion(t *testing.T) {
	assertISO(t, "7.1→7.2", games.BumpVersion("7.1"), "7.2")
	assertISO(t, "4.9→4.10", games.BumpVersion("4.9"), "4.10")
	assertISO(t, "empty", games.BumpVersion(""), "")
}

func TestMinutes(t *testing.T) {
	if m, ok := games.Minutes("19:30"); !ok || m != 19*60+30 {
		t.Fatalf("Minutes(19:30) = %d, %v", m, ok)
	}
	if _, ok := games.Minutes("25:00"); ok {
		t.Fatal("25:00 should be invalid")
	}
}

func cst(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %s: %v", value, err)
	}
	return parsed.In(CST)
}

func assertISO(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func TestLiveRoomAnnouncementParse(t *testing.T) {
	// The live-room embedded system message, verbatim from
	// live.bilibili.com/21987615 on 2026-10-22: single "晚" before the hour.
	text := "《原神》7.2版本前瞻特别节目将于10月23日晚20:00开启！"
	pub := utcMoment(t, "2026-10-21T04:00:30Z")
	at, ok := ExtractPreviewAt(text, pub, game(games.Genshin))
	if !ok {
		t.Fatal("no preview parsed from live-room announcement")
	}
	assertISO(t, "preview", at.Format(time.RFC3339), "2026-10-23T20:00:00+08:00")

	published := utcMoment(t, "2026-09-21T04:00:00Z")
	current := ParseArticle("7.1版本更新说明",
		"〓更新时间〓\n2026/09/23 06:00", published, game(games.Genshin))
	plan := PlanNext(game(games.Genshin), &current, utcMoment(t, "2026-10-22T04:00:00Z"), nil,
		[]Announcement{{Text: text, Pubdate: utcMoment(t, "2026-10-21T04:00:30Z")}}, nil)
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-10-23T20:00:00+08:00")
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "official")
}

// The 9.1 patch notes publish on maintenance day and never state the window;
// every datetime in the text is an "更新后~X月X日HH:MM" event window that
// must not be mistaken for the maintenance time (live capture 2026-09-24).
func TestHi3PatchNotesAtPublish(t *testing.T) {
	published := utcMoment(t, "2026-09-24T03:02:00Z")
	facts := ParseArticle("「时序照新」9.1版本更新公告",
		"欢迎来到9.1版本——「时序照新」！ =====更新内容===== ★ 全新虚数属性S级角色——「时序之律者」 "+
			"9.1版本更新后，协同者希娜狄雅将解放新的能力。"+
			"9.1版本更新后~10月16日12:00期间开启「镌刻于黎明的誓约」服装补给。"+
			"9.1版本更新后~11月26日04:00期间，累计登录7日即可领取奖励。"+
			"10月1日12:00~10月22日12:00期间，登录游戏共计可领取庆典角色十连补给券×2。"+
			"9.1版本更新后~10月22日10:00期间完成面板任务可获取奖励。",
		published, game(games.Hi3))
	assertISO(t, "version", facts.Version, "9.1")
	assertISO(t, "versionName", facts.VersionName, "时序照新")
	assertISO(t, "updateStart", facts.UpdateStart.Format(time.RFC3339), "2026-09-24T06:00:00+08:00")
	assertISO(t, "updateEnd", facts.UpdateEnd.Format(time.RFC3339), "2026-09-24T11:00:00+08:00")
	if !facts.VersionEnd.IsZero() {
		t.Errorf("versionEnd = %s, want zero", facts.VersionEnd)
	}
}

// 星布谷地 announced its pre-launch stream (公测前瞻特别节目, 2026-11-01
// 19:00) before any version exists: the preview-only plan carries it.
func TestBuguPreviewOnlyPlan(t *testing.T) {
	now := utcMoment(t, "2026-10-23T05:00:00Z")
	teaser := Announcement{
		Text:    "✨《星布谷地》公测前瞻特别节目官宣！直播将于11月1日周日晚19:00正式开启，敬请期待！",
		Pubdate: utcMoment(t, "2026-10-23T03:30:00Z"),
	}
	plan := PreviewOnlyPlan(game(games.Bugu), now, []Announcement{teaser})
	if plan == nil {
		t.Fatal("preview-only plan is nil")
	}
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-11-01T19:00:00+08:00")
	assertISO(t, "previewEnd", plan.PreviewEnd.Format(time.RFC3339), "2026-11-01T21:00:00+08:00")
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "official")
	if !plan.UpdateStart.IsZero() {
		t.Errorf("updateStart = %s, want zero", plan.UpdateStart)
	}
	if plan2 := PreviewOnlyPlan(game(games.Bugu), now, nil); plan2 != nil {
		t.Errorf("plan without announcements = %v, want nil", plan2)
	}
}

// HI3 runs no per-version preview streams: no slot may be fabricated from
// weekday rules, but an explicit big-event teaser still calendars.
func TestHi3NoFabricatedPreview(t *testing.T) {
	published := utcMoment(t, "2026-09-24T03:02:00Z")
	current := ParseArticle("「时序照新」9.1版本更新公告",
		"欢迎来到9.1版本。9.1版本更新后~10月16日12:00期间开启补给。", published, game(games.Hi3))
	plan := PlanNext(game(games.Hi3), &current, utcMoment(t, "2026-10-23T04:00:00Z"), nil, nil, nil)
	if plan == nil {
		t.Fatal("plan is nil")
	}
	if !plan.PreviewStart.IsZero() {
		t.Errorf("previewStart = %s, want zero (no fabricated slot)", plan.PreviewStart)
	}
	assertISO(t, "previewConfidence", plan.PreviewConfidence, "none")

	stream := Announcement{
		Text:    "《崩坏3》十周年前瞻特别节目将于11月13日周五19:00开启！",
		Pubdate: utcMoment(t, "2026-10-23T04:00:00Z"),
	}
	plan = PlanNext(game(games.Hi3), &current, utcMoment(t, "2026-10-23T04:00:00Z"), nil,
		[]Announcement{stream}, nil)
	if plan.PreviewStart.IsZero() {
		t.Fatal("explicit teaser was dropped")
	}
	assertISO(t, "previewStart", plan.PreviewStart.Format(time.RFC3339), "2026-11-13T19:00:00+08:00")
}
