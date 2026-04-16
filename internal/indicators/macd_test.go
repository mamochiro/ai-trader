package indicators

import (
	"errors"
	"testing"
)

// makeSeries returns n values produced by f(i).
func makeSeries(n int, f func(i int) float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func TestMACD_InsufficientData(t *testing.T) {
	// 26 + 9 = 35 required; 34 should fail.
	closes := makeSeries(34, func(i int) float64 { return float64(i) })
	if _, err := MACD(closes, 12, 26, 9); !errors.Is(err, ErrInsufficientData) {
		t.Errorf("expected ErrInsufficientData, got %v", err)
	}
}

func TestMACD_FlatSeriesIsZero(t *testing.T) {
	closes := makeSeries(50, func(int) float64 { return 100 })
	got, err := MACD(closes, 12, 26, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MACD != 0 || got.Signal != 0 || got.Histogram != 0 {
		t.Errorf("flat series MACD = %+v, want zeros", got)
	}
}

func TestMACD_RisingSeriesIsPositive(t *testing.T) {
	// A long linear rise: the fast EMA sits above the slow EMA, so the
	// MACD line is positive. After enough smoothing it exceeds the
	// signal line, so the histogram also ends positive.
	closes := makeSeries(200, func(i int) float64 { return float64(i) })
	got, err := MACD(closes, 12, 26, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MACD <= 0 {
		t.Errorf("rising series MACD = %v, want > 0", got.MACD)
	}
	if got.Signal <= 0 {
		t.Errorf("rising series Signal = %v, want > 0", got.Signal)
	}
}

func TestMACD_FallingSeriesIsNegative(t *testing.T) {
	closes := makeSeries(200, func(i int) float64 { return float64(200 - i) })
	got, err := MACD(closes, 12, 26, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MACD >= 0 {
		t.Errorf("falling series MACD = %v, want < 0", got.MACD)
	}
	if got.Signal >= 0 {
		t.Errorf("falling series Signal = %v, want < 0", got.Signal)
	}
}

func TestMACD_HistogramIsDifference(t *testing.T) {
	closes := makeSeries(100, func(i int) float64 {
		return 100 + float64(i%10)
	})
	got, err := MACD(closes, 12, 26, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Histogram != got.MACD-got.Signal {
		t.Errorf("histogram = %v, want MACD - Signal = %v", got.Histogram, got.MACD-got.Signal)
	}
}
