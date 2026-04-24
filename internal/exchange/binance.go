// Package exchange wraps the Binance Testnet client so the rest of
// the system only depends on a narrow interface.
package exchange

import (
	"context"
	"fmt"
	"strconv"

	"github.com/adshao/go-binance/v2"
)

// Side is BUY or SELL.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// Order is a filled order returned by the exchange.
type Order struct {
	ID     int64
	Symbol string
	Side   Side
	Status string
	Price  float64 // average fill price
	Qty    float64 // executed quantity
}

// OCOParams describes the bracket exit order placed after a fill.
type OCOParams struct {
	Symbol         string
	Side           Side   // opposite of entry side
	Quantity       string // base-asset quantity
	Price          string // take-profit limit price
	StopPrice      string // stop trigger price
	StopLimitPrice string // limit price after stop triggers
}

// Kline is a single OHLCV candle.
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

// BinanceClient is a real implementation backed by go-binance.
type BinanceClient struct {
	c *binance.Client
}

// NewBinanceClient constructs a client. When testnet is true, the
// underlying go-binance client is pointed at the Binance testnet.
func NewBinanceClient(apiKey, secret string, testnet bool) *BinanceClient {
	binance.UseTestnet = testnet
	return &BinanceClient{c: binance.NewClient(apiKey, secret)}
}

// GetBalance returns the free (available) balance for the given asset
// (e.g. "USDT" or "BTC").
func (b *BinanceClient) GetBalance(ctx context.Context, asset string) (float64, error) {
	acct, err := b.c.NewGetAccountService().Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("get account: %w", err)
	}
	for _, bal := range acct.Balances {
		if bal.Asset == asset {
			return strconv.ParseFloat(bal.Free, 64)
		}
	}
	return 0, nil
}

// AssetBalance holds one asset's free + locked balance.
type AssetBalance struct {
	Asset  string  `json:"asset"`
	Free   float64 `json:"free"`
	Locked float64 `json:"locked"`
}

// GetAllBalances returns all spot assets with non-zero balance.
func (b *BinanceClient) GetAllBalances(ctx context.Context) ([]AssetBalance, error) {
	acct, err := b.c.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	var out []AssetBalance
	for _, bal := range acct.Balances {
		free, _ := strconv.ParseFloat(bal.Free, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		if free > 0 || locked > 0 {
			out = append(out, AssetBalance{
				Asset:  bal.Asset,
				Free:   free,
				Locked: locked,
			})
		}
	}
	return out, nil
}

// GetTickerPrice returns the last traded price for a symbol.
func (b *BinanceClient) GetTickerPrice(ctx context.Context, symbol string) (float64, error) {
	prices, err := b.c.NewListPricesService().Symbol(symbol).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("ticker price: %w", err)
	}
	if len(prices) == 0 {
		return 0, fmt.Errorf("no price for %s", symbol)
	}
	return strconv.ParseFloat(prices[0].Price, 64)
}

// GetKlines fetches historical candlestick data.
func (b *BinanceClient) GetKlines(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	raw, err := b.c.NewKlinesService().
		Symbol(symbol).
		Interval(interval).
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("klines: %w", err)
	}
	out := make([]Kline, len(raw))
	for i, k := range raw {
		out[i] = Kline{
			OpenTime:  k.OpenTime,
			CloseTime: k.CloseTime,
		}
		out[i].Open, _ = strconv.ParseFloat(k.Open, 64)
		out[i].High, _ = strconv.ParseFloat(k.High, 64)
		out[i].Low, _ = strconv.ParseFloat(k.Low, 64)
		out[i].Close, _ = strconv.ParseFloat(k.Close, 64)
		out[i].Volume, _ = strconv.ParseFloat(k.Volume, 64)
	}
	return out, nil
}

// PlaceMarketOrder submits a MARKET order and returns the fill. The
// response type is set to FULL so we get individual fills and can
// compute the average price.
func (b *BinanceClient) PlaceMarketOrder(ctx context.Context, symbol string, side Side, quantity string) (*Order, error) {
	resp, err := b.c.NewCreateOrderService().
		Symbol(symbol).
		Side(binance.SideType(side)).
		Type(binance.OrderTypeMarket).
		Quantity(quantity).
		NewOrderRespType(binance.NewOrderRespTypeFULL).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("market order: %w", err)
	}

	execQty, _ := strconv.ParseFloat(resp.ExecutedQuantity, 64)
	cumQuote, _ := strconv.ParseFloat(resp.CummulativeQuoteQuantity, 64)
	avgPrice := 0.0
	if execQty > 0 {
		avgPrice = cumQuote / execQty
	}

	return &Order{
		ID:     resp.OrderID,
		Symbol: resp.Symbol,
		Side:   Side(resp.Side),
		Status: string(resp.Status),
		Price:  avgPrice,
		Qty:    execQty,
	}, nil
}

// GetOpenOrders returns the number of open orders for a symbol.
func (b *BinanceClient) GetOpenOrders(ctx context.Context, symbol string) (int, error) {
	orders, err := b.c.NewListOpenOrdersService().Symbol(symbol).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("open orders: %w", err)
	}
	return len(orders), nil
}

// CancelOpenOrders cancels all active orders for a symbol, including
// both legs of any OCO bracket. Needed before a SELL-to-close so the
// base asset locked by the bracket is released for the market sell.
func (b *BinanceClient) CancelOpenOrders(ctx context.Context, symbol string) error {
	_, err := b.c.NewCancelOpenOrdersService().Symbol(symbol).Do(ctx)
	if err != nil {
		return fmt.Errorf("cancel open orders: %w", err)
	}
	return nil
}

// PlaceOCOOrder places a one-cancels-the-other bracket exit order.
// Price is the take-profit limit, StopPrice triggers the stop, and
// StopLimitPrice is the limit after the stop fires (set slightly
// worse than StopPrice to ensure a fill).
func (b *BinanceClient) PlaceOCOOrder(ctx context.Context, p OCOParams) error {
	_, err := b.c.NewCreateOCOService().
		Symbol(p.Symbol).
		Side(binance.SideType(p.Side)).
		Quantity(p.Quantity).
		Price(p.Price).
		StopPrice(p.StopPrice).
		StopLimitPrice(p.StopLimitPrice).
		StopLimitTimeInForce(binance.TimeInForceTypeGTC).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("oco order: %w", err)
	}
	return nil
}
