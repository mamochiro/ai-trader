package strategy

import (
	"math"
	"testing"

	"github.com/mamochiro/ai-trader/internal/indicators"
	"github.com/mamochiro/ai-trader/internal/sentiment"
)

func sig(score int) indicators.SignalSummary {
	return indicators.SignalSummary{Score: score}
}

func sent(score int) sentiment.SentimentResult {
	return sentiment.SentimentResult{Score: score}
}

func TestDecide_StrongBuy(t *testing.T) {
	// tech=3 → 3*0.7=2.1, sent=5 → 3.0*0.3=0.9 → total=3.0
	d := Decide(sig(3), sent(5))
	if d.Action != "BUY" {
		t.Errorf("Action = %q, want BUY (total=%.2f)", d.Action, d.TotalScore)
	}
	if math.Abs(d.TotalScore-3.0) > 1e-9 {
		t.Errorf("TotalScore = %.4f, want 3.0", d.TotalScore)
	}
}

func TestDecide_StrongSell(t *testing.T) {
	// tech=-3 → -3*0.7=-2.1, sent=-5 → -3.0*0.3=-0.9 → total=-3.0
	d := Decide(sig(-3), sent(-5))
	if d.Action != "SELL" {
		t.Errorf("Action = %q, want SELL (total=%.2f)", d.Action, d.TotalScore)
	}
	if math.Abs(d.TotalScore+3.0) > 1e-9 {
		t.Errorf("TotalScore = %.4f, want -3.0", d.TotalScore)
	}
}

func TestDecide_Hold_Neutral(t *testing.T) {
	d := Decide(sig(0), sent(0))
	if d.Action != "HOLD" {
		t.Errorf("Action = %q, want HOLD", d.Action)
	}
	if d.TotalScore != 0 {
		t.Errorf("TotalScore = %v, want 0", d.TotalScore)
	}
}

func TestDecide_BuyWith2Indicators(t *testing.T) {
	// tech=2 + sent=0 → 2*0.7 = 1.4 → BUY (>=1.0)
	d := Decide(sig(2), sent(0))
	if d.Action != "BUY" {
		t.Errorf("Action = %q, want BUY (total=%.2f)", d.Action, d.TotalScore)
	}
}

func TestDecide_HoldWith1Indicator(t *testing.T) {
	// tech=1 + sent=0 → 1*0.7 = 0.7 → HOLD (<1.0)
	d := Decide(sig(1), sent(0))
	if d.Action != "HOLD" {
		t.Errorf("Action = %q, want HOLD (total=%.2f)", d.Action, d.TotalScore)
	}
}

func TestDecide_SellWith2Indicators(t *testing.T) {
	// tech=-2 + sent=0 → -2*0.7 = -1.4 → SELL (<=-1.0)
	d := Decide(sig(-2), sent(0))
	if d.Action != "SELL" {
		t.Errorf("Action = %q, want SELL (total=%.2f)", d.Action, d.TotalScore)
	}
}

func TestDecide_TechOverridesWeakSentiment(t *testing.T) {
	// tech=2 + sent=-1 → 2*0.7 + (-0.6)*0.3 = 1.4 - 0.18 = 1.22 → BUY
	d := Decide(sig(2), sent(-1))
	if d.Action != "BUY" {
		t.Errorf("Action = %q, want BUY (total=%.2f)", d.Action, d.TotalScore)
	}
}

func TestDecide_TechBullishSentBearish(t *testing.T) {
	// tech=1 + sent=-3 → 1*0.7 + (-1.8)*0.3 = 0.7 - 0.54 = 0.16 → HOLD
	d := Decide(sig(1), sent(-3))
	if d.Action != "HOLD" {
		t.Errorf("conflicting signals should HOLD, got %q (total=%.2f)", d.Action, d.TotalScore)
	}
}

func TestDecide_ReasonNonEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    Decision
	}{
		{"buy", Decide(sig(3), sent(5))},
		{"sell", Decide(sig(-3), sent(-5))},
		{"hold", Decide(sig(0), sent(0))},
	} {
		if tc.d.Reason == "" {
			t.Errorf("%s: Reason is empty", tc.name)
		}
		if tc.d.Timestamp.IsZero() {
			t.Errorf("%s: Timestamp is zero", tc.name)
		}
	}
}

func TestDecide_SentimentScaling(t *testing.T) {
	// sent=5 scales to 3.0, with tech=0:
	// total = 0 + 3.0*0.3 = 0.9 → HOLD (<1.0)
	d := Decide(sig(0), sent(5))
	wantTotal := 0.9
	if math.Abs(d.TotalScore-wantTotal) > 1e-9 {
		t.Errorf("TotalScore = %.10f, want %.10f", d.TotalScore, wantTotal)
	}
	if d.Action != "HOLD" {
		t.Errorf("sentiment alone should not trigger BUY, got %q", d.Action)
	}
}
