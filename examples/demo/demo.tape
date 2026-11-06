# Runs the real agentport inside the vhs container on the synthetic skill in
# examples/demo/release-notes, copied into a throwaway HOME.
# To regenerate, from the repo root:
#   GOOS=linux go build -o bin/linux/agentport ./cmd/agentport
#   docker run --rm -v "$PWD:/vhs" ghcr.io/charmbracelet/vhs examples/demo/demo.tape
Output examples/demo/demo.gif

Set Shell bash
Set FontSize 16
Set Width 1280
Set Height 640
Set Padding 16
Set TypingSpeed 35ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PATH=/vhs/bin/linux:$PATH HOME=/tmp/home && mkdir -p ~/.claude/skills && cp -r examples/demo/release-notes ~/.claude/skills/ && cd ~"
Enter
Type "PS1='$ ' && clear"
Enter
Sleep 500ms
Show

Type "cat ~/.claude/skills/release-notes/SKILL.md"
Enter
Sleep 3s
Type "agentport convert ~/.claude/skills/release-notes --to codex"
Enter
Sleep 6s
