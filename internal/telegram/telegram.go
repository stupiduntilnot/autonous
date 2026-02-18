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

	cmdpkg "github.com/stupiduntilnot/autonous/internal/commander"
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

type Update = cmdpkg.Update
type Message = cmdpkg.Message
type Chat = cmdpkg.Chat

type tgRawUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *cmdpkg.Message  `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgCallbackQuery struct {
	ID      string          `json:"id"`
	Data    string          `json:"data"`
	Message *cmdpkg.Message `json:"message,omitempty"`
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

	var raws []tgRawUpdate
	if err := json.Unmarshal(tgResp.Result, &raws); err != nil {
		return nil, fmt.Errorf("failed to parse getUpdates result: %w", err)
	}
	updates := make([]Update, 0, len(raws))
	for _, ru := range raws {
		if ru.Message != nil {
			updates = append(updates, Update{UpdateID: ru.UpdateID, Message: ru.Message})
			continue
		}
		if ru.CallbackQuery != nil && ru.CallbackQuery.Message != nil {
			msg := *ru.CallbackQuery.Message
			data := strings.TrimSpace(ru.CallbackQuery.Data)
			msg.Text = &data
			if msg.Date == 0 {
				msg.Date = time.Now().Unix()
			}
			updates = append(updates, Update{UpdateID: ru.UpdateID, Message: &msg})
			_ = c.answerCallbackQuery(ru.CallbackQuery.ID)
		}
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

// SendApprovalRequest sends a message with inline approve/cancel buttons.
func (c *Client) SendApprovalRequest(chatID int64, txID string) error {
	limited := truncate(fmt.Sprintf("候选版本已 staged：%s\n请选择操作。", txID), 3900)
	approve := jsonString("approve " + txID)
	cancel := jsonString("cancel " + txID)
	payload := fmt.Sprintf(
		`{"chat_id":%d,"text":%s,"reply_markup":{"inline_keyboard":[[{"text":"Approve","callback_data":%s},{"text":"Cancel","callback_data":%s}]]}}`,
		chatID, jsonString(limited), approve, cancel,
	)
	resp, err := c.httpClient.Post(
		c.apiBase+"/sendMessage",
		"application/json",
		strings.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("telegram sendApprovalRequest failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return nil
}

func (c *Client) answerCallbackQuery(callbackID string) error {
	callbackID = strings.TrimSpace(callbackID)
	if callbackID == "" {
		return nil
	}
	payload := fmt.Sprintf(`{"callback_query_id":%s}`, jsonString(callbackID))
	resp, err := c.httpClient.Post(
		c.apiBase+"/answerCallbackQuery",
		"application/json",
		strings.NewReader(payload),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
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
