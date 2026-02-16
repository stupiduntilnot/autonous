package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal OpenAI chat completions client.
type Client struct {
	apiKey     string
	url        string
	model      string
	httpClient *http.Client
}

// NewClient creates an OpenAI client.
func NewClient(apiKey, url, model string, timeout time.Duration) *Client {
	return &Client{
		apiKey: apiKey,
		url:    url,
		model:  model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ChatCompletion sends a chat completion request and returns the assistant reply.
func (c *Client) ChatCompletion(messages []Message) (string, error) {
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.2,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal openai request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed reading openai response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		truncated := truncate(string(body), 400)
		return "", fmt.Errorf("openai non-success status=%d body=%s", resp.StatusCode, truncated)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		truncated := truncate(string(body), 400)
		return "", fmt.Errorf("failed to parse openai response: %s", truncated)
	}

	if len(parsed.Choices) == 0 {
		return "(empty model response)", nil
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "(empty model response)", nil
	}
	return content, nil
}

func truncate(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars])
}
