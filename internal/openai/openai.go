package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	modelpkg "github.com/stupiduntilnot/autonous/internal/model"
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

// CompletionResponse is re-exported for compatibility.
type CompletionResponse = modelpkg.CompletionResponse

// message is the internal JSON-serializable chat message for OpenAI API.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ChatCompletion sends a chat completion request and returns a CompletionResponse.
func (c *Client) ChatCompletion(messages []ctxpkg.Message) (modelpkg.CompletionResponse, error) {
	internal := make([]message, len(messages))
	for i, m := range messages {
		internal[i] = message{Role: m.Role, Content: m.Content}
	}

	reqBody := chatRequest{
		Model:       c.model,
		Messages:    internal,
		Temperature: 0.2,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return modelpkg.CompletionResponse{}, fmt.Errorf("failed to marshal openai request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return modelpkg.CompletionResponse{}, fmt.Errorf("failed to create openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return modelpkg.CompletionResponse{}, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return modelpkg.CompletionResponse{}, fmt.Errorf("failed reading openai response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		truncated := truncate(string(body), 400)
		return modelpkg.CompletionResponse{}, fmt.Errorf("openai non-success status=%d body=%s", resp.StatusCode, truncated)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		truncated := truncate(string(body), 400)
		return modelpkg.CompletionResponse{}, fmt.Errorf("failed to parse openai response: %s", truncated)
	}

	result := modelpkg.CompletionResponse{}

	// Extract token usage.
	if parsed.Usage != nil {
		result.InputTokens = parsed.Usage.PromptTokens
		result.OutputTokens = parsed.Usage.CompletionTokens
	}

	if len(parsed.Choices) == 0 {
		result.Content = "(empty model response)"
		return result, nil
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		result.Content = "(empty model response)"
		return result, nil
	}
	result.Content = content
	return result, nil
}

func truncate(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars])
}
