package loss

import (
	"bytes"
	"strings"
	"testing"
)

func TestLossy(t *testing.T) {
	var r Report
	r.Add("name", Mapped, "")
	r.Add("description", Transformed, "quoted")
	if r.Lossy() {
		t.Error("mapped and transformed entries made the report lossy")
	}
	for _, s := range []Status{Approximated, Dropped, Warn} {
		lossy := Report{Entries: []Entry{{Field: "x", Status: s}}}
		if !lossy.Lossy() {
			t.Errorf("%s is not lossy", s)
		}
	}
	if AnyLossy([]Report{r}) || !AnyLossy([]Report{r, {Entries: []Entry{{Status: Dropped}}}}) {
		t.Error("AnyLossy disagrees with Lossy")
	}
}

func TestAddFormats(t *testing.T) {
	var r Report
	r.Addf("tools", Dropped, "no allowlist in %s", "Codex")
	r.Add("model", Mapped, "100% literal")
	if r.Entries[0].Detail != "no allowlist in Codex" || r.Entries[1].Detail != "100% literal" {
		t.Errorf("details = %q, %q", r.Entries[0].Detail, r.Entries[1].Detail)
	}
}

func TestWriters(t *testing.T) {
	reports := []Report{
		{Source: "agents/a.md", Target: "a.toml", Entries: []Entry{{"tools", Dropped, "no allowlist"}, {"name", Mapped, ""}}},
		{Source: "skills/b", Target: "skills/b", Entries: []Entry{{"name", Mapped, ""}}},
	}
	var text bytes.Buffer
	if err := WriteText(&text, reports); err != nil {
		t.Fatal(err)
	}
	// Each report is aligned on its own; the blank line between them ends a tabwriter block.
	want := "agents/a.md -> a.toml\n  dropped  tools  no allowlist\n  mapped   name   \n\nskills/b -> skills/b\n  mapped  name  \n"
	if text.String() != want {
		t.Errorf("WriteText =\n%q\nwant\n%q", text.String(), want)
	}

	var js bytes.Buffer
	if err := WriteJSON(&js, reports); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"status": "dropped"`) || strings.Contains(js.String(), `"detail": ""`) {
		t.Errorf("WriteJSON =\n%s", js.String())
	}
	js.Reset()
	if err := WriteJSON(&js, nil); err != nil || js.String() != "[]\n" {
		t.Errorf("WriteJSON(nil) = %q, %v; want []", js.String(), err)
	}
	js.Reset()
	if err := WriteJSON(&js, []Report{{Source: "a", Target: "b"}}); err != nil || !strings.Contains(js.String(), `"entries": []`) {
		t.Errorf("a report without entries = %s, %v; want an empty array, not null", js.String(), err)
	}
}
