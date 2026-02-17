package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/stupiduntilnot/autonous/internal/db"
)

// TestE2E_TreeOutput builds the binary and runs it against a seeded database,
// verifying tree and JSON output modes, --id, -L, and --no-payload flags.
func TestE2E_TreeOutput(t *testing.T) {
	// Build the binary.
	binPath := t.TempDir() + "/event-tree"
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Seed a database.
	dbPath := t.TempDir() + "/e2e.db"
	database, err := db.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(database); err != nil {
		t.Fatal(err)
	}
	seedUnifiedTree(t, database)
	database.Close()

	t.Run("default_full_tree", func(t *testing.T) {
		out, err := exec.Command(binPath, "--db", dbPath).CombinedOutput()
		if err != nil {
			t.Fatalf("exit error: %v\n%s", err, out)
		}
		output := string(out)

		// Should contain the full tree.
		for _, want := range []string{
			"process.started", "revision.promoted", "worker.spawned",
			"agent.started", "turn.started", "agent.completed", "worker.exited",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("expected %q in output:\n%s", want, output)
			}
		}

		// Should have tree-drawing characters.
		if !strings.Contains(output, "├──") {
			t.Errorf("expected tree characters:\n%s", output)
		}
	})

	t.Run("depth_limit_L2", func(t *testing.T) {
		out, err := exec.Command(binPath, "--db", dbPath, "-L", "2").CombinedOutput()
		if err != nil {
			t.Fatalf("exit error: %v\n%s", err, out)
		}
		output := string(out)

		// Should NOT contain agent-level events.
		if strings.Contains(output, "agent.started") {
			t.Errorf("agent.started should be hidden at -L 2:\n%s", output)
		}
		// Should show truncation.
		if !strings.Contains(output, "[...]") {
			t.Errorf("expected [...] for truncated nodes:\n%s", output)
		}
	})

	t.Run("id_flag_subtree", func(t *testing.T) {
		// --id 5 should show agent.started subtree (id=5 is agent.started).
		out, err := exec.Command(binPath, "--db", dbPath, "--id", "5").CombinedOutput()
		if err != nil {
			t.Fatalf("exit error: %v\n%s", err, out)
		}
		output := string(out)

		// Root should be agent.started.
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if !strings.Contains(lines[0], "agent.started") {
			t.Errorf("expected agent.started as root:\n%s", output)
		}
		// Should NOT contain supervisor-level events.
		if strings.Contains(output, "revision.promoted") {
			t.Errorf("revision.promoted should not appear in agent subtree:\n%s", output)
		}
	})

	t.Run("json_output", func(t *testing.T) {
		out, err := exec.Command(binPath, "--db", dbPath, "-json").CombinedOutput()
		if err != nil {
			t.Fatalf("exit error: %v\n%s", err, out)
		}

		var je jsonEvent
		if err := json.Unmarshal(out, &je); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if je.EventType != "process.started" {
			t.Errorf("expected process.started, got %s", je.EventType)
		}
		if len(je.Children) != 4 {
			t.Errorf("expected 4 children, got %d", len(je.Children))
		}
	})

	t.Run("json_with_depth_limit", func(t *testing.T) {
		out, err := exec.Command(binPath, "--db", dbPath, "-json", "-L", "2").CombinedOutput()
		if err != nil {
			t.Fatalf("exit error: %v\n%s", err, out)
		}

		var je jsonEvent
		if err := json.Unmarshal(out, &je); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		for _, child := range je.Children {
			if len(child.Children) > 0 {
				t.Errorf("expected no grandchildren at -L 2: %s has %d", child.EventType, len(child.Children))
			}
		}
	})

	t.Run("no_payload", func(t *testing.T) {
		out, err := exec.Command(binPath, "--db", dbPath, "-no-payload").CombinedOutput()
		if err != nil {
			t.Fatalf("exit error: %v\n%s", err, out)
		}
		output := string(out)

		// Should have event types but not payload values.
		if !strings.Contains(output, "process.started") {
			t.Errorf("expected process.started:\n%s", output)
		}
		if strings.Contains(output, "role=supervisor") {
			t.Errorf("expected no payload with --no-payload:\n%s", output)
		}
	})
}
