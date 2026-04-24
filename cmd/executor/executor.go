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

// syncPositions sets OpenPositions to match the exchange. Running this
// periodically (not just on startup) means the counter self-heals when
// an OCO bracket fills — we don't rely on per-trade monitor goroutines
// to decrement, so positions opened in a previous process lifetime are
// still cleaned up after they close.
func (e *Executor) syncPositions(ctx context.Context) {
	open, err := e.ex.GetOpenOrders(ctx, e.symbol)
	if err != nil {
		e.logger.Warn("position sync failed", "err", err)
		return
	}
	want := 0
	if open > 0 {
		want = 1
	}
	if e.risk.OpenPositions != want {
		e.logger.Info("position count synced from exchange",
			"was", e.risk.OpenPositions, "now", want, "open_orders", open)
		e.risk.OpenPositions = want
	}
}

func (e *Executor) runPositionSync(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.syncPositions(ctx)
		}
	}
}

// Run subscribes to the Redis signal channel and blocks until ctx
// is canceled.
func (e *Executor) Run(ctx context.Context) {
	e.syncPositions(ctx)
	go e.runPositionSync(ctx)

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

	price, err := e.ex.GetTickerPrice(ctx, e.symbol)
	if err != nil {
		e.logger.Error("failed to get ticker price", "err", err)
		return
	}

	if dec.Action == "SELL" {
		e.handleSellClose(ctx, dec, price)
		return
	}

	e.handleBuyOpen(ctx, dec, price)
}

// handleBuyOpen opens a new long: risk-sized market buy with an OCO
// bracket exit. Gated by RiskManager.CanTrade().
func (e *Executor) handleBuyOpen(ctx context.Context, dec strategy.Decision, price float64) {
	const minNotional = 5.0

	if !e.risk.CanTrade() {
		e.logger.Warn("BUY blocked by risk manager",
			"today_loss", e.risk.TodayLoss,
			"open_positions", e.risk.OpenPositions,
		)
		return
	}

	balance, err := e.ex.GetBalance(ctx, "USDT")
	if err != nil {
		e.logger.Error("failed to get balance", "err", err)
		return
	}
	e.risk.PortfolioValue = balance

	qty := e.risk.CalculatePositionSize(price)
	if qty <= 0 {
		e.logger.Warn("position size is zero", "price", price, "balance", balance)
		return
	}

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

	qtyStr := formatQty(qty)
	order, err := e.ex.PlaceMarketOrder(ctx, e.symbol, exchange.SideBuy, qtyStr)
	if err != nil {
		e.logger.Error("market buy failed", "qty", qtyStr, "err", err)
		return
	}
	e.logger.Info("market buy filled",
		"order_id", order.ID, "price", order.Price, "qty", order.Qty)

	sl := e.risk.CalculateStopLoss(order.Price)
	tp := e.risk.CalculateTakeProfit(order.Price)
	stopLimitPrice := sl - order.Price*0.001

	ocoErr := e.ex.PlaceOCOOrder(ctx, exchange.OCOParams{
		Symbol:         e.symbol,
		Side:           exchange.SideSell,
		Quantity:       formatQty(order.Qty),
		Price:          formatPrice(tp),
		StopPrice:      formatPrice(sl),
		StopLimitPrice: formatPrice(stopLimitPrice),
	})
	if ocoErr != nil {
		e.logger.Error("OCO order failed (market buy already filled)",
			"sl", sl, "tp", tp, "err", ocoErr)
	} else {
		e.logger.Info("OCO bracket placed", "stop_loss", sl, "take_profit", tp)
	}

	e.risk.OpenPositions++
	if ocoErr == nil {
		go e.monitorOCO(ctx, order)
	}

	trade := Trade{
		Time:       time.Now(),
		Symbol:     e.symbol,
		Action:     "BUY",
		Quantity:   order.Qty,
		EntryPrice: order.Price,
		StopLoss:   sl,
		TakeProfit: tp,
		Status:     "OPEN",
	}
	if err := insertTrade(ctx, e.db, trade); err != nil {
		e.logger.Error("failed to log trade", "err", err)
	}
	if e.tg != nil {
		if err := e.tg.Send(ctx, formatTradeNotification(trade, dec.Reason)); err != nil {
			e.logger.Warn("telegram notification failed", "err", err)
		}
	}
}

// handleSellClose closes any existing long on spot. Cancels the prior
// OCO bracket to unlock the base asset, then market-sells what we hold.
// No risk gate (exits must always be allowed) and no new OCO — once
// sold, there's nothing to bracket.
func (e *Executor) handleSellClose(ctx context.Context, dec strategy.Decision, price float64) {
	const minNotional = 5.0

	baseAsset := exchange.BaseAsset(e.symbol)

	if err := e.ex.CancelOpenOrders(ctx, e.symbol); err != nil {
		e.logger.Warn("cancel open orders failed (continuing)", "err", err)
	}

	baseBalance, err := e.ex.GetBalance(ctx, baseAsset)
	if err != nil {
		e.logger.Error("failed to get base asset balance", "asset", baseAsset, "err", err)
		return
	}
	if baseBalance <= 0 || baseBalance*price < minNotional {
		e.logger.Warn("no base asset to sell",
			"asset", baseAsset, "balance", baseBalance, "notional", baseBalance*price)
		e.syncPositions(ctx)
		return
	}

	qtyStr := formatQty(baseBalance)
	order, err := e.ex.PlaceMarketOrder(ctx, e.symbol, exchange.SideSell, qtyStr)
	if err != nil {
		e.logger.Error("market sell failed", "qty", qtyStr, "err", err)
		return
	}
	e.logger.Info("market sell filled (closed long)",
		"order_id", order.ID, "price", order.Price, "qty", order.Qty)

	e.syncPositions(ctx)

	trade := Trade{
		Time:       time.Now(),
		Symbol:     e.symbol,
		Action:     "SELL",
		Quantity:   order.Qty,
		EntryPrice: order.Price,
		Status:     "CLOSED",
	}
	if err := insertTrade(ctx, e.db, trade); err != nil {
		e.logger.Error("failed to log trade", "err", err)
	}
	if e.tg != nil {
		if err := e.tg.Send(ctx, formatTradeNotification(trade, dec.Reason)); err != nil {
			e.logger.Warn("telegram notification failed", "err", err)
		}
	}
}

// monitorOCO polls open orders until the OCO bracket (SL or TP) fills,
// then decrements OpenPositions and records a loss if the stop was hit.
func (e *Executor) monitorOCO(ctx context.Context, entry *exchange.Order) {
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
