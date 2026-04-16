package exchange

import "strings"

// Common quote assets used to split a trading pair like "BTCUSDT"
// into base ("BTC") and quote ("USDT").
var quoteAssets = []string{"USDT", "BUSD", "USDC", "BTC", "ETH", "BNB"}

// BaseAsset extracts the base asset from a symbol.
// e.g. "BTCUSDT" → "BTC", "ETHUSDT" → "ETH", "SOLUSDT" → "SOL".
func BaseAsset(symbol string) string {
	upper := strings.ToUpper(symbol)
	for _, q := range quoteAssets {
		if strings.HasSuffix(upper, q) && len(upper) > len(q) {
			return upper[:len(upper)-len(q)]
		}
	}
	return upper
}

// ParseSymbols splits a comma-separated list of symbols.
// Returns the default if the input is empty.
func ParseSymbols(csv string, defaultSymbols []string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return defaultSymbols
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(strings.ToUpper(p))
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return defaultSymbols
	}
	return out
}
