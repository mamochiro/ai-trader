package indicators

import "github.com/mamochiro/ai-trader/internal/exchange"

// Direction labels emitted by Analyze.
const (
	DirectionBuy  = "BUY"
	DirectionSell = "SELL"
	DirectionHold = "HOLD"
)

// SignalSummary fuses the three indicators into a single view the
// signal engine can act on.
//
// Score is the net vote from the three indicators, each contributing
// -1 (sell), 0 (neutral), or +1 (buy), so Score ∈ [-3, +3]. The
// direction rule is:
//
//	Score > 0  → BUY
//	Score < 0  → SELL
//	Score == 0 → HOLD
//
// This matches the spec ("negative=sell, positive=buy") literally;
// downstream can apply a stricter threshold if it wants conservative
// majority agreement.
type SignalSummary struct {
	RSI       float64
	MACD      MACDResult
	BB        BollingerResult
	Score     int
	Direction string
}

// Analyze extracts closing prices from the candle series, runs the
// three indicators with their default parameters, and returns the
// fused summary. When the series is too short for any indicator,
// that indicator contributes 0 to the score; if every indicator is
// unavailable the result is a zero-valued summary with Direction=HOLD.
func Analyze(candles []exchange.Candle) SignalSummary {
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}

	summary := SignalSummary{Direction: DirectionHold}

	if rsi, err := RSI(closes, RSIPeriod); err == nil {
		summary.RSI = rsi
		switch {
		case rsi < 30:
			summary.Score++
		case rsi > 70:
			summary.Score--
		}
	}

	if macd, err := MACD(closes, MACDFastPeriod, MACDSlowPeriod, MACDSignalPeriod); err == nil {
		summary.MACD = macd
		switch {
		case macd.Histogram > 0:
			summary.Score++
		case macd.Histogram < 0:
			summary.Score--
		}
	}

	if bb, err := Bollinger(closes, BollingerPeriod, BollingerStdDev); err == nil {
		summary.BB = bb
		if len(closes) > 0 {
			price := closes[len(closes)-1]
			switch {
			case price < bb.Lower:
				summary.Score++
			case price > bb.Upper:
				summary.Score--
			}
		}
	}

	switch {
	case summary.Score > 0:
		summary.Direction = DirectionBuy
	case summary.Score < 0:
		summary.Direction = DirectionSell
	default:
		summary.Direction = DirectionHold
	}
	return summary
}
