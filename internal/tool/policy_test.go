package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAllowedRoots(t *testing.T) {
	roots, err := ParseAllowedRoots("/workspace,/state,/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("expected 2 unique roots, got %d", len(roots))
	}
	if roots[0] != "/workspace" || roots[1] != "/state" {
		t.Fatalf("unexpected roots: %+v", roots)
	}
}

func TestParseAllowedRoots_Invalid(t *testing.T) {
	if _, err := ParseAllowedRoots(""); err == nil {
		t.Fatal("expected empty error")
	}
	if _, err := ParseAllowedRoots("workspace"); err == nil {
		t.Fatal("expected relative path error")
	}
}

func TestResolveAllowedPath_BasicAndEscape(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(filepath.Join(allowed, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := NewPolicy(allowed, "")
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.ResolveAllowedPath("sub/file.txt", allowed)
	if err != nil {
		t.Fatalf("expected allow path, got err: %v", err)
	}
	want := filepath.Join(allowed, "sub", "file.txt")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("unexpected resolved path: got=%s want=%s", got, want)
	}

	if _, err := p.ResolveAllowedPath(filepath.Join(other, "x.txt"), allowed); err == nil {
		t.Fatal("expected outside-root rejection")
	}
}

func TestResolveAllowedPath_SymlinkEscape(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(allowed, "link")); err != nil {
		t.Fatal(err)
	}

	p, err := NewPolicy(allowed, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveAllowedPath(filepath.Join(allowed, "link", "x.txt"), allowed); err == nil {
		t.Fatal("expected symlink-escape rejection")
	}
}

func TestIsBashDenied(t *testing.T) {
	p, err := NewPolicy("/workspace", "rm -rf /,shutdown,reboot")
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsBashDenied("echo hi && rm -rf /tmp") {
		t.Fatal("expected deny")
	}
	if p.IsBashDenied("echo hello") {
		t.Fatal("did not expect deny")
	}
}
