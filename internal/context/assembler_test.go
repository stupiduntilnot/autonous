package context

import "testing"

func TestStandardAssembler_Assemble(t *testing.T) {
	a := &StandardAssembler{}
	history := []Message{
		{Role: "user", Content: "prev question"},
		{Role: "assistant", Content: "prev answer"},
	}
	result := a.Assemble("You are a bot.", history, "new question")

	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}

	if result[0].Role != "system" || result[0].Content != "You are a bot." {
		t.Errorf("unexpected system message: %+v", result[0])
	}
	if result[1].Role != "user" || result[1].Content != "prev question" {
		t.Errorf("unexpected history[0]: %+v", result[1])
	}
	if result[2].Role != "assistant" || result[2].Content != "prev answer" {
		t.Errorf("unexpected history[1]: %+v", result[2])
	}
	if result[3].Role != "user" || result[3].Content != "new question" {
		t.Errorf("unexpected user message: %+v", result[3])
	}
}

func TestStandardAssembler_EmptyHistory(t *testing.T) {
	a := &StandardAssembler{}
	result := a.Assemble("system", nil, "hello")

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected system role, got %q", result[0].Role)
	}
	if result[1].Role != "user" || result[1].Content != "hello" {
		t.Errorf("unexpected user message: %+v", result[1])
	}
}
