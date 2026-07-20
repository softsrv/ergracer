package concept2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Split holds data for one split or interval within a workout.
type Split struct {
	Time       int64   `json:"time"`        // tenths of a second (work time)
	Distance   float64 `json:"distance"`    // metres
	StrokeRate int     `json:"stroke_rate"` // spm
	RestTime   int64   `json:"rest_time"`   // tenths of a second (intervals only)
}

// Pace returns the pace per 500m in tenths of a second, calculated from time
// and distance. Returns 0 if distance is zero.
func (s Split) Pace() int64 {
	if s.Distance <= 0 {
		return 0
	}
	return int64(float64(s.Time) * 500 / s.Distance)
}

// Workout holds the split or interval breakdown of a result.
// The API uses "splits" for fixed-distance/time workouts and "intervals" for
// variable-interval workouts. Only one will be populated.
type Workout struct {
	Splits    []Split `json:"splits"`
	Intervals []Split `json:"intervals"`
}

// Pieces returns whichever of Intervals or Splits is populated, preferring
// Intervals. Returns nil if neither is present.
func (w Workout) Pieces() []Split {
	if len(w.Intervals) > 0 {
		return w.Intervals
	}
	return w.Splits
}

// IsIntervals reports whether the workout used intervals (vs. splits).
func (w Workout) IsIntervals() bool {
	return len(w.Intervals) > 0
}

// Result holds the data for a single Concept2 Logbook workout result.
type Result struct {
	ID            int64   `json:"id"`
	UserID        int64   `json:"user_id"`
	Date          string  `json:"date"`           // "2024-01-15 HH:MM:SS" or "2024-01-15"
	Distance      float64 `json:"distance"`       // metres
	Type          string  `json:"type"`           // "rower", "skierg", "bikeerg"
	Time          int64   `json:"time"`           // tenths of a second (work time only)
	TimeFormatted string  `json:"time_formatted"` // pre-formatted by API; includes rest for intervals
	Pace          int64   `json:"pace"`           // tenths of a second per 500m (0 on list results)
	StrokeRate    int     `json:"stroke_rate"`    // spm
	WeightClass   string  `json:"weight_class"`   // "H" or "L"
	Verified      bool    `json:"verified"`
	Ranked        bool    `json:"ranked"`
	Comments      string  `json:"comments"`
	WorkoutType   string  `json:"workout_type"` // "JustRow", "FixedDistanceSplits", etc.
	Workout       Workout `json:"workout"`
}

type resultResponse struct {
	Data Result `json:"data"`
}

type resultListResponse struct {
	Data []Result `json:"data"`
}

// ListResults fetches the most recent Concept2 Logbook results for the
// authenticated user. limit controls the page size (max varies by API).
func ListResults(ctx context.Context, client *http.Client, apiBase, accessToken string, limit int) ([]Result, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf("%s/api/users/me/results?per_page=%d", apiBase, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("concept2 list results: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("concept2 list results: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("concept2 list results: status %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}

	var wrapper resultListResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("concept2 list results: decode: %w", err)
	}
	return wrapper.Data, nil
}

// GetResult fetches a single Concept2 Logbook result by ID. It calls
// GET {apiBase}/api/users/me/results/{resultID} with the supplied Bearer token.
// The API wraps the result in a {"data": {...}} envelope.
func GetResult(ctx context.Context, client *http.Client, apiBase, accessToken string, resultID int64) (Result, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf("%s/api/users/me/results/%d", apiBase, resultID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, fmt.Errorf("concept2 get result: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("concept2 get result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Result{}, fmt.Errorf("concept2 get result: status %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}

	var wrapper resultResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&wrapper); err != nil {
		return Result{}, fmt.Errorf("concept2 get result: decode: %w", err)
	}
	return wrapper.Data, nil
}
