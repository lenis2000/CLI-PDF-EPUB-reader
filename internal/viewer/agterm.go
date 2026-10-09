package viewer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// agterm's sidebar and session picker show a session's custom name when it has
// one and only otherwise its live OSC title, so on a renamed session (a
// numbered tab always is) the OSC 2 title alone would leave the session list
// stuck at the page the document opened on. agtermReporter keeps the name
// current by renaming the session through agtermctl as the page changes. A
// rename is sticky, so what the session showed before is captured here and
// handed back on exit: a custom name by renaming to it, a label agterm derived
// by a blank rename, which clears the custom name again.
//
// Everything is best-effort: outside agterm, or without agtermctl on PATH,
// newAgtermReporter returns nil and every method is a no-op.
type agtermReporter struct {
	sessionID string
	origName  string
	origAuto  bool // origName was agterm's own label, not a custom name

	wake chan struct{} // buffered 1: a pending rename, coalescing rapid page turns
	done chan struct{} // closed on shutdown; the loop exits promptly, even mid-sleep
	wg   sync.WaitGroup
	stop sync.Once

	mu   sync.Mutex
	want string // newest name asked for
	sent string // newest name pushed to agterm
}

// agtermRenameInterval rate-limits renames: a held-down page key must not spawn
// a process per page.
const agtermRenameInterval = 250 * time.Millisecond

// sidebarDecoration matches the run a sidebar-numbering hook keeps in front of
// a custom name and rewrites on every tree change: the "3· " (formerly "⌘3 ")
// tab number, the "■ " / "□ " scratch marker and an old "[03]" counter,
// doubled ones included. The name handed back on exit keeps one number and one
// marker, which the hook corrects if they went stale: a restored name that lost
// its number and equals the cwd basename would pass for agterm's own label,
// which the hook never numbers.
var sidebarDecoration = regexp.MustCompile(`^(?:(?:\d+[·⋅│]|⌘\d+)(?:\s+|$)|[■□]\s*|\[\d+\]\s*)+`)

var (
	sidebarNumber = regexp.MustCompile(`\d+[·⋅│]|⌘\d+`)
	sidebarMarker = regexp.MustCompile(`[■□]`)
)

// tidySidebarName cuts a tree name's decoration down to one tab number and one
// scratch marker, dropping repeats and old counters.
func tidySidebarName(name string) string {
	name = strings.TrimSpace(name)
	deco := sidebarDecoration.FindString(name)
	prefix := ""
	if n := sidebarNumber.FindString(deco); n != "" {
		prefix = n + " "
	}
	if m := sidebarMarker.FindString(deco); m != "" {
		prefix += m + " "
	}
	return strings.TrimSpace(prefix + strings.TrimSpace(name[len(deco):]))
}

func newAgtermReporter() *agtermReporter {
	sessionID := os.Getenv("AGTERM_SESSION_ID")
	if os.Getenv("AGTERM_ENABLED") != "1" || sessionID == "" {
		return nil
	}
	// One name covers the whole session, so a reader in a split or scratch pane
	// would label the session after a document while its main pane is running
	// something else entirely. Only the main pane speaks for the session.
	if pane := os.Getenv("AGTERM_PANE"); pane != "" && pane != "left" {
		return nil
	}
	if _, err := exec.LookPath("agtermctl"); err != nil {
		return nil
	}
	origName, origAuto := agtermSessionName(sessionID)
	r := &agtermReporter{
		sessionID: sessionID,
		origName:  origName,
		origAuto:  origAuto,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	r.wg.Add(1)
	go r.loop()
	return r
}

// report asks for the session to be named title. Called from the render path, so
// it must not block: the rename happens on the reporter's goroutine.
func (r *agtermReporter) report(title string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.want = title
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default: // a rename is already pending; it will pick up the newer name
	}
}

// close stops renaming, waits out any rename in flight, and then restores the
// name the session had at startup — the wait guarantees the restore lands last.
// Both the signal handler and Run's defer call it; the second call is a no-op.
func (r *agtermReporter) close() {
	if r == nil {
		return
	}
	r.stop.Do(func() {
		close(r.done)
		r.wg.Wait()
		r.restore()
	})
}

// restore puts the original name back; skipped when the capture at startup came
// up empty, leaving an unknown name alone rather than guessing at one. A label
// agterm derived is handed back with a blank rename: renaming to it would pin
// it as a custom name, and the label would stop following the live title.
func (r *agtermReporter) restore() {
	if r.origName == "" {
		return
	}
	if r.origAuto {
		r.rename("")
		return
	}
	r.rename(r.origName)
}

func (r *agtermReporter) loop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.done:
			return
		case <-r.wake:
		}
		r.mu.Lock()
		title := r.want
		r.mu.Unlock()
		if title == "" || title == r.sent {
			continue
		}
		r.rename(title)
		r.sent = title

		// Pages turned during the rename collapse into one more pass.
		select {
		case <-r.done:
			return
		case <-time.After(agtermRenameInterval):
		}
		r.mu.Lock()
		pending := r.want != r.sent
		r.mu.Unlock()
		if pending {
			select {
			case r.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (r *agtermReporter) rename(name string) {
	exec.Command("agtermctl", "session", "rename", name, "--target", r.sessionID).Run()
}

// agtermSessionName reads a session's current name out of the tree, and whether
// agterm derived it rather than a rename setting it. Returns "" when agterm
// cannot be reached or does not know the session, which leaves the name
// untouched on exit rather than guessing at one.
func agtermSessionName(sessionID string) (string, bool) {
	// The tree covers one window, the frontmost unless one is named.
	args := []string{"tree", "--json"}
	if w := strings.TrimSpace(os.Getenv("AGTERM_WINDOW_ID")); w != "" {
		args = append(args, "--window", w)
	}
	out, err := exec.Command("agtermctl", args...).Output()
	if err != nil {
		return "", false
	}
	var tree struct {
		Result struct {
			Tree struct {
				Workspaces []struct {
					Sessions []agtermTreeSession `json:"sessions"`
				} `json:"workspaces"`
			} `json:"tree"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &tree) != nil {
		return "", false
	}
	for _, ws := range tree.Result.Tree.Workspaces {
		for _, s := range ws.Sessions {
			if strings.EqualFold(s.ID, sessionID) {
				return tidySidebarName(s.Name), s.autoNamed()
			}
		}
	}
	return "", false
}

// agtermTreeSession is the part of a tree session node the name logic reads.
type agtermTreeSession struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Cwd          string `json:"cwd"`
	SplitFocused bool   `json:"splitFocused"`
}

// autoNamed reports whether the session has no custom name: agterm then labels
// it with the focused pane's OSC title, or with that pane's cwd basename while
// there is no title. The tree carries only the main pane's title and cwd, so a
// session focused on its split pane never counts.
func (s agtermTreeSession) autoNamed() bool {
	name := strings.TrimSpace(s.Name)
	if name == "" || s.SplitFocused {
		return false
	}
	if title := strings.TrimSpace(s.Title); title != "" {
		return name == title
	}
	if s.Cwd == "" {
		return name == "~"
	}
	return name == filepath.Base(s.Cwd)
}
