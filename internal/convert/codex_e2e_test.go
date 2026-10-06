package convert

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/paths"
	"github.com/somaz94/agentport/internal/skilldir"
)

// CodexEnv names a Codex CLI binary. When set, the E2E test loads converted skills and agents with
// its app-server instead of trusting the golden files alone.
const CodexEnv = "AGENTPORT_CODEX_BIN"

// canaryRole is a role file Codex must reject: without its warning, the absence of warnings for the
// converted roles would prove nothing. It sorts after every fixture role, so its warning arriving
// before the skills/list reply also covers the roles Codex reads first.
const canaryRole = "zz-agentport-e2e-canary.toml"

type codexSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
}

// TestCodexLoadsConverted writes every skill, command and agent fixture in Codex's format into a
// scratch home, starts Codex's app-server against it, and checks what it loaded. Codex lists skills
// but not agent roles; a role it cannot load is reported as a configuration warning instead.
func TestCodexLoadsConverted(t *testing.T) {
	bin := os.Getenv(CodexEnv)
	if bin == "" {
		t.Skipf("set %s to a Codex CLI binary to run this test", CodexEnv)
	}
	layout, err := paths.For(harness.Codex)
	if err != nil {
		t.Fatal(err)
	}
	skillsRel, err := layout.Dir(paths.ScopeUser, ir.KindSkill)
	if err != nil {
		t.Fatal(err)
	}
	agentsRel, err := layout.Dir(paths.ScopeUser, ir.KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	codexRel, err := layout.Root(paths.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	skillsDir, agentsDir := filepath.Join(home, skillsRel), filepath.Join(home, agentsRel)

	want := map[string]string{}
	writeSkill := func(item *ir.Item) {
		t.Helper()
		res, err := Skill(item, harness.Codex, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(filepath.Join(skillsDir, item.Name), res.Files); err != nil {
			t.Fatal(err)
		}
		for _, f := range res.Files {
			if f.Path != "SKILL.md" {
				continue
			}
			doc, err := frontmatter.Parse(f.Data)
			if err != nil {
				t.Fatal(err)
			}
			want[item.Name], _ = doc.Scalar("description")
		}
	}
	for name, from := range cases {
		item, err := ReadSkill(from, filepath.Join("testdata", "skills", name, "skill"))
		if err != nil {
			t.Fatal(err)
		}
		writeSkill(item)
	}
	for _, name := range commandCases {
		writeSkill(readCommandCase(t, name))
	}
	var roleFiles []string
	for name := range agentCases {
		res, err := Agent(readAgentCase(t, name), harness.Codex, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(agentsDir, res.Files); err != nil {
			t.Fatal(err)
		}
		for _, f := range res.Files {
			roleFiles = append(roleFiles, string(filepath.Separator)+f.Path)
		}
	}
	canary := []byte("name = \"zz-agentport-e2e-canary\"\ndescription = \"canary\"\ndeveloper_instructions = \"x\"\nnot_a_role_key = true\n")
	if err := os.WriteFile(filepath.Join(agentsDir, canaryRole), canary, 0o644); err != nil {
		t.Fatal(err)
	}

	skills, warnings := loadCodex(t, bin, home, filepath.Join(home, codexRel))
	got := map[string]codexSkill{}
	for _, s := range skills {
		got[s.Name] = s
	}
	for name, desc := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("Codex did not load %s", name)
		case g.Scope != "user" || !g.Enabled:
			t.Errorf("%s loaded with scope %q, enabled %v; want user, true", name, g.Scope, g.Enabled)
		case g.Description != desc:
			t.Errorf("%s loaded with description %q; want %q", name, g.Description, desc)
		}
	}
	var canaryWarned bool
	for _, w := range warnings {
		var named []string
		for _, f := range roleFiles {
			if strings.Contains(w, f) {
				named = append(named, f)
			}
		}
		switch {
		case len(named) > 0:
			t.Errorf("Codex rejected %s: %s", strings.Join(named, ", "), w)
		case strings.Contains(w, string(filepath.Separator)+canaryRole):
			canaryWarned = true
		default:
			t.Errorf("Codex warned: %s", w)
		}
	}
	if !canaryWarned {
		t.Errorf("Codex did not warn about %s, so its silence about the converted roles proves nothing", canaryRole)
	}
}

// loadCodex starts Codex's app-server against home and codexHome, asks it to list skills, and
// returns them with every configuration warning reported before the reply.
func loadCodex(t *testing.T, bin, home, codexHome string) ([]codexSkill, []string) {
	t.Helper()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Dir = ws
	cmd.WaitDelay = 5 * time.Second
	// The developer's own Codex and OpenAI settings must not leak into the run.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CODEX_") && !strings.HasPrefix(kv, "OPENAI_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+home, "CODEX_HOME="+codexHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// A child of Codex that keeps stdout open would otherwise block the read past the timeout.
	stop := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	t.Cleanup(func() { stop() })
	var logs lockedBuffer
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })

	enc, dec := json.NewEncoder(stdin), json.NewDecoder(stdout)
	send := func(msg map[string]any) {
		t.Helper()
		if err := enc.Encode(msg); err != nil {
			t.Fatalf("app-server: %v\n%s", err, logs.String())
		}
	}
	var warnings []string
	await := func(id int) json.RawMessage {
		t.Helper()
		for {
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := dec.Decode(&msg); err != nil {
				t.Fatalf("app-server, awaiting reply %d: %v (%v)\n%s", id, err, ctx.Err(), logs.String())
			}
			failed := len(msg.Error) > 0 && string(msg.Error) != "null"
			switch {
			case msg.Method == "configWarning":
				var w struct{ Summary, Details string }
				_ = json.Unmarshal(msg.Params, &w)
				warnings = append(warnings, strings.TrimSpace(w.Summary+" "+w.Details))
				continue
			case msg.Method != "":
				if len(msg.ID) > 0 {
					t.Logf("ignoring app-server request %s", msg.Method)
				}
				continue
			case string(msg.ID) == strconv.Itoa(id):
				if failed {
					t.Fatalf("app-server request %d failed: %s", id, msg.Error)
				}
				return msg.Result
			case failed && (len(msg.ID) == 0 || string(msg.ID) == "null"):
				t.Fatalf("app-server error while awaiting reply %d: %s", id, msg.Error)
			}
		}
	}

	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"clientInfo": map[string]string{"name": "agentport-e2e", "version": "0"}}})
	await(1)
	send(map[string]any{"method": "initialized"})
	send(map[string]any{"id": 2, "method": "skills/list", "params": map[string]any{"cwds": []string{ws}, "forceReload": true}})
	var res struct {
		Data []struct {
			Skills []codexSkill      `json:"skills"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(await(2), &res); err != nil {
		t.Fatal(err)
	}
	var skills []codexSkill
	for _, d := range res.Data {
		for _, e := range d.Errors {
			t.Errorf("Codex could not load a skill: %s", e)
		}
		skills = append(skills, d.Skills...)
	}
	return skills, warnings
}

// lockedBuffer collects a child process's output, which exec copies from its own goroutine while a
// failing test may already be reading it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
