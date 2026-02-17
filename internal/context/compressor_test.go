package context

import "testing"

func TestSimpleCompressor_Truncate(t *testing.T) {
	c := &SimpleCompressor{MaxMessages: 2}
	msgs := []Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	result := c.Compress(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Content != "b" {
		t.Errorf("expected 'b', got %q", result[0].Content)
	}
	if result[1].Content != "c" {
		t.Errorf("expected 'c', got %q", result[1].Content)
	}
}

func TestSimpleCompressor_NoTruncation(t *testing.T) {
	c := &SimpleCompressor{MaxMessages: 5}
	msgs := []Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	result := c.Compress(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestSimpleCompressor_EmptyInput(t *testing.T) {
	c := &SimpleCompressor{MaxMessages: 3}
	result := c.Compress(nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(result))
	}
}

func TestSimpleCompressor_ZeroMax(t *testing.T) {
	c := &SimpleCompressor{MaxMessages: 0}
	msgs := []Message{{Role: "user", Content: "a"}}
	result := c.Compress(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message (no truncation with 0 max), got %d", len(result))
	}
}
