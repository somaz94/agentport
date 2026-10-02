package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/frontmatter"
	"github.com/somaz94/agentport/internal/harness"
	"github.com/somaz94/agentport/internal/ir"
	"github.com/somaz94/agentport/internal/skilldir"
)

// LanguageServerEnv names the Antigravity desktop app's bundled language server. When set, the E2E
// test loads converted skills with the real loader instead of trusting the golden files alone.
const LanguageServerEnv = "AGENTPORT_AG_LANGUAGE_SERVER"

type loadedSkill struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	DisableModelInvocation bool   `json:"disableModelInvocation"`
	DisableSlashCommand    bool   `json:"disableSlashCommand"`
}

// TestAntigravityLoadsConvertedSkills writes every skill and command fixture as an Antigravity
// skill into a scratch config root, starts the app's language server against it, and checks what
// the loader parsed.
func TestAntigravityLoadsConvertedSkills(t *testing.T) {
	bin := os.Getenv(LanguageServerEnv)
	if bin == "" {
		t.Skipf("set %s to the Antigravity language server to run this test", LanguageServerEnv)
	}
	root := t.TempDir()
	skillsDir := filepath.Join(root, "gemini", "config", "skills")
	want := map[string]loadedSkill{}
	for name, from := range cases {
		item, err := ReadSkill(from, filepath.Join("testdata", "skills", name, "skill"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := Skill(item, harness.Antigravity, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(filepath.Join(skillsDir, item.Name), res.Files); err != nil {
			t.Fatal(err)
		}
		want[item.Name] = loadedSkill{
			Name:                   item.Name,
			DisableModelInvocation: !item.Invocation.ModelInvocable,
			DisableSlashCommand:    !item.Invocation.UserInvocable,
		}
	}
	for _, name := range commandCases {
		item := readCommandCase(t, name)
		res, err := Skill(item, harness.Antigravity, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(filepath.Join(skillsDir, item.Name), res.Files); err != nil {
			t.Fatal(err)
		}
		want[item.Name] = loadedSkill{
			Name:                   item.Name,
			DisableModelInvocation: !item.Invocation.ModelInvocable || common.UserOnly(item, Options{}),
			DisableSlashCommand:    !item.Invocation.UserInvocable,
		}
	}

	got := loadSkills(t, bin, root)
	for name, w := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("Antigravity did not load %s", name)
		case g.DisableModelInvocation != w.DisableModelInvocation || g.DisableSlashCommand != w.DisableSlashCommand:
			t.Errorf("%s loaded as %+v; want %+v", name, g, w)
		case g.Description == "":
			t.Errorf("%s loaded without a description", name)
		}
	}
}

func loadSkills(t *testing.T, bin, root string) map[string]loadedSkill {
	t.Helper()
	var body struct {
		Skills []loadedSkill `json:"skills"`
	}
	rpc(t, startServer(t, bin, root), "GetAllSkills", &body)
	out := map[string]loadedSkill{}
	for _, s := range body.Skills {
		out[s.Name] = s
	}
	return out
}

// startServer runs the language server against the config root and returns its base URL.
func startServer(t *testing.T, bin, root string) string {
	t.Helper()
	port, cdp := freePort(t), freePort(t)
	for _, d := range []string{"home", "ws"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "-standalone=true", "-gemini_dir="+filepath.Join(root, "gemini"),
		"-app_data_dir=antigravity", "-headless=true", "-csrf_token=agentport-e2e",
		fmt.Sprintf("-http_server_port=%d", port), fmt.Sprintf("-cdp_port=%d", cdp))
	cmd.Dir = filepath.Join(root, "ws")
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(root, "home"))
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if resp, err := client.Get(base + "/healthz"); err == nil {
			resp.Body.Close()
			return base
		}
		if time.Now().After(deadline) {
			t.Fatalf("language server did not start:\n%s", logs.String())
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// rpc calls method on the language server and decodes the JSON reply into out.
func rpc(t *testing.T, base, method string, out any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/exa.language_server_pb.LanguageServerService/"+method, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-codeium-csrf-token", "agentport-e2e")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %s", method, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

type agentScript struct {
	Name      string `json:"name"`
	ModelTier string `json:"modelTier"`
	Config    struct {
		AgentConfig struct {
			MixinConfig struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"mixinConfig"`
		} `json:"agentConfig"`
	} `json:"config"`
}

// TestAntigravityLoadsConvertedAgents writes every agent fixture as an Antigravity agent and checks
// that the loader enables each one. The agent picker, the only place the parsed tools and model
// show, lists main agents alone, so a copy of each with `mainAgent: true` checks those too.
func TestAntigravityLoadsConvertedAgents(t *testing.T) {
	bin := os.Getenv(LanguageServerEnv)
	if bin == "" {
		t.Skipf("set %s to the Antigravity language server to run this test", LanguageServerEnv)
	}
	root := t.TempDir()
	agentsDir := filepath.Join(root, "gemini", "config", "agents")
	wantTools := map[string][]string{}
	wantTier := map[string]string{}
	subagents := map[string]bool{}
	var written []string
	for name := range agentCases {
		res, err := Agent(readAgentCase(t, name), harness.Antigravity, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(agentsDir, res.Files); err != nil {
			t.Fatal(err)
		}
		doc, err := frontmatter.Parse(res.Files[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		agent, _ := doc.Scalar("name")
		written = append(written, agent, "picker-"+agent)
		if main, _ := doc.Scalar("mainAgent"); main == "false" {
			subagents[agent] = true
		}
		pick := "picker-" + agent
		doc.SetString("name", pick)
		doc.Set("mainAgent", common.BoolNode(true))
		data, err := doc.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err := skilldir.Write(agentsDir, []ir.Resource{{Path: pick + ".md", Data: data}}); err != nil {
			t.Fatal(err)
		}
		if n, ok := doc.Get("tools"); ok {
			for _, c := range n.Content {
				wantTools[pick] = append(wantTools[pick], c.Value)
			}
		}
		model, _ := doc.Scalar("model")
		// The reply omits the default tier, inherit.
		wantTier[pick] = map[string]string{"flash": "MODEL_TIER_FLASH"}[model]
	}

	base := startServer(t, bin, root)
	var states struct {
		States []struct {
			Type, Name, Status string
		} `json:"states"`
	}
	rpc(t, base, "GetCustomizationStates", &states)
	enabled := map[string]bool{}
	for _, s := range states.States {
		if s.Type == "REFRESH_CUSTOMIZATION_TYPE_AGENT" && s.Status == "STATUS_ENABLED" {
			enabled[s.Name] = true
		}
	}
	var scripts struct {
		AgentScripts []agentScript `json:"agentScripts"`
	}
	rpc(t, base, "GetAgentScripts", &scripts)
	picked := map[string]agentScript{}
	for _, s := range scripts.AgentScripts {
		picked[s.Name] = s
	}
	for _, name := range written {
		if !enabled[name] {
			t.Errorf("Antigravity did not enable %s", name)
		}
	}
	for pick, tools := range wantTools {
		agent := strings.TrimPrefix(pick, "picker-")
		s, ok := picked[pick]
		var got []string
		for _, tool := range s.Config.AgentConfig.MixinConfig.Tools {
			got = append(got, tool.Name)
		}
		if !ok || !slices.Equal(got, tools) {
			t.Errorf("%s loaded with tools %v; want %v", agent, got, tools)
		}
		if s.ModelTier != wantTier[pick] {
			t.Errorf("%s loaded with model tier %s; want %s", agent, s.ModelTier, wantTier[pick])
		}
	}
	for agent := range subagents {
		if _, ok := picked[agent]; ok {
			t.Errorf("%s was written with mainAgent: false but is in the agent picker", agent)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
