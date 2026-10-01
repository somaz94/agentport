package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somaz94/agentport/internal/crosswalk"
	"github.com/somaz94/agentport/internal/golden"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestVersion(t *testing.T) {
	out, err := run(t, "version")
	if err != nil || out != "agentport dev (commit: none, built: unknown)\n" {
		t.Errorf("version = %q, %v", out, err)
	}
	if _, err := run(t, "version", "extra"); err == nil {
		t.Error("version accepted an argument")
	}
	out, err = run(t, "version", "-o", "json")
	var v map[string]string
	if err != nil || json.Unmarshal([]byte(out), &v) != nil || v["version"] != "dev" {
		t.Errorf("version -o json = %q, %v", out, err)
	}
}

func TestMapGolden(t *testing.T) {
	cases := map[string][]string{
		"map-all.golden":   {"map"},
		"map-agent.golden": {"map", "agent"},
		"map-json.golden":  {"map", "skill", "-o", "json"},
	}
	for file, args := range cases {
		t.Run(file, func(t *testing.T) {
			out, err := run(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			golden.Assert(t, filepath.Join("testdata", file), []byte(out))
		})
	}
}

func TestMapJSONIsComplete(t *testing.T) {
	out, err := run(t, "map", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []crosswalk.Row
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != len(crosswalk.Rows) {
		t.Errorf("map -o json decoded %d rows, %v", len(rows), err)
	}
}

func TestMapErrors(t *testing.T) {
	if _, err := run(t, "map", "workflow"); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("map workflow error = %v", err)
	}
	if _, err := run(t, "map", "a", "b"); err == nil {
		t.Error("map accepted two arguments")
	}
	if _, err := run(t, "map", "-o", "yaml"); err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Errorf("-o yaml error = %v", err)
	}
}

func TestRunMainExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMain([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Errorf("version exit = %d", code)
	}
	if code := runMain([]string{"no-such-command"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("unknown command exit = %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	lossy := filepath.Join("..", "..", "internal", "convert", "testdata", "skills", "claude-basic", "skill")
	if code := runMain([]string{"convert", lossy, "--from", "claude", "--to", "codex", "--strict"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "lossy") {
		t.Errorf("--strict on a lossy conversion exit = %d, stderr %q", code, stderr.String())
	}
	if (&exitError{code: 2}).Error() != "exit 2" {
		t.Error("exitError without a cause does not name its code")
	}
}
