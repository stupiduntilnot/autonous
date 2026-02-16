package telegram

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal Telegram Bot API client.
type Client struct {
	apiBase    string
	httpClient *http.Client
}

// NewClient creates a Telegram client for the given bot API base URL
// (e.g. "https://api.telegram.org/bot<token>").
func NewClient(apiBase string, requestTimeout time.Duration) *Client {
	return &Client{
		apiBase: apiBase,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// Response is the generic Telegram API response wrapper.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}

// Update represents a Telegram update.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message represents a Telegram message.
type Message struct {
	Chat Chat    `json:"chat"`
	Text *string `json:"text,omitempty"`
	Date int64   `json:"date"`
}

// Chat represents a Telegram chat.
type Chat struct {
	ID int64 `json:"id"`
}

// GetUpdates calls the getUpdates API.
func (c *Client) GetUpdates(offset int64, timeout int) ([]Update, error) {
	params := url.Values{}
	params.Set("offset", strconv.FormatInt(offset, 10))
	params.Set("timeout", strconv.Itoa(timeout))

	resp, err := c.httpClient.Get(c.apiBase + "/getUpdates?" + params.Encode())
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read getUpdates response: %w", err)
	}

	var tgResp Response
	if err := json.Unmarshal(body, &tgResp); err != nil {
		return nil, fmt.Errorf("failed to parse getUpdates response: %w", err)
	}

	if !tgResp.OK {
		return nil, nil
	}

	var updates []Update
	if err := json.Unmarshal(tgResp.Result, &updates); err != nil {
		return nil, fmt.Errorf("failed to parse getUpdates result: %w", err)
	}
	return updates, nil
}

// SendMessage sends a text message to the given chat.
func (c *Client) SendMessage(chatID int64, text string) error {
	limited := truncate(text, 3900)
	payload := fmt.Sprintf(`{"chat_id":%d,"text":%s}`, chatID, jsonString(limited))

	resp, err := c.httpClient.Post(
		c.apiBase+"/sendMessage",
		"application/json",
		strings.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("telegram sendMessage request failed: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain
	return nil
}

func truncate(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars])
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
