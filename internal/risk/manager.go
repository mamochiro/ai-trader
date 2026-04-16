package risk

// Default risk parameters.
const (
	DefaultMaxRiskPercent = 0.03  // 3% of portfolio per trade
	DefaultStopLossPct    = 0.025 // 2.5% below entry
	DefaultTakeProfitPct  = 0.05  // 5% above entry (2:1 reward/risk)
	DefaultDailyLossLimit = 0.08  // 8% drawdown → halt trading
	DefaultMaxPositions   = 1
)

// RiskManager enforces hard risk rules before any order reaches the
// exchange. The executor must consult it on every trade.
type RiskManager struct {
	PortfolioValue float64
	MaxRiskPercent float64 // fraction of portfolio risked per trade
	StopLossPct    float64 // stop-loss distance as fraction of entry
	TakeProfitPct  float64 // take-profit distance as fraction of entry
	DailyLossLimit float64 // max daily drawdown as fraction of portfolio
	MaxPositions   int     // max simultaneous open positions
	TodayLoss      float64 // accumulated loss today (absolute USDT)
	OpenPositions  int     // current open position count
}

// NewRiskManager returns a manager initialised with safe defaults:
// 3% risk per trade, 2.5% stop, 5% take-profit, 8% daily limit,
// 1 open position max.
func NewRiskManager(portfolioValue float64) *RiskManager {
	return &RiskManager{
		PortfolioValue: portfolioValue,
		MaxRiskPercent: DefaultMaxRiskPercent,
		StopLossPct:    DefaultStopLossPct,
		TakeProfitPct:  DefaultTakeProfitPct,
		DailyLossLimit: DefaultDailyLossLimit,
		MaxPositions:   DefaultMaxPositions,
	}
}

// CanTrade returns false when:
//   - the daily loss limit has been breached, OR
//   - the maximum number of open positions is reached.
//
// The executor must call CanTrade before every order attempt.
func (r *RiskManager) CanTrade() bool {
	if r.PortfolioValue > 0 && r.TodayLoss >= r.PortfolioValue*r.DailyLossLimit {
		return false
	}
	if r.OpenPositions >= r.MaxPositions {
		return false
	}
	return true
}

// CalculatePositionSize returns the base-asset quantity (e.g. BTC)
// to buy at entryPrice such that hitting the stop-loss costs exactly
// MaxRiskPercent of the portfolio.
//
//	riskBudget   = PortfolioValue × MaxRiskPercent
//	stopDistance  = entryPrice × StopLossPct
//	positionSize = riskBudget / stopDistance
func (r *RiskManager) CalculatePositionSize(entryPrice float64) float64 {
	if entryPrice <= 0 || r.StopLossPct <= 0 {
		return 0
	}
	riskBudget := r.PortfolioValue * r.MaxRiskPercent
	stopDistance := entryPrice * r.StopLossPct
	return riskBudget / stopDistance
}

// CalculateStopLoss returns the stop-loss price for a long position:
// entryPrice × (1 − StopLossPct). For the default 2.5% stop on a
// $50 000 entry, the stop sits at $48 750.
func (r *RiskManager) CalculateStopLoss(entryPrice float64) float64 {
	return entryPrice * (1 - r.StopLossPct)
}

// CalculateTakeProfit returns the take-profit price for a long
// position: entryPrice × (1 + TakeProfitPct). With the default 5%
// target and 2.5% stop the reward:risk ratio is 2:1.
func (r *RiskManager) CalculateTakeProfit(entryPrice float64) float64 {
	return entryPrice * (1 + r.TakeProfitPct)
}

// RecordLoss adds a loss amount (positive USDT) to the daily tally.
// The executor calls this whenever a position is closed at a loss.
func (r *RiskManager) RecordLoss(amount float64) {
	if amount > 0 {
		r.TodayLoss += amount
	}
}

// ResetDaily zeroes the daily loss tally. Call at midnight UTC.
func (r *RiskManager) ResetDaily() {
	r.TodayLoss = 0
}
