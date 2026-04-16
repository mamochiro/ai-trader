package indicators

// MACD default periods.
const (
	MACDFastPeriod   = 12
	MACDSlowPeriod   = 26
	MACDSignalPeriod = 9
)

// MACDResult holds the latest MACD line, signal line, and histogram.
// Histogram > 0 is bullish (MACD above signal), < 0 is bearish.
type MACDResult struct {
	MACD      float64
	Signal    float64
	Histogram float64
}

// MACD computes the latest MACD(fast, slow, signal) point. It
// requires at least slow+signal closes to produce a well-formed
// signal line. closes must be ordered oldest → newest.
func MACD(closes []float64, fast, slow, signal int) (MACDResult, error) {
	if fast <= 0 || slow <= 0 || signal <= 0 || fast >= slow {
		return MACDResult{}, ErrInsufficientData
	}
	if len(closes) < slow+signal {
		return MACDResult{}, ErrInsufficientData
	}

	fastEMA := emaSeries(closes, fast)
	slowEMA := emaSeries(closes, slow)

	// MACD line over the whole series.
	macdLine := make([]float64, len(closes))
	for i := range closes {
		macdLine[i] = fastEMA[i] - slowEMA[i]
	}

	// Signal line is EMA(signal) of the MACD line.
	signalLine := emaSeries(macdLine, signal)

	last := len(closes) - 1
	m := macdLine[last]
	s := signalLine[last]
	return MACDResult{MACD: m, Signal: s, Histogram: m - s}, nil
}

// emaSeries returns the exponential moving average over values with
// the given period, aligned with values (same length). The initial
// value seeds the series; successive values are smoothed with
// k = 2/(period+1).
func emaSeries(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 || period <= 0 {
		return out
	}
	k := 2.0 / float64(period+1)
	out[0] = values[0]
	for i := 1; i < len(values); i++ {
		out[i] = values[i]*k + out[i-1]*(1-k)
	}
	return out
}
