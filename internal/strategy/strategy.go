// Package strategy fuses technical indicator signals with Claude
// sentiment into a single BUY/SELL/HOLD decision.
package strategy

// Action is the trading decision emitted by the strategy engine.
type Action string

const (
	ActionBuy  Action = "BUY"
	ActionSell Action = "SELL"
	ActionHold Action = "HOLD"
)
