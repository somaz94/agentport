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
	"strings"
	"testing"
	"time"

	"github.com/somaz94/agentport/internal/adapters/common"
	"github.com/somaz94/agentport/internal/harness"
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
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("language server did not start:\n%s", logs.String())
		}
		time.Sleep(300 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, base+"/exa.language_server_pb.LanguageServerService/GetAllSkills", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-codeium-csrf-token", "agentport-e2e")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetAllSkills returned %s", resp.Status)
	}
	var body struct {
		Skills []loadedSkill `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	out := map[string]loadedSkill{}
	for _, s := range body.Skills {
		out[s.Name] = s
	}
	return out
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
