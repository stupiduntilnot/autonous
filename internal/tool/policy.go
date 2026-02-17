package tool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Policy enforces allowlist and command restrictions for tools.
type Policy struct {
	AllowedRoots []string
	BashDenylist []string
}

func NewPolicy(allowedRootsCSV, bashDenylistCSV string) (*Policy, error) {
	roots, err := ParseAllowedRoots(allowedRootsCSV)
	if err != nil {
		return nil, err
	}
	return &Policy{
		AllowedRoots: roots,
		BashDenylist: parseCSV(bashDenylistCSV),
	}, nil
}

func ParseAllowedRoots(raw string) ([]string, error) {
	items := parseCSV(raw)
	if len(items) == 0 {
		return nil, fmt.Errorf("AUTONOUS_TOOL_ALLOWED_ROOTS is empty")
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !filepath.IsAbs(it) {
			return nil, fmt.Errorf("allowlist root must be absolute path: %s", it)
		}
		clean := filepath.Clean(it)
		if real, err := filepath.EvalSymlinks(clean); err == nil {
			clean = filepath.Clean(real)
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("AUTONOUS_TOOL_ALLOWED_ROOTS has no valid roots")
	}
	return out, nil
}

// ResolveAllowedPath validates the input path against allowlist and returns a safe absolute path.
func (p *Policy) ResolveAllowedPath(path string, baseDir string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	if baseDir == "" {
		baseDir = "/workspace"
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	candidate = filepath.Clean(candidate)

	resolved, err := resolvePathForCheck(candidate)
	if err != nil {
		return "", err
	}
	for _, root := range p.AllowedRoots {
		if hasPathPrefix(resolved, root) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("path outside allowlist: %s", path)
}

func (p *Policy) IsBashDenied(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, rule := range p.BashDenylist {
		if rule == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(rule)) {
			return true
		}
	}
	return false
}

func resolvePathForCheck(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(real), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// If path does not exist (write case), validate its nearest existing parent.
	dir := filepath.Dir(path)
	for {
		realDir, dirErr := filepath.EvalSymlinks(dir)
		if dirErr == nil {
			leaf := strings.TrimPrefix(path, dir)
			leaf = strings.TrimPrefix(leaf, string(filepath.Separator))
			return filepath.Clean(filepath.Join(realDir, leaf)), nil
		}
		if !errors.Is(dirErr, os.ErrNotExist) {
			return "", fmt.Errorf("failed to resolve parent path: %w", dirErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing parent for path: %s", path)
		}
		dir = parent
	}
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func hasPathPrefix(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}
