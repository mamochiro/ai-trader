package risk

import (
	"math"
	"testing"
)

func TestNewRiskManager_Defaults(t *testing.T) {
	rm := NewRiskManager(10_000)
	if rm.PortfolioValue != 10_000 {
		t.Errorf("PortfolioValue = %v, want 10000", rm.PortfolioValue)
	}
	if rm.MaxRiskPercent != 0.03 {
		t.Errorf("MaxRiskPercent = %v, want 0.03", rm.MaxRiskPercent)
	}
	if rm.StopLossPct != 0.025 {
		t.Errorf("StopLossPct = %v, want 0.025", rm.StopLossPct)
	}
	if rm.TakeProfitPct != 0.05 {
		t.Errorf("TakeProfitPct = %v, want 0.05", rm.TakeProfitPct)
	}
	if rm.DailyLossLimit != 0.08 {
		t.Errorf("DailyLossLimit = %v, want 0.08", rm.DailyLossLimit)
	}
	if rm.MaxPositions != 1 {
		t.Errorf("MaxPositions = %v, want 1", rm.MaxPositions)
	}
}

// --- CanTrade -------------------------------------------------------

func TestCanTrade_NormalConditions(t *testing.T) {
	rm := NewRiskManager(10_000)
	if !rm.CanTrade() {
		t.Error("fresh manager should allow trading")
	}
}

func TestCanTrade_DailyLossBreached(t *testing.T) {
	rm := NewRiskManager(10_000) // limit = 10000 * 0.08 = 800
	rm.TodayLoss = 800           // exactly at the limit
	if rm.CanTrade() {
		t.Error("should block trading when daily loss equals limit")
	}
	rm.TodayLoss = 900
	if rm.CanTrade() {
		t.Error("should block trading when daily loss exceeds limit")
	}
}

func TestCanTrade_DailyLossBelowLimit(t *testing.T) {
	rm := NewRiskManager(10_000)
	rm.TodayLoss = 799
	if !rm.CanTrade() {
		t.Error("should allow trading when daily loss is below limit")
	}
}

func TestCanTrade_MaxPositionsReached(t *testing.T) {
	rm := NewRiskManager(10_000)
	rm.OpenPositions = 1 // max is 1
	if rm.CanTrade() {
		t.Error("should block trading at max open positions")
	}
}

func TestCanTrade_BothRulesChecked(t *testing.T) {
	rm := NewRiskManager(10_000)
	rm.TodayLoss = 900
	rm.OpenPositions = 1
	if rm.CanTrade() {
		t.Error("should block when both limits breached")
	}
}

// --- CalculatePositionSize ------------------------------------------

func TestCalculatePositionSize_KnownValues(t *testing.T) {
	// Portfolio=10000, risk=3%, stop=2.5%, entry=50000
	// riskBudget  = 10000 * 0.03  = 300
	// stopDist    = 50000 * 0.025 = 1250
	// posSize     = 300 / 1250    = 0.24
	rm := NewRiskManager(10_000)
	got := rm.CalculatePositionSize(50_000)
	want := 300.0 / 1250.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("PositionSize = %v, want %v", got, want)
	}
}

func TestCalculatePositionSize_LossEqualsRiskBudget(t *testing.T) {
	// The defining invariant: if the position hits the stop, the
	// loss must equal PortfolioValue * MaxRiskPercent.
	rm := NewRiskManager(10_000)
	entry := 50_000.0
	qty := rm.CalculatePositionSize(entry)
	stop := rm.CalculateStopLoss(entry)
	loss := qty * (entry - stop)
	wantLoss := rm.PortfolioValue * rm.MaxRiskPercent
	if math.Abs(loss-wantLoss) > 1e-6 {
		t.Errorf("loss at stop = %.4f, want risk budget = %.4f", loss, wantLoss)
	}
}

func TestCalculatePositionSize_ZeroEntry(t *testing.T) {
	rm := NewRiskManager(10_000)
	if rm.CalculatePositionSize(0) != 0 {
		t.Error("zero entry should return 0")
	}
}

func TestCalculatePositionSize_NegativeEntry(t *testing.T) {
	rm := NewRiskManager(10_000)
	if rm.CalculatePositionSize(-100) != 0 {
		t.Error("negative entry should return 0")
	}
}

// --- CalculateStopLoss ----------------------------------------------

func TestCalculateStopLoss_KnownValues(t *testing.T) {
	// 50000 * (1 - 0.025) = 48750
	rm := NewRiskManager(10_000)
	got := rm.CalculateStopLoss(50_000)
	if got != 48_750 {
		t.Errorf("StopLoss = %v, want 48750", got)
	}
}

func TestCalculateStopLoss_BelowEntry(t *testing.T) {
	rm := NewRiskManager(10_000)
	entry := 100_000.0
	if rm.CalculateStopLoss(entry) >= entry {
		t.Error("stop-loss must be below entry for a long position")
	}
}

// --- CalculateTakeProfit --------------------------------------------

func TestCalculateTakeProfit_KnownValues(t *testing.T) {
	// 50000 * (1 + 0.05) = 52500
	rm := NewRiskManager(10_000)
	got := rm.CalculateTakeProfit(50_000)
	if got != 52_500 {
		t.Errorf("TakeProfit = %v, want 52500", got)
	}
}

func TestCalculateTakeProfit_RewardRiskRatio(t *testing.T) {
	// With 2.5% stop and 5% TP the R:R must be exactly 2:1.
	rm := NewRiskManager(10_000)
	entry := 50_000.0
	reward := rm.CalculateTakeProfit(entry) - entry
	risk := entry - rm.CalculateStopLoss(entry)
	ratio := reward / risk
	if math.Abs(ratio-2.0) > 1e-9 {
		t.Errorf("reward:risk = %.4f, want 2.0", ratio)
	}
}

func TestCalculateTakeProfit_AboveEntry(t *testing.T) {
	rm := NewRiskManager(10_000)
	entry := 100_000.0
	if rm.CalculateTakeProfit(entry) <= entry {
		t.Error("take-profit must be above entry for a long position")
	}
}

// --- RecordLoss / ResetDaily ----------------------------------------

func TestRecordLoss_AccumulatesAndResets(t *testing.T) {
	rm := NewRiskManager(10_000)
	rm.RecordLoss(100)
	rm.RecordLoss(200)
	if rm.TodayLoss != 300 {
		t.Errorf("TodayLoss = %v, want 300", rm.TodayLoss)
	}
	rm.ResetDaily()
	if rm.TodayLoss != 0 {
		t.Errorf("after reset TodayLoss = %v, want 0", rm.TodayLoss)
	}
}

func TestRecordLoss_IgnoresNegative(t *testing.T) {
	rm := NewRiskManager(10_000)
	rm.RecordLoss(-50)
	if rm.TodayLoss != 0 {
		t.Errorf("negative loss should be ignored, TodayLoss = %v", rm.TodayLoss)
	}
}
