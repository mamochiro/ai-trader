package indicators

import (
	"errors"
	"math"
	"testing"
)

// Wilder's canonical 16-point RSI example (from "New Concepts in
// Technical Trading Systems", 1978). The expected RSI at the final
// index is ~66.32.
var wilderCloses = []float64{
	44.3389, 44.0902, 44.1497, 43.6124, 44.3278, 44.8264, 45.0955,
	45.4245, 45.8433, 46.0826, 45.8931, 46.0328, 45.6140, 46.2820,
	46.2820, 46.0028,
}

func TestRSI_WilderReference(t *testing.T) {
	got, err := RSI(wilderCloses, 14)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = 66.3192
	if math.Abs(got-want) > 0.01 {
		t.Errorf("RSI = %.4f, want %.4f (±0.01)", got, want)
	}
}

func TestRSI_InsufficientData(t *testing.T) {
	cases := [][]float64{
		nil,
		{},
		{1, 2, 3}, // only 3 points, period 14 needs 15
	}
	for i, closes := range cases {
		if _, err := RSI(closes, 14); !errors.Is(err, ErrInsufficientData) {
			t.Errorf("case %d: expected ErrInsufficientData, got %v", i, err)
		}
	}
}

func TestRSI_AllEqualIs100(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = 100
	}
	got, err := RSI(closes, 14)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 100 {
		t.Errorf("flat series RSI = %v, want 100", got)
	}
}

func TestRSI_MonotonicUpIs100(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = float64(i + 1)
	}
	got, err := RSI(closes, 14)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 100 {
		t.Errorf("rising series RSI = %v, want 100", got)
	}
}

func TestRSI_MonotonicDownIs0(t *testing.T) {
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = float64(20 - i)
	}
	got, err := RSI(closes, 14)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("falling series RSI = %v, want 0", got)
	}
}
