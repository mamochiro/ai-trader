package sentiment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const cacheTTL = 15 * time.Minute

func cacheKey(asset string) string {
	return "sentiment:" + asset
}

// getCached retrieves a previously cached SentimentResult from Redis.
// Returns the result and true on a hit, or zero-value and false on
// any miss or error.
func (a *Analyzer) getCached(ctx context.Context, asset string) (SentimentResult, bool) {
	val, err := a.rdb.Get(ctx, cacheKey(asset)).Bytes()
	if err == redis.Nil || err != nil {
		return SentimentResult{}, false
	}
	var r SentimentResult
	if err := json.Unmarshal(val, &r); err != nil {
		return SentimentResult{}, false
	}
	return r, true
}

// setCache stores a SentimentResult in Redis with a 15-minute TTL.
func (a *Analyzer) setCache(ctx context.Context, asset string, r SentimentResult) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return a.rdb.Set(ctx, cacheKey(asset), data, cacheTTL).Err()
}
