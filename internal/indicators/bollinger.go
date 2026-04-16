package indicators

import "math"

// Bollinger default parameters.
const (
	BollingerPeriod = 20
	BollingerStdDev = 2.0
)

// BollingerResult holds the latest upper, middle (SMA), and lower
// band values. Price above Upper is often read as overbought; price
// below Lower as oversold.
type BollingerResult struct {
	Upper  float64
	Middle float64
	Lower  float64
}

// Bollinger computes the latest Bollinger Bands over the final
// `period` closes using a simple moving average and a standard
// deviation multiplier. closes must contain at least `period` values.
//
// We compute variance over the same window as the mean (population
// standard deviation), matching the original Bollinger formulation.
func Bollinger(closes []float64, period int, mult float64) (BollingerResult, error) {
	if period <= 0 || mult < 0 {
		return BollingerResult{}, ErrInsufficientData
	}
	if len(closes) < period {
		return BollingerResult{}, ErrInsufficientData
	}

	window := closes[len(closes)-period:]

	var sum float64
	for _, v := range window {
		sum += v
	}
	mean := sum / float64(period)

	var variance float64
	for _, v := range window {
		diff := v - mean
		variance += diff * diff
	}
	sd := math.Sqrt(variance / float64(period))

	return BollingerResult{
		Upper:  mean + mult*sd,
		Middle: mean,
		Lower:  mean - mult*sd,
	}, nil
}
