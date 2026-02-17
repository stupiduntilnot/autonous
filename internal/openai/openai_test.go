package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatCompletion_WithUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "Hello!"}},
			},
			"usage": map[string]any{
				"prompt_tokens":     42,
				"completion_tokens": 7,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "test-model", 5*time.Second)
	result, err := client.ChatCompletion([]Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}

	if result.Content != "Hello!" {
		t.Errorf("expected content 'Hello!', got %q", result.Content)
	}
	if result.InputTokens != 42 {
		t.Errorf("expected 42 input tokens, got %d", result.InputTokens)
	}
	if result.OutputTokens != 7 {
		t.Errorf("expected 7 output tokens, got %d", result.OutputTokens)
	}
}

func TestChatCompletion_NoUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "World"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "test-model", 5*time.Second)
	result, err := client.ChatCompletion([]Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}

	if result.Content != "World" {
		t.Errorf("expected 'World', got %q", result.Content)
	}
	if result.InputTokens != 0 {
		t.Errorf("expected 0 input tokens, got %d", result.InputTokens)
	}
	if result.OutputTokens != 0 {
		t.Errorf("expected 0 output tokens, got %d", result.OutputTokens)
	}
}

func TestChatCompletion_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 0},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "test-model", 5*time.Second)
	result, err := client.ChatCompletion([]Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}

	if result.Content != "(empty model response)" {
		t.Errorf("expected empty model response fallback, got %q", result.Content)
	}
	if result.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.InputTokens)
	}
}

func TestChatCompletion_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "test-model", 5*time.Second)
	_, err := client.ChatCompletion([]Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
}
