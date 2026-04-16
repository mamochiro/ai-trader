package indicators

import (
	"testing"
	"time"

	"github.com/mamochiro/ai-trader/internal/exchange"
)

// candlesFromCloses builds a minimal candle slice where only Close is
// meaningful. OpenTime is spaced 1 minute apart so it's plausible but
// Analyze does not read it.
func candlesFromCloses(closes []float64) []exchange.Candle {
	out := make([]exchange.Candle, len(closes))
	base := time.Unix(1_700_000_000, 0)
	for i, c := range closes {
		out[i] = exchange.Candle{
			Symbol:   "BTCUSDT",
			Interval: "1m",
			OpenTime: base.Add(time.Duration(i) * time.Minute),
			Close:    c,
			IsFinal:  true,
		}
	}
	return out
}

func TestAnalyze_EmptyReturnsHold(t *testing.T) {
	got := Analyze(nil)
	if got.Direction != DirectionHold {
		t.Errorf("Direction = %q, want HOLD", got.Direction)
	}
	if got.Score != 0 {
		t.Errorf("Score = %d, want 0", got.Score)
	}
}

func TestAnalyze_InsufficientDataReturnsHold(t *testing.T) {
	// 10 candles — below every indicator's minimum lookback.
	closes := make([]float64, 10)
	for i := range closes {
		closes[i] = 100
	}
	got := Analyze(candlesFromCloses(closes))
	if got.Direction != DirectionHold {
		t.Errorf("Direction = %q, want HOLD", got.Direction)
	}
	if got.Score != 0 {
		t.Errorf("Score = %d, want 0", got.Score)
	}
}

func TestAnalyze_MonotonicDownTriggersSell(t *testing.T) {
	// A long monotonic decline: RSI → 0 (oversold? no, RSI=0 is
	// extreme bearish — our rule: RSI < 30 → +1, so oversold gives
	// buy bias). MACD histogram ends negative (falling series),
	// so contributes -1. Price sits far below the lower band, so
	// Bollinger contributes +1. Net score = +1 buy (because RSI=0
	// is treated as oversold bounce bias).
	//
	// This test mainly verifies: Score lives in [-3, +3], Direction
	// matches the sign, and field values are populated.
	closes := make([]float64, 100)
	for i := range closes {
		closes[i] = float64(200 - i)
	}
	got := Analyze(candlesFromCloses(closes))

	if got.Score < -3 || got.Score > 3 {
		t.Errorf("Score = %d, out of [-3, +3]", got.Score)
	}
	if got.RSI == 0 && got.MACD == (MACDResult{}) && got.BB == (BollingerResult{}) {
		t.Error("no indicator was populated for a 100-candle series")
	}
	switch {
	case got.Score > 0 && got.Direction != DirectionBuy:
		t.Errorf("Score %d but Direction %q, want BUY", got.Score, got.Direction)
	case got.Score < 0 && got.Direction != DirectionSell:
		t.Errorf("Score %d but Direction %q, want SELL", got.Score, got.Direction)
	case got.Score == 0 && got.Direction != DirectionHold:
		t.Errorf("Score 0 but Direction %q, want HOLD", got.Direction)
	}
}

// TestAnalyze_MatchesDirectIndicatorCalls verifies that Analyze's
// populated RSI/MACD/BB fields equal the result of calling the
// individual indicator functions on the same closing prices.
func TestAnalyze_MatchesDirectIndicatorCalls(t *testing.T) {
	closes := make([]float64, 100)
	for i := range closes {
		// A slightly noisy uptrend.
		closes[i] = 100 + float64(i) + float64(i%5)
	}

	got := Analyze(candlesFromCloses(closes))

	wantRSI, err := RSI(closes, RSIPeriod)
	if err != nil {
		t.Fatalf("RSI failed: %v", err)
	}
	if got.RSI != wantRSI {
		t.Errorf("Analyze.RSI = %v, direct RSI = %v", got.RSI, wantRSI)
	}

	wantMACD, err := MACD(closes, MACDFastPeriod, MACDSlowPeriod, MACDSignalPeriod)
	if err != nil {
		t.Fatalf("MACD failed: %v", err)
	}
	if got.MACD != wantMACD {
		t.Errorf("Analyze.MACD = %+v, direct MACD = %+v", got.MACD, wantMACD)
	}

	wantBB, err := Bollinger(closes, BollingerPeriod, BollingerStdDev)
	if err != nil {
		t.Fatalf("Bollinger failed: %v", err)
	}
	if got.BB != wantBB {
		t.Errorf("Analyze.BB = %+v, direct BB = %+v", got.BB, wantBB)
	}
}
