package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAgtermctl puts an agtermctl on PATH that answers `tree --json` with
// tree, logging its arguments to trees, and logs the name of every
// `session rename`, one per line. It returns the rename log's path.
func stubAgtermctl(t *testing.T, tree string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tree.json"), []byte(tree), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1 $2" in
"tree --json") printf '%s\n' "$*" >> "$AGTERMCTL_STUB/trees"; cat "$AGTERMCTL_STUB/tree.json" ;;
"session rename") printf '%s\n' "$3" >> "$AGTERMCTL_STUB/renames" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "agtermctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGTERMCTL_STUB", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("AGTERM_ENABLED", "1")
	t.Setenv("AGTERM_SESSION_ID", "test-session")
	t.Setenv("AGTERM_PANE", "left")
	t.Setenv("AGTERM_WINDOW_ID", "")
	return filepath.Join(dir, "renames")
}

// TestAgtermRestore pins what the session gets back on exit: a custom name is
// renamed back with its sidebar number and scratch marker, tidied of repeats,
// while a label agterm derived from the title or the cwd is cleared with a
// blank rename so it is not pinned as a custom name. A session focused on its
// split cannot be told apart, so it keeps the restore by name.
func TestAgtermRestore(t *testing.T) {
	for _, tc := range []struct {
		desc, node, want string
	}{
		{"custom name", `"name":"3· paper","title":"zsh","cwd":"/work/paper"`, "3· paper"},
		{"scratch marker", `"name":"3· □ paper","title":"zsh","cwd":"/work/paper"`, "3· □ paper"},
		{"doubled decoration", `"name":"3· □ 3· □ [04] paper","title":"zsh","cwd":"/work/paper"`, "3· □ paper"},
		{"numbered name equal to cwd basename", `"name":"1· paper","cwd":"/work/paper"`, "1· paper"},
		{"unnumbered custom name", `"name":"X custom","title":"zsh","cwd":"/work/paper"`, "X custom"},
		{"title", `"name":"dv a.pdf","title":"dv a.pdf","cwd":"/work/paper"`, ""},
		{"cwd basename", `"name":"paper","cwd":"/work/paper"`, ""},
		{"custom name equal to cwd basename", `"name":"paper","title":"zsh","cwd":"/work/paper"`, "paper"},
		{"split focused", `"name":"vim","title":"zsh","cwd":"/work/paper","splitFocused":true`, "vim"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			renames := stubAgtermctl(t, `{"result":{"tree":{"workspaces":[{"sessions":[{"id":"TEST-SESSION",`+tc.node+`}]}]}}}`)
			r := newAgtermReporter()
			if r == nil {
				t.Fatal("reporter should start with agtermctl on PATH")
			}
			r.report("📕 a.pdf 1/3")
			r.close()

			data, err := os.ReadFile(renames)
			if err != nil {
				t.Fatalf("no rename was issued: %v", err)
			}
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			if got := lines[len(lines)-1]; got != tc.want {
				t.Fatalf("restore renamed to %q, want %q (renames %q)", got, tc.want, lines)
			}
		})
	}
}

// TestAgtermNameReadsOwnWindow pins the tree read to the window dv runs in:
// without --window agtermctl reads the frontmost window, which need not hold
// this session.
func TestAgtermNameReadsOwnWindow(t *testing.T) {
	renames := stubAgtermctl(t, `{"result":{"tree":{"workspaces":[{"sessions":[{"id":"TEST-SESSION","name":"3· paper","title":"zsh"}]}]}}}`)
	t.Setenv("AGTERM_WINDOW_ID", "WINDOW-1")

	if name, _ := agtermSessionName("test-session"); name != "3· paper" {
		t.Fatalf("tree read returned %q", name)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(renames), "trees"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "tree --json --window WINDOW-1" {
		t.Fatalf("tree read should name dv's window, got %q", got)
	}
}
