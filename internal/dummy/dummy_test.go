package dummy

import (
	"testing"

	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
)

func TestNewProvider_InvalidScript(t *testing.T) {
	_, err := NewProvider("x", "boom")
	if err == nil {
		t.Fatal("expected parse error for invalid script")
	}
}

func TestProvider_ScriptedResponses(t *testing.T) {
	p, err := NewProvider("x", "err:provider_api,msg:hello")
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.ChatCompletion([]ctxpkg.Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected first call to error")
	}

	resp, err := p.ChatCompletion([]ctxpkg.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("expected hello, got %q", resp.Content)
	}
}

func TestCommander_MsgAction(t *testing.T) {
	c, err := NewCommander("msg:test-msg", "ok")
	if err != nil {
		t.Fatal(err)
	}
	updates, err := c.GetUpdates(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Message == nil || updates[0].Message.Text == nil {
		t.Fatalf("unexpected updates: %+v", updates)
	}
	if *updates[0].Message.Text != "test-msg" {
		t.Fatalf("expected test-msg, got %q", *updates[0].Message.Text)
	}
}

func TestProvider_MsgB64Action(t *testing.T) {
	p, err := NewProvider("x", "msgb64:aGVsbG8=") // "hello"
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.ChatCompletion([]ctxpkg.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("expected hello, got %q", resp.Content)
	}
}
