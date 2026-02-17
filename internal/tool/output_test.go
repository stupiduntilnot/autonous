package tool

import (
	"strings"
	"testing"
)

func TestApplyOutputLimits_ByLines(t *testing.T) {
	in := "a\nb\nc\nd"
	out, tl, tb := ApplyOutputLimits(in, Limits{MaxLines: 2, MaxBytes: 0})
	if out != "a\nb" {
		t.Fatalf("unexpected output: %q", out)
	}
	if !tl || tb {
		t.Fatalf("unexpected flags tl=%v tb=%v", tl, tb)
	}
}

func TestApplyOutputLimits_ByBytes(t *testing.T) {
	in := "abcdef"
	out, tl, tb := ApplyOutputLimits(in, Limits{MaxLines: 0, MaxBytes: 3})
	if out != "abc" {
		t.Fatalf("unexpected output: %q", out)
	}
	if tl || !tb {
		t.Fatalf("unexpected flags tl=%v tb=%v", tl, tb)
	}
}

func TestApplyOutputLimits_Both(t *testing.T) {
	in := strings.Repeat("x", 10) + "\n" + strings.Repeat("y", 10) + "\n" + strings.Repeat("z", 10)
	out, tl, tb := ApplyOutputLimits(in, Limits{MaxLines: 2, MaxBytes: 15})
	if !tl || !tb {
		t.Fatalf("expected both truncations, got tl=%v tb=%v", tl, tb)
	}
	if len([]byte(out)) > 15 {
		t.Fatalf("output not byte-truncated: len=%d", len([]byte(out)))
	}
}

func TestBuildCursor(t *testing.T) {
	c := BuildCursor("/workspace/a.txt", 128)
	if c == "" {
		t.Fatal("empty cursor")
	}
	if !strings.Contains(c, ":128") {
		t.Fatalf("unexpected cursor format: %s", c)
	}
}
