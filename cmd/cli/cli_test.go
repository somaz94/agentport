package cli

import (
	"bytes"
	"encoding/json"
	"os"
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

func TestExecute(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()

	os.Args = []string{"agentport", "version"}
	if err := Execute(); err != nil {
		t.Errorf("Execute(version) = %v", err)
	}
	os.Args = []string{"agentport", "no-such-command"}
	if err := Execute(); err == nil {
		t.Error("Execute(no-such-command) succeeded")
	}
}
