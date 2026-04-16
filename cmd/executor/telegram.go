package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TelegramNotifier sends trade notifications via the Telegram Bot API.
type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegramNotifier returns a notifier. Pass empty botToken/chatID
// to disable notifications — the caller should nil-check instead.
func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts a text message to the configured chat.
func (t *TelegramNotifier) Send(ctx context.Context, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	body, err := json.Marshal(map[string]string{
		"chat_id": t.chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
