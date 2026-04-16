package indicators

import (
	"errors"
	"math"
	"testing"
)

func TestBollinger_InsufficientData(t *testing.T) {
	closes := []float64{1, 2, 3}
	if _, err := Bollinger(closes, 20, 2); !errors.Is(err, ErrInsufficientData) {
		t.Errorf("expected ErrInsufficientData, got %v", err)
	}
}

// Known-value test: closes = [1,2,3,4,5], period=5, mult=2.
//
//	mean     = 3
//	variance = ((1-3)² + (2-3)² + (3-3)² + (4-3)² + (5-3)²) / 5 = 10/5 = 2
//	stddev   = √2
//	upper    = 3 + 2√2  ≈ 5.828427
//	lower    = 3 - 2√2  ≈ 0.171573
func TestBollinger_KnownValues(t *testing.T) {
	closes := []float64{1, 2, 3, 4, 5}
	got, err := Bollinger(closes, 5, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantMiddle := 3.0
	wantUpper := 3 + 2*math.Sqrt(2)
	wantLower := 3 - 2*math.Sqrt(2)

	if math.Abs(got.Middle-wantMiddle) > 1e-9 {
		t.Errorf("Middle = %v, want %v", got.Middle, wantMiddle)
	}
	if math.Abs(got.Upper-wantUpper) > 1e-9 {
		t.Errorf("Upper = %v, want %v", got.Upper, wantUpper)
	}
	if math.Abs(got.Lower-wantLower) > 1e-9 {
		t.Errorf("Lower = %v, want %v", got.Lower, wantLower)
	}
}

func TestBollinger_FlatSeriesCollapses(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = 42
	}
	got, err := Bollinger(closes, 20, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Upper != 42 || got.Middle != 42 || got.Lower != 42 {
		t.Errorf("flat series bands = %+v, want all 42", got)
	}
}

func TestBollinger_UsesLastWindow(t *testing.T) {
	// Earlier noise must not leak into the last-window stats.
	closes := append(
		[]float64{100, 200, 50, 300}, // older prices — should be ignored
		1, 2, 3, 4, 5,                // last 5 window
	)
	got, err := Bollinger(closes, 5, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(got.Middle-3) > 1e-9 {
		t.Errorf("Middle = %v, want 3 (from tail window)", got.Middle)
	}
}
