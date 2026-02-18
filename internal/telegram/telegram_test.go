package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetUpdates_MapsCallbackQueryToMessage(t *testing.T) {
	var answered bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getUpdates":
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":11,"callback_query":{"id":"cb-1","data":"approve tx-1","message":{"chat":{"id":123},"date":1700000000}}}]}`)
		case "/answerCallbackQuery":
			answered = true
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	updates, err := c.GetUpdates(0, 0)
	if err != nil {
		t.Fatalf("GetUpdates failed: %v", err)
	}
	if len(updates) != 1 || updates[0].Message == nil || updates[0].Message.Text == nil {
		t.Fatalf("unexpected updates: %#v", updates)
	}
	if *updates[0].Message.Text != "approve tx-1" {
		t.Fatalf("unexpected callback mapped text: %q", *updates[0].Message.Text)
	}
	if !answered {
		t.Fatal("expected answerCallbackQuery to be called")
	}
}

func TestSendApprovalRequest_SendsInlineKeyboard(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sendMessage" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	if err := c.SendApprovalRequest(123, "update stage 成功：tx_id=tx-abc sha256=deadbeef", "tx-abc"); err != nil {
		t.Fatalf("SendApprovalRequest failed: %v", err)
	}
	if !strings.Contains(gotBody, `"inline_keyboard"`) {
		t.Fatalf("expected inline keyboard payload, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"approve tx-abc"`) {
		t.Fatalf("expected approve callback_data, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"cancel tx-abc"`) {
		t.Fatalf("expected cancel callback_data, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "update stage 成功") {
		t.Fatalf("expected merged stage success text, got: %s", gotBody)
	}
}
