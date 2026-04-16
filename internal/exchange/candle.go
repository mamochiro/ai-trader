package exchange

import "time"

// Candle is a normalized OHLCV candle produced by the feeder and
// consumed by downstream services. OpenTime is the candle's open
// instant; IsFinal is true once the candle has closed and its values
// are immutable.
type Candle struct {
	Symbol   string    `json:"symbol"`
	Interval string    `json:"interval"`
	OpenTime time.Time `json:"open_time"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	Volume   float64   `json:"volume"`
	IsFinal  bool      `json:"is_final"`
}
