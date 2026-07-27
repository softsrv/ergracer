//go:build ignore

// Command cmd_dump is a throwaway local-iteration harness for visually
// checking internal/render's PNG output without touching Discord or the DB.
//
// Usage (run from the repo root):
//
//	go run internal/render/cmd_dump/main.go [fixture]
//
// where [fixture] is one of: justrow, splits, splits-hr, intervals,
// intervals-hr, many, skierg, bike, unknown (default: justrow).
//
// Writes the rendered PNG to tmp/result.png.
package main

import (
	"fmt"
	"os"

	"github.com/softsrv/rowbot/internal/concept2"
	"github.com/softsrv/rowbot/internal/render"
)

func main() {
	name := "justrow"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}

	result, ok := fixtures[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown fixture %q; available: %s\n", name, availableNames())
		os.Exit(1)
	}

	data, err := render.RenderResultPNG(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll("tmp", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir tmp: %v\n", err)
		os.Exit(1)
	}

	const outPath = "tmp/result.png"
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}

	fmt.Printf("wrote %s (%d bytes) using fixture %q\n", outPath, len(data), name)
}

func availableNames() string {
	names := make([]string, 0, len(fixtures))
	for k := range fixtures {
		names = append(names, k)
	}
	return fmt.Sprint(names)
}

var fixtures = map[string]concept2.Result{
	"justrow": {
		ID:            1001,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          "rower",
		Time:          13477,
		TimeFormatted: "22:27.7",
		Pace:          1348,
		StrokeRate:    22,
		WorkoutType:   "JustRow",
		Calories:      312,
	},
	"splits": {
		ID:            1002,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          "rower",
		Time:          30050,
		TimeFormatted: "50:05.0",
		StrokeRate:    20,
		WorkoutType:   "FixedDistanceSplits",
		Workout: concept2.Workout{
			Splits: makeSplits(5, false),
		},
	},
	"splits-hr": {
		ID:            1003,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          "rower",
		Time:          30050,
		TimeFormatted: "50:05.0",
		Pace:          3005,
		StrokeRate:    20,
		WorkoutType:   "FixedDistanceSplits",
		Calories:      410,
		HeartRate:     &concept2.HeartRateSummary{Average: 158},
		Workout: concept2.Workout{
			Splits: makeSplits(5, true),
		},
	},
	"intervals": {
		ID:            1004,
		Date:          "2024-01-15 08:00:00",
		Distance:      6000,
		Type:          "skierg",
		Time:          24000,
		TimeFormatted: "42:15.0",
		StrokeRate:    26,
		WorkoutType:   "VariableInterval",
		Workout: concept2.Workout{
			Intervals: makeSplits(8, false),
		},
	},
	"intervals-hr": {
		ID:            1005,
		Date:          "2024-01-15 08:00:00",
		Distance:      6000,
		Type:          "skierg",
		Time:          24000,
		TimeFormatted: "42:15.0",
		StrokeRate:    26,
		WorkoutType:   "VariableInterval",
		Workout: concept2.Workout{
			Intervals: makeSplits(8, true),
		},
	},
	"many": {
		ID:            1006,
		Date:          "2024-01-15 08:00:00",
		Distance:      10000,
		Type:          "bike",
		Time:          60000,
		TimeFormatted: "100:00.0",
		StrokeRate:    22,
		WorkoutType:   "FixedDistanceSplits",
		Workout: concept2.Workout{
			Splits: makeSplits(40, false),
		},
	},
	"skierg": {
		ID:            1007,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          "skierg",
		Time:          15000,
		TimeFormatted: "25:00.0",
		StrokeRate:    24,
		WorkoutType:   "JustRow",
	},
	"bike": {
		ID:            1008,
		Date:          "2024-01-15 08:00:00",
		Distance:      20000,
		Type:          "bike",
		Time:          36000,
		TimeFormatted: "60:00.0",
		StrokeRate:    90,
		WorkoutType:   "JustRow",
	},
	// "unknown" exercises the fallback default branch of
	// sportLabelAndColor. It deliberately uses a type that is NOT in
	// Concept2's documented enum — "paddle" used to serve this purpose but
	// is now a real documented type with its own branding, so it moved to
	// its own path in RenderResultPNG's normal flow instead.
	"unknown": {
		ID:            1009,
		Date:          "2024-01-15 08:00:00",
		Distance:      5000,
		Type:          "jetski",
		Time:          20000,
		TimeFormatted: "33:20.0",
		StrokeRate:    30,
		WorkoutType:   "JustRow",
	},
}

func makeSplits(n int, hr bool) []concept2.Split {
	splits := make([]concept2.Split, n)
	for i := range splits {
		splits[i] = concept2.Split{
			Time:       6000 + int64(i)*15,
			Distance:   1000,
			StrokeRate: 20 + i%5,
			Calories:   80 + i,
		}
		if hr {
			splits[i].HeartRate = &concept2.HeartRateSummary{Average: 140 + i}
		}
	}
	return splits
}
