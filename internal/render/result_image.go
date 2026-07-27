// Package render generates presentation assets (currently PNG images) for
// Concept2 rowing/skiing/biking results. It has no DB or service
// dependencies — callers (e.g. internal/app) own delivery.
package render

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fogleman/gg"

	"github.com/softsrv/rowbot/internal/concept2"
)

// canvasWidth is the fixed pixel width of every rendered result image.
// Height is computed dynamically from content (header, hero stats, and —
// if present — the splits/intervals table row count, plus the footer).
const canvasWidth = 750

const (
	marginX = 32

	headerHeight = 100

	// heroStatsHeight is the height of the band of big pull-out numbers
	// (time, avg pace, avg watts, calories) drawn between the header and
	// the splits table.
	heroStatsHeight = 84

	// tableTopPad is the breathing room between the hero stats band and
	// the table's column-header row.
	tableTopPad       = 20
	tableHeaderHeight = 34
	tableRowHeight    = 30
	tableBottomPad    = 24

	// footerHeight is the height of the closing brand/date strip at the
	// bottom of the card.
	footerHeight = 40

	// cardRadius is the corner radius applied to the whole image, giving
	// it a card silhouette (the four corners render fully transparent)
	// rather than a hard rectangle.
	cardRadius = 16

	// maxTableRows caps the number of split/interval rows drawn before
	// falling back to a "+N more" line, keeping canvas height bounded
	// regardless of workout length.
	maxTableRows = 22
)

var (
	colorWhite      = rgb{255, 255, 255}
	colorLightText  = rgb{221, 232, 250}
	colorDarkText   = rgb{30, 34, 40}
	colorMutedText  = rgb{100, 108, 120}
	colorBg         = rgb{255, 255, 255}
	colorTableLine  = rgb{225, 229, 235}
	colorRowShade   = rgb{244, 246, 249}
	colorHeaderFill = rgb{237, 240, 245}
	colorHeroBg     = rgb{248, 249, 251}

	// colorPaceSlow/colorPaceFast are the endpoints of the gradient used to
	// color each split row's pace "chip" (see drawPaceChip): a slow split
	// is a muted neutral, a fast split is a saturated green.
	colorPaceSlow = rgb{219, 223, 229}
	colorPaceFast = rgb{99, 153, 34}
)

type rgb struct {
	r, g, b int
}

// RenderResultPNG draws a Concept2 workout result as a PNG "card": a colored
// header band (sport label and workout type), a row of hero stats pulled
// out of the summary (time, avg pace, avg watts, calories), a table whose
// remaining rows — if the workout has splits or intervals — are the
// per-piece breakdown with a relative-pace chip behind each PACE value, and
// a closing brand/date footer. The whole card has rounded corners
// (transparent PNG corners) and a thin border. It returns the PNG-encoded
// bytes.
func RenderResultPNG(result concept2.Result) ([]byte, error) {
	title, color := sportLabelAndColor(result.Type)
	subtitle := result.WorkoutType
	if subtitle != "" {
		subtitle = humanizeWorkoutType(subtitle) + " - " + workoutSubtitleMetric(result)
	}

	pieces := result.Workout.Pieces()

	// The summary row is a synthetic Split built from the result's own
	// totals/averages. Its Pace()/Watts() use the exact same formula as
	// Split's, computed from these same Time/Distance fields, so this
	// renders identically to calling Result.AveragePace()/Watts() directly
	// — letting the summary row (and the hero stats band) reuse every
	// value function rather than needing a parallel set for
	// concept2.Result.
	summary := concept2.Split{
		Time:       result.Time,
		Distance:   result.Distance,
		StrokeRate: result.StrokeRate,
		Calories:   result.Calories,
		HeartRate:  result.HeartRate,
	}

	hasHR := result.HeartRate != nil
	for _, p := range pieces {
		if p.HeartRate != nil {
			hasHR = true
			break
		}
	}

	rowCount := len(pieces)
	truncated := false
	if rowCount > maxTableRows {
		rowCount = maxTableRows
		truncated = true
	}

	// header + hero stats + breathing room + column-header row + per-piece
	// rows (+ "+N more" line if truncated) + bottom padding + footer.
	height := headerHeight + heroStatsHeight + tableTopPad + tableHeaderHeight + rowCount*tableRowHeight
	if truncated {
		height += tableRowHeight
	}
	height += tableBottomPad + footerHeight

	dc := gg.NewContext(canvasWidth, height)

	// Clip everything that follows to a rounded rect. The image is
	// initialized fully transparent, so the four corners outside that
	// rect are left untouched — the PNG renders as a rounded card however
	// it's composited downstream, rather than a hard white rectangle.
	dc.DrawRoundedRectangle(0, 0, float64(canvasWidth), float64(height), cardRadius)
	dc.Clip()

	setColor(dc, colorBg)
	dc.DrawRectangle(0, 0, float64(canvasWidth), float64(height))
	dc.Fill()

	drawHeader(dc, title, subtitle, color)
	drawHeroStats(dc, float64(headerHeight), []heroStat{
		{"Time", formatDuration(summary.Time)},
		{"Avg pace", paceString(summary)},
		{"Avg watts", wattsString(summary)},
		{"Calories", strconv.Itoa(summary.Calories)},
	})

	tableTop := float64(headerHeight+heroStatsHeight) + tableTopPad
	drawSplitsTable(dc, pieces, hasHR, rowCount, truncated, tableTop)

	drawFooter(dc, float64(height-footerHeight), formatResultDate(result.Date))

	// The border is drawn outside the clip so it isn't itself clipped away
	// at the very edge.
	dc.ResetClip()
	setColor(dc, colorTableLine)
	dc.DrawRoundedRectangle(0.75, 0.75, float64(canvasWidth)-1.5, float64(height)-1.5, cardRadius-1)
	dc.SetLineWidth(1.5)
	dc.Stroke()

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("render result png: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// workoutSubtitleMetric returns the single headline metric appended after
// the workout type in the header subtitle: total time for time-based
// workout types (those whose WorkoutType contains "Time", e.g.
// "FixedTimeSplits"), otherwise total distance — which also covers types
// that don't fit Concept2's Fixed<Distance|Time><Splits|Interval> naming
// convention (e.g. "JustRow", "VariableInterval"), where distance is the
// more meaningful default.
func workoutSubtitleMetric(result concept2.Result) string {
	if strings.Contains(result.WorkoutType, "Time") {
		return formatDuration(result.Time)
	}
	return formatDistance(result.Distance)
}

// humanizeWorkoutType inserts a space before each interior capital letter
// of a Concept2 WorkoutType value (PascalCase, e.g. "FixedDistanceSplits"),
// so it displays as "Fixed Distance Splits". Values with no interior
// capital letters (e.g. "unknown", one of Concept2's documented
// workout_type values) pass through unchanged — there's nothing to split.
func humanizeWorkoutType(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// maxHeaderTextWidth is the pixel budget for the header band's title/
// subtitle text: full width minus the left and right margins.
const maxHeaderTextWidth = canvasWidth - 2*marginX

// drawHeader draws the colored header band: a bolded title (the sport
// label) with a lighter subtitle line below it (the workout type).
func drawHeader(dc *gg.Context, title, subtitle string, color int) {
	setColorHex(dc, color)
	dc.DrawRectangle(0, 0, float64(canvasWidth), headerHeight)
	dc.Fill()

	textX := float64(marginX)

	titleFace := loadFace(fontBold, 26)
	dc.SetFontFace(titleFace)
	setColor(dc, colorWhite)
	displayTitle := title
	if w, _ := dc.MeasureString(displayTitle); w > maxHeaderTextWidth {
		// Text is too wide for the default size — try a smaller face
		// before resorting to truncation.
		titleFace = loadFace(fontBold, 22)
		dc.SetFontFace(titleFace)
		if w, _ := dc.MeasureString(displayTitle); w > maxHeaderTextWidth {
			displayTitle = truncateToWidth(dc, displayTitle, maxHeaderTextWidth)
		}
	}

	if subtitle == "" {
		dc.DrawStringAnchored(displayTitle, textX, float64(headerHeight)/2, 0, 0.5)
		return
	}

	dc.DrawStringAnchored(displayTitle, textX, float64(headerHeight)*0.38, 0, 0.5)

	subFace := loadFace(fontRegular, 13)
	dc.SetFontFace(subFace)
	setColor(dc, colorLightText)
	displaySubtitle := subtitle
	if w, _ := dc.MeasureString(displaySubtitle); w > maxHeaderTextWidth {
		displaySubtitle = truncateToWidth(dc, displaySubtitle, maxHeaderTextWidth)
	}
	dc.DrawStringAnchored(displaySubtitle, textX, float64(headerHeight)*0.68, 0, 0.5)
}

// truncateToWidth shortens s (using the font face currently set on dc) to
// fit within maxWidth pixels, appending an ellipsis. s is assumed to be
// ASCII (true for the sport labels and Concept2 WorkoutType values this is
// used with), so byte-wise slicing is safe.
func truncateToWidth(dc *gg.Context, s string, maxWidth float64) string {
	const ellipsis = "…"
	for i := len(s); i > 0; i-- {
		candidate := s[:i] + ellipsis
		if w, _ := dc.MeasureString(candidate); w <= maxWidth {
			return candidate
		}
	}
	return ellipsis
}

// heroStat is one label/value pair drawn in the hero stats band.
type heroStat struct {
	label string
	value string
}

// drawHeroStats draws a row of evenly-spaced stat cards (muted caption
// label above, large bold value below), separated by hairline dividers, on
// a light band starting at top. This pulls the headline numbers (time, avg
// pace, avg watts, calories) out of the table below so they're readable at
// a glance instead of buried in a bolded row of six columns.
func drawHeroStats(dc *gg.Context, top float64, stats []heroStat) {
	if len(stats) == 0 {
		return
	}

	contentWidth := float64(canvasWidth - 2*marginX)
	cardWidth := contentWidth / float64(len(stats))

	setColor(dc, colorHeroBg)
	dc.DrawRectangle(float64(marginX), top, contentWidth, heroStatsHeight)
	dc.Fill()

	labelFace := loadFace(fontBold, 11)
	valueFace := loadFace(fontBold, 22)

	for i, s := range stats {
		cardX := float64(marginX) + float64(i)*cardWidth
		if i > 0 {
			setColor(dc, colorTableLine)
			dc.DrawLine(cardX, top+14, cardX, top+heroStatsHeight-14)
			dc.SetLineWidth(1)
			dc.Stroke()
		}
		centerX := cardX + cardWidth/2

		dc.SetFontFace(labelFace)
		setColor(dc, colorMutedText)
		dc.DrawStringAnchored(strings.ToUpper(s.label), centerX, top+28, 0.5, 0.5)

		dc.SetFontFace(valueFace)
		setColor(dc, colorDarkText)
		dc.DrawStringAnchored(s.value, centerX, top+56, 0.5, 0.5)
	}

	setColor(dc, colorTableLine)
	dc.DrawLine(float64(marginX), top+heroStatsHeight, float64(canvasWidth-marginX), top+heroStatsHeight)
	dc.SetLineWidth(1)
	dc.Stroke()
}

// paceString formats a split's pace the way the table and hero stats band
// both want it: "m:ss.t" per 500m, or an em dash when the split has no
// distance to compute a pace from.
func paceString(p concept2.Split) string {
	if pace := p.Pace(); pace > 0 {
		return formatDuration(pace)
	}
	return "—"
}

// wattsString formats a split's watts, or an em dash when unavailable.
func wattsString(p concept2.Split) string {
	if watts := p.Watts(); watts > 0 {
		return strconv.Itoa(int(math.Round(watts)))
	}
	return "—"
}

// splitsColumn describes one column of the results table: its header label,
// a relative width weight (used to divide the fixed content width), and a
// per-row value function. Per-piece rows are concept2.Split values (see
// RenderResultPNG), so one column set renders all of them.
type splitsColumn struct {
	label  string
	weight float64
	value  func(p concept2.Split) string
}

// drawSplitsTable draws the column-header row (starting at headerTop), then
// up to rowCount per-piece rows (plus a "+N more" line if truncated).
// pieces may be empty (e.g. a JustRow result with no split/interval
// breakdown), in which case the table is just the header row. Each
// per-piece row's PACE cell gets a filled "chip" behind the value, sized
// and colored by how fast that split was relative to the others in pieces.
func drawSplitsTable(dc *gg.Context, pieces []concept2.Split, hasHR bool, rowCount int, truncated bool, headerTop float64) {
	columns := []splitsColumn{
		{"DIST", 1.3, func(p concept2.Split) string { return formatDistance(p.Distance) }},
		{"TIME", 1.15, func(p concept2.Split) string { return formatDuration(p.Time) }},
		{"PACE", 1.15, paceString},
		{"WATTS", 0.9, wattsString},
		{"CAL", 0.9, func(p concept2.Split) string { return strconv.Itoa(p.Calories) }},
		{"S/M", 0.9, func(p concept2.Split) string { return strconv.Itoa(p.StrokeRate) }},
	}
	if hasHR {
		columns = append(columns, splitsColumn{"HR", 0.9, func(p concept2.Split) string {
			if p.HeartRate != nil {
				return strconv.Itoa(p.HeartRate.Average)
			}
			return "—"
		}})
	}

	paceColIdx := -1
	for i, c := range columns {
		if c.label == "PACE" {
			paceColIdx = i
			break
		}
	}

	contentWidth := float64(canvasWidth - 2*marginX)
	totalWeight := 0.0
	for _, c := range columns {
		totalWeight += c.weight
	}

	colWidths := make([]float64, len(columns))
	colX := make([]float64, len(columns))
	x := float64(marginX)
	for i, c := range columns {
		colWidths[i] = c.weight / totalWeight * contentWidth
		colX[i] = x
		x += colWidths[i]
	}

	setColor(dc, colorHeaderFill)
	dc.DrawRectangle(float64(marginX), headerTop, contentWidth, tableHeaderHeight)
	dc.Fill()

	headerFace := loadFace(fontMonoBold, 13)
	dc.SetFontFace(headerFace)
	setColor(dc, colorMutedText)
	headerCenterY := headerTop + tableHeaderHeight/2
	for i, c := range columns {
		cx := colX[i] + colWidths[i] - 8
		dc.DrawStringAnchored(c.label, cx, headerCenterY, 1, 0.5)
	}

	rowFace := loadFace(fontMono, 14)
	dc.SetFontFace(rowFace)

	rowsTop := headerTop + tableHeaderHeight

	// Pace range across the drawn rows, used to size/color each row's pace
	// chip (see drawPaceChip). minPace tracks the fastest (smallest, since
	// pace is time-per-500m) split; maxPace the slowest.
	var minPace, maxPace int64
	if paceColIdx >= 0 {
		for r := 0; r < rowCount; r++ {
			pace := pieces[r].Pace()
			if pace <= 0 {
				continue
			}
			if minPace == 0 || pace < minPace {
				minPace = pace
			}
			if pace > maxPace {
				maxPace = pace
			}
		}
	}

	for r := 0; r < rowCount; r++ {
		p := pieces[r]
		rowY := rowsTop + float64(r)*tableRowHeight

		if r%2 == 1 {
			setColor(dc, colorRowShade)
			dc.DrawRectangle(float64(marginX), rowY, contentWidth, tableRowHeight)
			dc.Fill()
		}

		pace := p.Pace()
		frac := 0.0
		if paceColIdx >= 0 && pace > 0 {
			if maxPace > minPace {
				frac = float64(maxPace-pace) / float64(maxPace-minPace)
			} else {
				frac = 1.0
			}
			drawPaceChip(dc, colX[paceColIdx], colWidths[paceColIdx], rowY, frac)
		}

		dc.SetFontFace(rowFace)
		rowCenterY := rowY + tableRowHeight/2
		for i, c := range columns {
			val := c.value(p)
			cx := colX[i] + colWidths[i] - 8
			// The fastest splits get a saturated pace chip dark enough
			// that the default dark text loses contrast — flip to white
			// for those cells only.
			textColor := colorDarkText
			if i == paceColIdx && pace > 0 && frac > 0.72 {
				textColor = colorWhite
			}
			setColor(dc, textColor)
			dc.DrawStringAnchored(val, cx, rowCenterY, 1, 0.5)
		}
	}

	if truncated {
		moreY := rowsTop + float64(rowCount)*tableRowHeight
		setColor(dc, colorMutedText)
		moreFace := loadFace(fontRegular, 13)
		dc.SetFontFace(moreFace)
		more := fmt.Sprintf("+ %d more", len(pieces)-rowCount)
		dc.DrawStringAnchored(more, float64(marginX), moreY+tableRowHeight/2, 0, 0.5)
	}
}

// drawPaceChip draws a filled, rounded bar behind a row's PACE value,
// right-aligned within the PACE column like the value text itself. frac is
// 0 (slowest split in the table) to 1 (fastest); it drives both the chip's
// width (a faster split's bar reaches further left) and its color (from
// colorPaceSlow to colorPaceFast), so a glance down the column shows the
// pacing trend without reading every split time.
func drawPaceChip(dc *gg.Context, colStartX, colWidth, rowY, frac float64) {
	const minChipFrac = 0.22
	chipFrac := minChipFrac + frac*(1-minChipFrac)
	chipW := colWidth*chipFrac - 8
	if chipW < 10 {
		chipW = 10
	}
	chipH := tableRowHeight - 10.0
	chipX := colStartX + colWidth - 8 - chipW
	chipY := rowY + 5

	setColor(dc, lerpColor(colorPaceSlow, colorPaceFast, frac))
	dc.DrawRoundedRectangle(chipX, chipY, chipW, chipH, 4)
	dc.Fill()
}

// lerpColor linearly interpolates between two colors; t is clamped to
// [0, 1].
func lerpColor(a, b rgb, t float64) rgb {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return rgb{
		r: a.r + int(float64(b.r-a.r)*t),
		g: a.g + int(float64(b.g-a.g)*t),
		b: a.b + int(float64(b.b-a.b)*t),
	}
}

// drawFooter draws the closing brand/date strip: a hairline divider, the
// "RowBot" brand mark on the left, and the formatted result date (if any)
// on the right.
func drawFooter(dc *gg.Context, top float64, dateLabel string) {
	setColor(dc, colorTableLine)
	dc.DrawLine(float64(marginX), top, float64(canvasWidth-marginX), top)
	dc.SetLineWidth(1)
	dc.Stroke()

	face := loadFace(fontRegular, 12)
	dc.SetFontFace(face)
	setColor(dc, colorMutedText)
	centerY := top + footerHeight/2
	dc.DrawStringAnchored("RowBot", float64(marginX), centerY, 0, 0.5)
	if dateLabel != "" {
		dc.DrawStringAnchored(dateLabel, float64(canvasWidth-marginX), centerY, 1, 0.5)
	}
}

// formatResultDate parses a Concept2 result's Date field (observed as
// "2006-01-02 15:04:05", but RFC3339 and a bare date are also accepted
// defensively) into a short human-readable form, e.g. "Jul 24, 2026".
// Returns "" if raw is empty or doesn't match any known layout, so callers
// can simply skip drawing it.
func formatResultDate(raw string) string {
	if raw == "" {
		return ""
	}
	layouts := []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("Jan 2, 2006")
		}
	}
	return ""
}

func setColor(dc *gg.Context, c rgb) {
	dc.SetRGB255(c.r, c.g, c.b)
}

// setColorHex sets the drawing color from a 0xRRGGBB packed int, as
// returned by sportLabelAndColor.
func setColorHex(dc *gg.Context, hex int) {
	r := (hex >> 16) & 0xFF
	g := (hex >> 8) & 0xFF
	b := hex & 0xFF
	dc.SetRGB255(r, g, b)
}

// sportLabelAndColor returns the short display label and brand color (as a
// packed 0xRRGGBB int) for a given Concept2 activity type. It covers all 10
// values Concept2's Add Result API documents for concept2.Result.Type
// (rower, dynamic, slides, paddle, water, snow, rollerski, bike, multierg)
// plus the empty string, grouped into a small brand palette by activity
// family so the rendered card reads as one coherent design rather than ten
// unrelated hues:
//
//   - Rowing family (default blue 0x4A90D9): "rower", "dynamic", "slides",
//     "water". These are rowing-erg variants or outdoor-rowing log entries —
//     Concept2's own docs group them together too, e.g. the weight_class
//     field is documented as "Required if type is rower, dynamic or
//     slides".
//   - Ski family (light blue 0x5B9BD5): "skierg", "snow", "rollerski" —
//     indoor SkiErg plus its outdoor skiing analogues.
//   - Bike (orange 0xED7D31): "bike". Note the API's documented value is
//     "bike", not "bikeerg" — an earlier version of this switch checked for
//     "bikeerg", which never matched a real API response and silently fell
//     through to the generic default branch instead of showing the BikeErg
//     branding. The display label is still "BikeErg" even though the match
//     key has no "erg" suffix.
//   - Paddle (teal 0x2FA89A) and MultiErg (purple 0x8B5FBF) are distinct
//     enough product categories to get their own accent colors rather than
//     being folded into another family.
//
// Any other non-empty, undocumented type falls back to the type itself with
// its first letter uppercased, using the default rowing blue; an empty type
// falls back to the generic label "Result".
func sportLabelAndColor(sportType string) (string, int) {
	const (
		defaultColor = 0x4A90D9
		skiColor     = 0x5B9BD5
		bikeColor    = 0xED7D31
		paddleColor  = 0x2FA89A
		multiColor   = 0x8B5FBF
	)

	switch sportType {
	case "rower":
		return "Rowing", defaultColor
	case "dynamic":
		return "Dynamic Rower", defaultColor
	case "slides":
		return "Slides", defaultColor
	case "water":
		return "Water", defaultColor
	case "skierg":
		return "SkiErg", skiColor
	case "snow":
		return "Snow", skiColor
	case "rollerski":
		return "Roller Ski", skiColor
	case "bike":
		return "BikeErg", bikeColor
	case "paddle":
		return "Paddle", paddleColor
	case "multierg":
		return "MultiErg", multiColor
	case "":
		return "Result", defaultColor
	default:
		return titleCase(sportType), defaultColor
	}
}

// titleCase uppercases the first byte of s and lowercases the rest. It's
// ASCII-only, which is sufficient for the Concept2 activity type strings.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// formatDuration converts tenths of a second to "m:ss.t" format.
// For example, 13477 → "22:27.7".
func formatDuration(tenths int64) string {
	if tenths <= 0 {
		return "0:00.0"
	}
	t := tenths % 10
	totalSeconds := tenths / 10
	seconds := totalSeconds % 60
	minutes := totalSeconds / 60
	return fmt.Sprintf("%d:%02d.%d", minutes, seconds, t)
}

// formatDistance formats a distance in metres as a human-readable string.
// Values under 10 km are shown as whole metres with a thousands separator;
// values 10 km and over are shown in kilometres with one decimal place.
func formatDistance(metres float64) string {
	if metres >= 10000 {
		return fmt.Sprintf("%.1f km", metres/1000)
	}
	// Format with comma thousands separator for values >= 1000.
	m := int64(metres)
	if m >= 1000 {
		thousands := m / 1000
		remainder := m % 1000
		return fmt.Sprintf("%d,%03d m", thousands, remainder)
	}
	return fmt.Sprintf("%d m", m)
}
