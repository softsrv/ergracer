package render

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/softsrv/rowbot/internal/concept2"
)

func justRowResult(sportType string) concept2.Result {
	return concept2.Result{
		ID:            1,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          sportType,
		Time:          13477,
		TimeFormatted: "22:27.7",
		Pace:          1348,
		StrokeRate:    22,
		WorkoutType:   "JustRow",
		Calories:      320,
	}
}

// justRowResultWithHR is like justRowResult but optionally attaches a
// Result-level HeartRate summary, mirroring the "only show if actually
// reported" convention already established for the per-split HR column.
func justRowResultWithHR(sportType string, hr bool) concept2.Result {
	r := justRowResult(sportType)
	if hr {
		r.HeartRate = &concept2.HeartRateSummary{Average: 145}
	}
	return r
}

func splitsResult(hr bool) concept2.Result {
	splits := make([]concept2.Split, 5)
	for i := range splits {
		splits[i] = concept2.Split{
			Time:       6000 + int64(i)*10,
			Distance:   1000,
			StrokeRate: 20 + i,
			Calories:   80 + i,
		}
		if hr {
			splits[i].HeartRate = &concept2.HeartRateSummary{Average: 140 + i}
		}
	}
	return concept2.Result{
		ID:            2,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          "rower",
		Time:          30050,
		TimeFormatted: "50:05.0",
		StrokeRate:    20,
		WorkoutType:   "FixedDistanceSplits",
		Workout: concept2.Workout{
			Splits: splits,
		},
	}
}

func intervalsResult(hr bool) concept2.Result {
	intervals := make([]concept2.Split, 8)
	for i := range intervals {
		intervals[i] = concept2.Split{
			Time:       3000 + int64(i)*5,
			Distance:   750,
			StrokeRate: 24 + i,
			RestTime:   600,
			Calories:   60 + i,
		}
		if hr {
			intervals[i].HeartRate = &concept2.HeartRateSummary{Average: 150 + i}
		}
	}
	return concept2.Result{
		ID:            3,
		Date:          "2024-01-15 08:00:00",
		Distance:      6000,
		Type:          "skierg",
		Time:          24000,
		TimeFormatted: "42:15.0",
		StrokeRate:    26,
		WorkoutType:   "VariableInterval",
		Workout: concept2.Workout{
			Intervals: intervals,
		},
	}
}

func manySplitsResult() concept2.Result {
	splits := make([]concept2.Split, 40)
	for i := range splits {
		splits[i] = concept2.Split{
			Time:       500,
			Distance:   250,
			StrokeRate: 22,
			Calories:   20,
		}
	}
	return concept2.Result{
		ID:            4,
		Date:          "2024-01-15 08:00:00",
		Distance:      10000,
		Type:          "bike",
		Time:          60000,
		TimeFormatted: "100:00.0",
		StrokeRate:    22,
		WorkoutType:   "FixedDistanceSplits",
		Workout: concept2.Workout{
			Splits: splits,
		},
	}
}

func TestRenderResultPNG(t *testing.T) {
	tests := []struct {
		name        string
		result      concept2.Result
		expectPiece bool
	}{
		{"just row - no splits", justRowResult("rower"), false},
		{"just row - result-level calories, no heart rate", justRowResultWithHR("rower", false), false},
		{"just row - result-level calories and heart rate", justRowResultWithHR("rower", true), false},
		{"fixed distance splits", splitsResult(false), true},
		{"fixed distance splits with heart rate", splitsResult(true), true},
		{"interval workout", intervalsResult(false), true},
		{"interval workout with heart rate", intervalsResult(true), true},
		{"rower type", justRowResult("rower"), false},
		{"skierg type", justRowResult("skierg"), false},
		{"bike type", justRowResult("bike"), false},
		{"paddle type", justRowResult("paddle"), false},
		{"unknown type", justRowResult("jetski"), false},
		{"empty type", justRowResult(""), false},
		{"many splits truncated", manySplitsResult(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := RenderResultPNG(tt.result)
			if err != nil {
				t.Fatalf("RenderResultPNG: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("RenderResultPNG returned empty bytes")
			}

			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("png.Decode: %v", err)
			}

			bounds := img.Bounds()
			if bounds.Dx() != canvasWidth {
				t.Errorf("width = %d, want %d", bounds.Dx(), canvasWidth)
			}

			pieces := tt.result.Workout.Pieces()
			if tt.expectPiece && len(pieces) == 0 {
				t.Fatal("test setup error: expected pieces but found none")
			}

			// Every result renders the header band and hero stats, plus —
			// only when there are pieces — the splits/intervals table
			// (column-header row included). A result with no pieces (e.g.
			// JustRow) has no table at all, so its height excludes the
			// table dimensions entirely.
			rowCount := len(pieces)
			truncated := false
			if rowCount > maxTableRows {
				rowCount = maxTableRows
				truncated = true
			}
			expected := headerHeight + heroStatsHeight
			if len(pieces) > 0 {
				expected += tableTopPad + tableHeaderHeight + rowCount*tableRowHeight
				if truncated {
					expected += tableRowHeight
				}
				expected += tableBottomPad
			}
			expected += footerHeight
			if bounds.Dy() != expected {
				t.Errorf("height = %d, want %d (rowCount=%d truncated=%v)", bounds.Dy(), expected, rowCount, truncated)
			}
		})
	}
}

// TestStatsRowHeartRateColumnDoesNotAffectHeight verifies that a
// Result-level heart rate value is an extra column in the results table,
// not an extra row — so image height is identical whether or not HeartRate
// is present, even though width per column shrinks to fit the additional
// column.
func TestStatsRowHeartRateColumnDoesNotAffectHeight(t *testing.T) {
	without, err := RenderResultPNG(justRowResultWithHR("rower", false))
	if err != nil {
		t.Fatalf("RenderResultPNG (no HR): %v", err)
	}
	with, err := RenderResultPNG(justRowResultWithHR("rower", true))
	if err != nil {
		t.Fatalf("RenderResultPNG (with HR): %v", err)
	}

	imgWithout, err := png.Decode(bytes.NewReader(without))
	if err != nil {
		t.Fatalf("png.Decode (no HR): %v", err)
	}
	imgWith, err := png.Decode(bytes.NewReader(with))
	if err != nil {
		t.Fatalf("png.Decode (with HR): %v", err)
	}

	if imgWithout.Bounds().Dx() != imgWith.Bounds().Dx() {
		t.Errorf("width differs: without HR = %d, with HR = %d", imgWithout.Bounds().Dx(), imgWith.Bounds().Dx())
	}
	if imgWithout.Bounds().Dy() != imgWith.Bounds().Dy() {
		t.Errorf("height differs: without HR = %d, with HR = %d", imgWithout.Bounds().Dy(), imgWith.Bounds().Dy())
	}
}

func TestSportLabelAndColor(t *testing.T) {
	tests := []struct {
		sportType string
		wantLabel string
		wantColor int
	}{
		// Rowing family.
		{"rower", "Rowing", 0x4A90D9},
		{"dynamic", "Dynamic Rower", 0x4A90D9},
		{"slides", "Slides", 0x4A90D9},
		{"water", "Water", 0x4A90D9},
		// Ski family.
		{"skierg", "SkiErg", 0x5B9BD5},
		{"snow", "Snow", 0x5B9BD5},
		{"rollerski", "Roller Ski", 0x5B9BD5},
		// Bike — note the real API key is "bike", not "bikeerg".
		{"bike", "BikeErg", 0xED7D31},
		// Paddle and MultiErg get their own accent colors.
		{"paddle", "Paddle", 0x2FA89A},
		{"multierg", "MultiErg", 0x8B5FBF},
		// Fallbacks.
		{"", "Result", 0x4A90D9},
		{"jetski", "Jetski", 0x4A90D9},
	}

	for _, tt := range tests {
		t.Run(tt.sportType, func(t *testing.T) {
			label, color := sportLabelAndColor(tt.sportType)
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
			if color != tt.wantColor {
				t.Errorf("color = %#x, want %#x", color, tt.wantColor)
			}
		})
	}
}
