package dummy

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	cmdpkg "github.com/stupiduntilnot/autonous/internal/commander"
	ctxpkg "github.com/stupiduntilnot/autonous/internal/context"
	modelpkg "github.com/stupiduntilnot/autonous/internal/model"
)

type action struct {
	kind string
	arg  string
}

func parseScript(script string) ([]action, error) {
	if strings.TrimSpace(script) == "" {
		return []action{{kind: "ok"}}, nil
	}
	parts := strings.Split(script, ",")
	actions := make([]action, 0, len(parts))
	for _, p := range parts {
		token := strings.TrimSpace(p)
		if token == "" {
			continue
		}
		if token == "ok" {
			actions = append(actions, action{kind: "ok"})
			continue
		}
		if strings.HasPrefix(token, "err:") {
			actions = append(actions, action{kind: "err", arg: strings.TrimPrefix(token, "err:")})
			continue
		}
		if strings.HasPrefix(token, "sleep:") {
			actions = append(actions, action{kind: "sleep", arg: strings.TrimPrefix(token, "sleep:")})
			continue
		}
		if strings.HasPrefix(token, "msg:") {
			actions = append(actions, action{kind: "msg", arg: strings.TrimPrefix(token, "msg:")})
			continue
		}
		if strings.HasPrefix(token, "msgb64:") {
			actions = append(actions, action{kind: "msgb64", arg: strings.TrimPrefix(token, "msgb64:")})
			continue
		}
		return nil, fmt.Errorf("invalid dummy action: %s", token)
	}
	if len(actions) == 0 {
		actions = append(actions, action{kind: "ok"})
	}
	return actions, nil
}

type scriptRunner struct {
	actions []action
	index   int
}

func newRunner(script string) (*scriptRunner, error) {
	actions, err := parseScript(script)
	if err != nil {
		return nil, err
	}
	return &scriptRunner{actions: actions}, nil
}

func (r *scriptRunner) next() action {
	if len(r.actions) == 0 {
		return action{kind: "ok"}
	}
	if r.index >= len(r.actions) {
		return r.actions[len(r.actions)-1]
	}
	a := r.actions[r.index]
	r.index++
	return a
}

type Commander struct {
	mu       sync.Mutex
	poll     *scriptRunner
	send     *scriptRunner
	updateID int64
}

func NewCommander(pollScript, sendScript string) (*Commander, error) {
	poll, err := newRunner(pollScript)
	if err != nil {
		return nil, err
	}
	send, err := newRunner(sendScript)
	if err != nil {
		return nil, err
	}
	return &Commander{poll: poll, send: send, updateID: 1}, nil
}

func (c *Commander) GetUpdates(offset int64, timeout int) ([]cmdpkg.Update, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.poll.next()
	switch a.kind {
	case "ok":
		return nil, nil
	case "err":
		return nil, fmt.Errorf("dummy commander error class=%s", emptyAs(a.arg, "command_source_api"))
	case "sleep":
		ms, _ := strconv.Atoi(a.arg)
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return nil, nil
	case "msg":
		text := a.arg
		msg := text
		c.updateID++
		return []cmdpkg.Update{
			{
				UpdateID: c.updateID,
				Message: &cmdpkg.Message{
					Chat: cmdpkg.Chat{ID: 1},
					Text: &msg,
					Date: time.Now().Unix(),
				},
			},
		}, nil
	case "msgb64":
		raw, err := base64.StdEncoding.DecodeString(a.arg)
		if err != nil {
			return nil, fmt.Errorf("dummy commander msgb64 decode failed: %w", err)
		}
		msg := string(raw)
		c.updateID++
		return []cmdpkg.Update{
			{
				UpdateID: c.updateID,
				Message: &cmdpkg.Message{
					Chat: cmdpkg.Chat{ID: 1},
					Text: &msg,
					Date: time.Now().Unix(),
				},
			},
		}, nil
	default:
		return nil, nil
	}
}

func (c *Commander) SendMessage(chatID int64, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.send.next()
	switch a.kind {
	case "ok":
		return nil
	case "err":
		return fmt.Errorf("dummy commander send error class=%s", emptyAs(a.arg, "command_source_api"))
	case "sleep":
		ms, _ := strconv.Atoi(a.arg)
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return nil
	default:
		return nil
	}
}

type Provider struct {
	mu     sync.Mutex
	model  string
	script *scriptRunner
}

func NewProvider(model, script string) (*Provider, error) {
	runner, err := newRunner(script)
	if err != nil {
		return nil, err
	}
	return &Provider{model: model, script: runner}, nil
}

func (p *Provider) ChatCompletion(messages []ctxpkg.Message) (modelpkg.CompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	a := p.script.next()
	switch a.kind {
	case "ok":
		return modelpkg.CompletionResponse{
			Content:      emptyAs(a.arg, "dummy-ok"),
			InputTokens:  1,
			OutputTokens: 1,
		}, nil
	case "err":
		return modelpkg.CompletionResponse{}, fmt.Errorf("dummy provider error class=%s", emptyAs(a.arg, "provider_api"))
	case "sleep":
		ms, _ := strconv.Atoi(a.arg)
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return modelpkg.CompletionResponse{
			Content:      "dummy-after-sleep",
			InputTokens:  1,
			OutputTokens: 1,
		}, nil
	case "msg":
		return modelpkg.CompletionResponse{
			Content:      a.arg,
			InputTokens:  1,
			OutputTokens: 1,
		}, nil
	case "msgb64":
		raw, err := base64.StdEncoding.DecodeString(a.arg)
		if err != nil {
			return modelpkg.CompletionResponse{}, fmt.Errorf("dummy provider msgb64 decode failed: %w", err)
		}
		return modelpkg.CompletionResponse{
			Content:      string(raw),
			InputTokens:  1,
			OutputTokens: 1,
		}, nil
	default:
		return modelpkg.CompletionResponse{
			Content:      "dummy-ok",
			InputTokens:  1,
			OutputTokens: 1,
		}, nil
	}
}

func emptyAs(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
