// Package indicators implements the technical indicators consumed by
// the signal engine: RSI, MACD, and Bollinger Bands.
//
// Every indicator takes a []float64 of closing prices ordered oldest
// → newest and returns the latest scalar value plus an error. If the
// series is shorter than the indicator's minimum lookback, the
// indicator returns ErrInsufficientData and a zero value.
package indicators

import "errors"

// ErrInsufficientData is returned when an indicator cannot be
// computed because the input series is shorter than its required
// lookback window.
var ErrInsufficientData = errors.New("indicators: insufficient data")

// RSIPeriod is the conventional Wilder RSI lookback.
const RSIPeriod = 14

// RSI computes the latest Relative Strength Index value (0..100)
// using Wilder's smoothing. closes must contain at least period+1
// points. The result follows the usual convention: RSI < 30 is
// oversold (buy bias) and RSI > 70 is overbought (sell bias).
func RSI(closes []float64, period int) (float64, error) {
	if period <= 0 {
		return 0, ErrInsufficientData
	}
	if len(closes) < period+1 {
		return 0, ErrInsufficientData
	}

	// Seed: simple averages of gains and losses over the first `period` changes.
	var gainSum, lossSum float64
	for i := 1; i <= period; i++ {
		change := closes[i] - closes[i-1]
		if change >= 0 {
			gainSum += change
		} else {
			lossSum -= change
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	// Wilder smoothing for every remaining change.
	for i := period + 1; i < len(closes); i++ {
		change := closes[i] - closes[i-1]
		gain, loss := 0.0, 0.0
		if change >= 0 {
			gain = change
		} else {
			loss = -change
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}

	if avgLoss == 0 {
		// No losses in the lookback → RSI is conventionally 100.
		return 100, nil
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs)), nil
}
