package concept2

import (
	"math"
	"testing"
)

func TestResultAveragePace(t *testing.T) {
	tests := []struct {
		name     string
		distance float64
		time     int64
		want     int64
	}{
		{
			name:     "normal case",
			distance: 5000,
			time:     13477,
			want:     1347, // int64(13477 * 500 / 5000) = int64(1347.7)
		},
		{
			name:     "zero distance",
			distance: 0,
			time:     13477,
			want:     0,
		},
		{
			name:     "negative distance",
			distance: -100,
			time:     13477,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Result{Distance: tt.distance, Time: tt.time}
			if got := r.AveragePace(); got != tt.want {
				t.Errorf("AveragePace() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestResultWatts validates against Concept2's own worked example
// (https://www.concept2.com/training/watts-calculator): a 2:05/500m split
// (125 seconds/500m, a 0.25 sec/m pace) yields (2.80/0.25³) = 179.2 watts.
func TestResultWatts(t *testing.T) {
	tests := []struct {
		name     string
		distance float64
		time     int64
		want     float64
	}{
		{
			name:     "concept2 worked example: 2:05/500m",
			distance: 500,
			time:     1250, // tenths of a second = 125.0 seconds
			want:     179.2,
		},
		{
			name:     "zero distance",
			distance: 0,
			time:     1250,
			want:     0,
		},
		{
			name:     "zero time",
			distance: 500,
			time:     0,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Result{Distance: tt.distance, Time: tt.time}
			if got := r.Watts(); math.Abs(got-tt.want) > 0.1 {
				t.Errorf("Watts() = %f, want %f", got, tt.want)
			}
		})
	}
}

// TestSplitWatts mirrors TestResultWatts for Split.Watts(), using the same
// Concept2 worked example.
func TestSplitWatts(t *testing.T) {
	tests := []struct {
		name     string
		distance float64
		time     int64
		want     float64
	}{
		{
			name:     "concept2 worked example: 2:05/500m",
			distance: 500,
			time:     1250, // tenths of a second = 125.0 seconds
			want:     179.2,
		},
		{
			name:     "zero distance",
			distance: 0,
			time:     1250,
			want:     0,
		},
		{
			name:     "zero time",
			distance: 500,
			time:     0,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Split{Distance: tt.distance, Time: tt.time}
			if got := s.Watts(); math.Abs(got-tt.want) > 0.1 {
				t.Errorf("Watts() = %f, want %f", got, tt.want)
			}
		})
	}
}
