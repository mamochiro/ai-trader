package strategy

import (
	"fmt"
	"time"

	"github.com/mamochiro/ai-trader/internal/indicators"
	"github.com/mamochiro/ai-trader/internal/sentiment"
)

// Weights control how much each signal class contributes to the
// total score.
const (
	technicalWeight = 0.7
	sentimentWeight = 0.3
	buyThreshold    = 1.0
	sellThreshold   = -1.0
)

// Decision is the output of the strategy engine.
type Decision struct {
	Action     string    // BUY | SELL | HOLD
	TotalScore float64
	Reason     string
	Timestamp  time.Time
}

// Decide combines technical indicator signals with Claude sentiment
// into a weighted score and returns a trading decision.
//
// Scoring:
//
//	technicalScore = signals.Score             (already in -3..+3)
//	sentimentScore = sentiment.Score * 3 / 5   (map -5..+5 → -3..+3)
//	totalScore     = technical*0.7 + sentiment*0.3
//
// Thresholds:
//
//	totalScore >=  1.0 → BUY
//	totalScore <= -1.0 → SELL
//	otherwise          → HOLD
func Decide(signals indicators.SignalSummary, sent sentiment.SentimentResult) Decision {
	techScore := float64(signals.Score)
	sentScore := float64(sent.Score) * 3.0 / 5.0

	total := techScore*technicalWeight + sentScore*sentimentWeight

	d := Decision{
		Action:     string(ActionHold),
		TotalScore: total,
		Timestamp:  time.Now(),
	}

	switch {
	case total >= buyThreshold:
		d.Action = string(ActionBuy)
		d.Reason = fmt.Sprintf(
			"strong buy: tech=%.2f (w=%.0f%%) + sent=%.2f (w=%.0f%%) = %.2f",
			techScore, technicalWeight*100, sentScore, sentimentWeight*100, total,
		)
	case total <= sellThreshold:
		d.Action = string(ActionSell)
		d.Reason = fmt.Sprintf(
			"strong sell: tech=%.2f (w=%.0f%%) + sent=%.2f (w=%.0f%%) = %.2f",
			techScore, technicalWeight*100, sentScore, sentimentWeight*100, total,
		)
	default:
		d.Reason = fmt.Sprintf(
			"no clear edge: tech=%.2f (w=%.0f%%) + sent=%.2f (w=%.0f%%) = %.2f",
			techScore, technicalWeight*100, sentScore, sentimentWeight*100, total,
		)
	}

	return d
}
