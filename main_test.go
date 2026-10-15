package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"
)

func TestInstallerCycleSkipsMissing(t *testing.T) {
	// only pacman present -> stays on pacman
	m := &model{installer: "pacman"}
	m.nextInstaller()
	if m.installer != "pacman" {
		t.Errorf("with no helpers, expected pacman, got %q", m.installer)
	}

	// both helpers present -> yay -> paru -> pacman
	m = &model{installer: "yay", hasYay: true, hasParu: true}
	var seen []string
	for i := 0; i < 4; i++ {
		m.nextInstaller()
		seen = append(seen, m.installer)
	}
	want := []string{"paru", "pacman", "yay", "paru"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("cycle = %v, want %v", seen, want)
	}

	// only paru present -> yay is skipped
	m = &model{installer: "pacman", hasParu: true}
	m.nextInstaller()
	if m.installer != "paru" {
		t.Errorf("expected paru, got %q", m.installer)
	}
}

func TestCanInstallRejectsAurWithPacman(t *testing.T) {
	m := &model{installer: "pacman"}
	if m.canInstall(true) {
		t.Error("pacman must not claim it can install an aur package")
	}
	if !m.statusErr || !strings.Contains(m.status, "yay or paru") {
		t.Errorf("expected an aur-needs-helper status, got %q (err=%v)", m.status, m.statusErr)
	}
	if !m.canInstall(false) {
		t.Error("pacman must be able to install repo packages")
	}

	m = &model{installer: "yay"}
	if !m.canInstall(true) {
		t.Error("yay must be able to install aur packages")
	}
}

func TestInstallArgsPerInstaller(t *testing.T) {
	m := &model{installer: "pacman"}
	got := strings.Join(m.installArgs("htop", false), " ")
	if got != "sudo pacman -S --noconfirm htop" {
		t.Errorf("pacman args = %q", got)
	}

	m = &model{installer: "yay"}
	got = strings.Join(m.installArgs("foo", true), " ")
	if !strings.HasPrefix(got, "yay -S ") || !strings.HasSuffix(got, " foo") {
		t.Errorf("yay args = %q", got)
	}
	if strings.Contains(got, "--answeredit y") {
		t.Errorf("yay args must not allow the edit menu: %q", got)
	}
}

func TestUpdatesMsgCountsAndStamp(t *testing.T) {
	m := &model{updLoading: true}
	msg := updatesMsg{
		items: []updateItem{
			{name: "rsync", aur: false},
			{name: "foo-git", aur: true},
		},
		checked: time.Now(),
	}
	m.Update(msg)
	if m.updLoading {
		t.Error("updatesMsg must clear the loading flag")
	}
	if !m.updStamp.Equal(msg.checked) {
		t.Error("updatesMsg must record when the check happened")
	}
	if !strings.Contains(m.status, "2 updates") {
		t.Errorf("status = %q", m.status)
	}
}

func TestRefreshTabMarksLoading(t *testing.T) {
	m := &model{tab: tabUpdates}
	if cmd := m.refreshTab(); cmd == nil {
		t.Error("updates tab: refreshTab must return a command")
	}
	if !m.updLoading {
		t.Error("updates tab: refreshTab must set updLoading")
	}
	if m.instLoading {
		t.Error("updates tab: refreshTab must not set instLoading")
	}
	if strings.Contains(m.status, "ready") {
		t.Errorf("updates tab: status must say what is refreshing, got %q", m.status)
	}

	m = &model{tab: tabUninstall}
	if cmd := m.refreshTab(); cmd == nil {
		t.Error("uninstall tab: refreshTab must return a command")
	}
	if !m.instLoading {
		t.Error("uninstall tab: refreshTab must set instLoading")
	}
	if m.updLoading {
		t.Error("uninstall tab: refreshTab must not set updLoading")
	}

	// the search tab has nothing to refresh
	m = &model{tab: tabSearch}
	if cmd := m.refreshTab(); cmd != nil {
		t.Error("search tab: refreshTab must be a no-op")
	}
}

func TestViewSmoke(t *testing.T) {
	sizes := []struct{ w, h int }{{100, 32}, {80, 24}, {60, 20}, {40, 14}}
	for _, s := range sizes {
		for _, tab := range []tabKind{tabSearch, tabUninstall, tabUpdates} {
			m := newModel()
			m.width, m.height = s.w, s.h
			m.tab = tab
			m.results = []pkg{
				{name: "mpv", repo: "extra", version: "1:0.38.0-1", desc: "a Free, Open Source Media Player"},
				{name: "mpv-mpris", repo: "aur", version: "0.9-1", desc: "mpris support", installed: true, aur: true},
			}
			m.installed = []pkg{{name: "mpv", version: "1:0.38.0-1"}, {name: "linux", version: "6.9.1", aur: false}}
			m.updates = []updateItem{{name: "ffmpeg", oldver: "6.0", newver: "7.0", aur: false}}
			m.busy = true
			m.actionTitle = "download mpv"
			m.logLines = []string{"resolving dependencies...", "looking for conflicting packages..."}

			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) > s.h {
				t.Errorf("size %dx%d tab %d: frame has %d lines (terminal height %d)",
					s.w, s.h, tab, len(lines), s.h)
			}
			if s.h >= 14 && !strings.Contains(out, "get my aur installer") {
				t.Errorf("size %dx%d tab %d: header missing", s.w, s.h, tab)
			}
		}
	}
}

func TestViewRows(t *testing.T) {
	m := newModel()
	m.width, m.height = 100, 32
	m.results = []pkg{
		{name: "mpv", repo: "extra", version: "1:0.38.0-1", desc: "a Free, Open Source Media Player"},
	}
	out := m.View()
	t.Log("\n" + out)
	if !strings.Contains(out, "a free, open source media player") {
		t.Error("description not lowercased")
	}
	if strings.Contains(out, "Free, Open") {
		t.Error("uppercase leaked into view")
	}
}
func TestParseSearch(t *testing.T) {
	out := "" +
		"extra/mpv 1:0.41.0-6 [installed]\n" +
		"    a free, open source media player\n" +
		"    second description line\n" +
		"core/perl 5.42.3-1\n" +
		"    a programming language\n"
	pkgs := parseSearch(out, false)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages: %#v", len(pkgs), pkgs)
	}
	if pkgs[0].name != "mpv" || pkgs[0].repo != "extra" || !pkgs[0].installed {
		t.Errorf("bad first package: %#v", pkgs[0])
	}
	if pkgs[0].desc != "a free, open source media player second description line" {
		t.Errorf("bad desc: %q", pkgs[0].desc)
	}
	if pkgs[1].name != "perl" || pkgs[1].repo != "core" || pkgs[1].installed {
		t.Errorf("bad second package: %#v", pkgs[1])
	}
}

func TestParseAurSearch(t *testing.T) {
	out := "yay 12.5.0-1\n    aur helper\n"
	pkgs := parseSearch(out, true)
	if len(pkgs) != 1 || !pkgs[0].aur || pkgs[0].repo != "aur" {
		t.Fatalf("bad aur parse: %#v", pkgs)
	}
}

func TestParseUpdates(t *testing.T) {
	out := "rsync 3.5.1-2.1 -> 3.5.1-3.1 [2026-10-01]\n" +
		"opencode-beta 2.0.28-1 -> 2.0.29-1 [14h24m]\n" +
		"\n"
	items := parseUpdates(out, false)
	if len(items) != 2 {
		t.Fatalf("got %d items: %#v", len(items), items)
	}
	if items[0].name != "rsync" || items[0].newver != "3.5.1-3.1" || items[0].oldver != "3.5.1-2.1" {
		t.Errorf("bad item: %#v", items[0])
	}
}

// collector receives streamed command output inside a real program.
type collector struct {
	lines []string
}

func (c *collector) Init() tea.Cmd { return nil }
func (c *collector) View() string  { return "" }
func (c *collector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m, ok := msg.(logLineMsg); ok {
		c.lines = append(c.lines, m.text)
	}
	return c, nil
}

func TestStartActionCmd(t *testing.T) {
	c := &collector{}
	p := tea.NewProgram(c, tea.WithInput(nil), tea.WithoutRenderer())
	go func() { _, _ = p.Run() }()
	defer p.Kill()

	msg := startActionCmd(p, "test action",
		[]string{"sh", "-c", "echo hello; echo oops >&2; exit 3"})()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("unexpected msg: %#v", msg)
	}
	if done.title != "test action" {
		t.Errorf("title = %q", done.title)
	}
	if done.err == nil || !strings.Contains(done.err.Error(), "exit status 3") {
		t.Errorf("err = %v", done.err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(c.lines) != 2 || c.lines[0] != "hello" || c.lines[1] != "oops" {
		t.Errorf("streamed lines: %#v", c.lines)
	}
}
