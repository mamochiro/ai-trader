package sentiment

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// rssFeedURLs are public RSS feeds that require no API key.
// The first reachable feed wins; the rest are fallbacks.
var rssFeedURLs = map[string]string{
	"BTC":  "https://cointelegraph.com/rss/tag/bitcoin",
	"ETH":  "https://cointelegraph.com/rss/tag/ethereum",
	"SOL":  "https://cointelegraph.com/rss/tag/solana",
	"BNB":  "https://cointelegraph.com/rss/tag/bnb",
	"XRP":  "https://cointelegraph.com/rss/tag/xrp",
	"DOGE": "https://cointelegraph.com/rss/tag/dogecoin",
	"ADA":  "https://cointelegraph.com/rss/tag/cardano",
}

const (
	defaultRSSFeed = "https://cointelegraph.com/rss"
	maxHeadlines   = 5
)

// rssResponse is the minimal subset of RSS 2.0 we need.
type rssResponse struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title string `xml:"title"`
}

// fetchHeadlines GETs the RSS feed for the given asset and returns
// up to 5 headline strings. No API key is required.
func (a *Analyzer) fetchHeadlines(ctx context.Context, asset string) ([]string, error) {
	feedURL, ok := rssFeedURLs[strings.ToUpper(asset)]
	if !ok {
		feedURL = defaultRSSFeed
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rss fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("rss %d: %s", resp.StatusCode, body)
	}

	var feed rssResponse
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("decode rss: %w", err)
	}

	items := feed.Channel.Items
	limit := maxHeadlines
	if len(items) < limit {
		limit = len(items)
	}
	headlines := make([]string, limit)
	for i := 0; i < limit; i++ {
		headlines[i] = items[i].Title
	}
	return headlines, nil
}
