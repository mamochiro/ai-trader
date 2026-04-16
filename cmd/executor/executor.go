package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/mamochiro/ai-trader/internal/exchange"
	"github.com/mamochiro/ai-trader/internal/risk"
	"github.com/mamochiro/ai-trader/internal/strategy"
)

// Executor subscribes to strategy decisions and manages the full
// trade lifecycle: risk check → market order → OCO bracket → log → notify.
type Executor struct {
	logger  *slog.Logger
	rdb     *redis.Client
	db      *pgxpool.Pool
	ex      *exchange.BinanceClient
	risk    *risk.RiskManager
	tg      *TelegramNotifier
	symbol  string
	channel string
}

// reconcilePositions checks Binance for existing open orders on
// startup so OpenPositions reflects reality after a restart.
func (e *Executor) reconcilePositions(ctx context.Context) {
	open, err := e.ex.GetOpenOrders(ctx, e.symbol)
	if err != nil {
		e.logger.Error("failed to reconcile open positions", "err", err)
		return
	}
	if open > 0 {
		// OCO has two legs (limit + stop-limit), so any open orders
		// means we have one active position.
		e.risk.OpenPositions = 1
		e.logger.Info("reconciled open position from exchange",
			"open_orders", open)
	} else {
		e.risk.OpenPositions = 0
		e.logger.Info("no open positions on exchange")
	}
}

// Run subscribes to the Redis signal channel and blocks until ctx
// is canceled.
func (e *Executor) Run(ctx context.Context) {
	e.reconcilePositions(ctx)

	sub := e.rdb.Subscribe(ctx, e.channel)
	defer sub.Close()

	ch := sub.Channel()
	e.logger.Info("subscribed to signal channel", "channel", e.channel)

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			e.handleSignal(ctx, msg.Payload)
		}
	}
}

func (e *Executor) handleSignal(ctx context.Context, payload string) {
	var dec strategy.Decision
	if err := json.Unmarshal([]byte(payload), &dec); err != nil {
		e.logger.Error("invalid signal payload", "err", err, "payload", truncate(payload, 200))
		return
	}

	e.logger.Info("signal received",
		"action", dec.Action,
		"score", dec.TotalScore,
		"reason", dec.Reason,
	)

	if dec.Action == "HOLD" {
		return
	}

	// --- Risk gate --------------------------------------------------
	if !e.risk.CanTrade() {
		e.logger.Warn("trade blocked by risk manager",
			"today_loss", e.risk.TodayLoss,
			"open_positions", e.risk.OpenPositions,
		)
		return
	}

	// --- Current price ----------------------------------------------
	price, err := e.ex.GetTickerPrice(ctx, e.symbol)
	if err != nil {
		e.logger.Error("failed to get ticker price", "err", err)
		return
	}

	// --- Position size ----------------------------------------------
	var qty float64
	var side exchange.Side

	const minNotional = 5.0 // Binance minimum order value (USD)

	if dec.Action == "BUY" {
		side = exchange.SideBuy

		// Use USDT balance for BUY.
		balance, err := e.ex.GetBalance(ctx, "USDT")
		if err != nil {
			e.logger.Error("failed to get balance", "err", err)
			return
		}
		e.risk.PortfolioValue = balance

		qty = e.risk.CalculatePositionSize(price)
		if qty <= 0 {
			e.logger.Warn("position size is zero", "price", price, "balance", balance)
			return
		}

		// Cap quantity so the order cost does not exceed available balance.
		maxQty := (balance * 0.995) / price
		if qty > maxQty {
			e.logger.Info("capping qty to available balance",
				"risk_qty", qty, "max_qty", maxQty, "balance", balance)
			qty = maxQty
		}

		if qty*price < minNotional {
			e.logger.Warn("order below minimum notional",
				"notional", qty*price, "min", minNotional,
				"qty", qty, "price", price, "balance", balance)
			return
		}
	} else {
		side = exchange.SideSell

		// For SELL on spot: sell the base asset we actually hold.
		baseAsset := exchange.BaseAsset(e.symbol)
		baseBalance, err := e.ex.GetBalance(ctx, baseAsset)
		if err != nil {
			e.logger.Error("failed to get base asset balance", "asset", baseAsset, "err", err)
			return
		}
		if baseBalance <= 0 || baseBalance*price < minNotional {
			e.logger.Warn("no base asset to sell",
				"asset", baseAsset, "balance", baseBalance, "notional", baseBalance*price)
			return
		}
		qty = baseBalance
	}

	qtyStr := formatQty(qty)
	order, err := e.ex.PlaceMarketOrder(ctx, e.symbol, side, qtyStr)
	if err != nil {
		e.logger.Error("market order failed",
			"side", side, "qty", qtyStr, "err", err)
		return
	}

	e.logger.Info("market order filled",
		"order_id", order.ID,
		"side", order.Side,
		"price", order.Price,
		"qty", order.Qty,
	)

	// --- Calculate SL/TP --------------------------------------------
	var sl, tp float64
	var ocoSide exchange.Side
	if dec.Action == "BUY" {
		sl = e.risk.CalculateStopLoss(order.Price)
		tp = e.risk.CalculateTakeProfit(order.Price)
		ocoSide = exchange.SideSell
	} else {
		// Selling (closing long / entering short bracket).
		sl = order.Price * (1 + e.risk.StopLossPct)
		tp = order.Price * (1 - e.risk.TakeProfitPct)
		ocoSide = exchange.SideBuy
	}

	// StopLimitPrice is set slightly worse than StopPrice to improve
	// fill probability.
	slippage := order.Price * 0.001
	var stopLimitPrice float64
	if ocoSide == exchange.SideSell {
		stopLimitPrice = sl - slippage
	} else {
		stopLimitPrice = sl + slippage
	}

	// --- Place OCO bracket ------------------------------------------
	ocoQtyStr := formatQty(order.Qty)
	err = e.ex.PlaceOCOOrder(ctx, exchange.OCOParams{
		Symbol:         e.symbol,
		Side:           ocoSide,
		Quantity:       ocoQtyStr,
		Price:          formatPrice(tp),
		StopPrice:      formatPrice(sl),
		StopLimitPrice: formatPrice(stopLimitPrice),
	})
	if err != nil {
		e.logger.Error("OCO order failed (market order already filled)",
			"side", ocoSide, "sl", sl, "tp", tp, "err", err)
		// Continue — the market order is filled, we still log and notify.
	} else {
		e.logger.Info("OCO bracket placed",
			"side", ocoSide,
			"stop_loss", sl,
			"take_profit", tp,
		)
	}

	// --- Track open position ----------------------------------------
	e.risk.OpenPositions++

	// Monitor the OCO bracket in the background so we can decrement
	// OpenPositions and record losses when the position closes.
	if err == nil {
		go e.monitorOCO(ctx, order, sl)
	}

	// --- Log trade to DB --------------------------------------------
	trade := Trade{
		Time:        time.Now(),
		Symbol:      e.symbol,
		Action:      dec.Action,
		Quantity:     order.Qty,
		EntryPrice:  order.Price,
		StopLoss:    sl,
		TakeProfit:  tp,
		Status:      "OPEN",
		PnL:         0,
	}
	if err := insertTrade(ctx, e.db, trade); err != nil {
		e.logger.Error("failed to log trade", "err", err)
	}

	// --- Telegram notification --------------------------------------
	if e.tg != nil {
		msg := formatTradeNotification(trade, dec.Reason)
		if err := e.tg.Send(ctx, msg); err != nil {
			e.logger.Warn("telegram notification failed", "err", err)
		}
	}
}

// monitorOCO polls open orders until the OCO bracket (SL or TP) fills,
// then decrements OpenPositions and records a loss if the stop was hit.
func (e *Executor) monitorOCO(ctx context.Context, entry *exchange.Order, stopLoss float64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			open, err := e.ex.GetOpenOrders(ctx, e.symbol)
			if err != nil {
				e.logger.Warn("failed to check open orders", "err", err)
				continue
			}
			if open > 0 {
				continue
			}

			// OCO filled — position is closed.
			e.risk.OpenPositions--
			if e.risk.OpenPositions < 0 {
				e.risk.OpenPositions = 0
			}

			// Check if stop-loss was likely hit by comparing current
			// price to the entry. If price moved against us, record loss.
			price, err := e.ex.GetTickerPrice(ctx, e.symbol)
			if err != nil {
				e.logger.Warn("failed to get price for PnL check", "err", err)
				return
			}

			pnl := (price - entry.Price) * entry.Qty
			if entry.Side == exchange.SideSell {
				pnl = -pnl
			}

			if pnl < 0 {
				e.risk.RecordLoss(-pnl)
				e.logger.Info("position closed at loss",
					"pnl", pnl, "today_loss", e.risk.TodayLoss)
			} else {
				e.logger.Info("position closed at profit", "pnl", pnl)
			}
			return
		}
	}
}

// formatQty truncates (not rounds) a base-asset quantity to 5 decimals
// so we never try to sell more than we hold.
func formatQty(qty float64) string {
	truncated := math.Floor(qty*100000) / 100000
	return fmt.Sprintf("%.5f", truncated)
}

// formatPrice formats a price for BTCUSDT (2 decimals).
func formatPrice(price float64) string {
	return fmt.Sprintf("%.2f", price)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func formatTradeNotification(t Trade, reason string) string {
	emoji := "\U0001f7e2" // green circle
	if t.Action == "SELL" {
		emoji = "\U0001f534" // red circle
	}
	return fmt.Sprintf(
		"%s %s %s\nPrice: $%.2f\nQty: %.5f BTC\nSL: $%.2f | TP: $%.2f\nReason: %s",
		emoji, t.Action, t.Symbol,
		t.EntryPrice,
		t.Quantity,
		t.StopLoss, t.TakeProfit,
		reason,
	)
}
