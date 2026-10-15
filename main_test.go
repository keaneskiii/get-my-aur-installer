package main

import (
	"encoding/json"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"regexp"
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

func TestSingleThemeIsTheOriginalDarkOne(t *testing.T) {
	// force a colour profile so the palette is actually visible in the
	// rendered output, then put the profile back afterwards
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())

	m := newModel()
	// the app has one look: the original catppuccin dark window
	if themePalette.bg != "#1e1e2e" {
		t.Errorf("background = %q, want #1e1e2e", themePalette.bg)
	}
	if themePalette.fg != "#cdd6f4" {
		t.Errorf("foreground = %q, want #cdd6f4", themePalette.fg)
	}
	if got := m.st.bg.Render("x"); !strings.Contains(got, "48;2;30;30;46") {
		t.Errorf("every element must paint the window colour, got %q", got)
	}
	if got := m.st.title.Render("x"); !strings.Contains(got, "38;2;") {
		t.Errorf("styles must still carry the accent colours, got %q", got)
	}

	// cycling the installer must not restyle anything: there is no
	// theme option any more
	before := m.st.title.Render("x")
	m.cycleSetting(0)
	if after := m.st.title.Render("x"); after != before {
		t.Error("cycling the installer must not restyle the ui")
	}
	if len(m.settingsRows()) != 1 || m.settingsRows()[0] != "install with" {
		t.Errorf("settings rows = %v, want just the installer", m.settingsRows())
	}
}

func TestSettingsRowsRoundTrip(t *testing.T) {
	m := newModel()
	rows := m.settingsRows()
	if len(rows) != 1 || rows[0] != "install with" {
		t.Fatalf("settings rows = %v", rows)
	}
	// the single row reports the installer
	if m.settingValue(0) != m.installer {
		t.Errorf("row 0 should report the installer, got %q", m.settingValue(0))
	}

	// the settings tab has no action buttons and no text input
	m.tab = tabSettings
	if got := m.buttonsFor(); got != nil {
		t.Errorf("settings tab buttons = %v, want none", got)
	}
	if m.hasInput() {
		t.Error("settings tab must not claim an input field")
	}
}

func TestSettingsOptionsAreAllThreeInstallers(t *testing.T) {
	m := newModel()
	// the installers row must offer the full choice even when a helper is
	// absent, so the picker reads as a real option list
	if got := m.settingOptions(0); len(got) != 3 ||
		got[0] != "pacman" || got[1] != "yay" || got[2] != "paru" {
		t.Errorf("installer options = %v", got)
	}

	// pacman is always available; a missing helper is not, and is skipped
	// by the cycler but still listed in the row
	if !m.settingAvailable(0, "pacman") {
		t.Error("pacman must always be available")
	}

	m.hasYay, m.hasParu = false, false
	// point the selection at a tool that is always there, otherwise the
	// current value counts as available by definition
	m.installer = "pacman"
	if m.settingAvailable(0, "yay") || m.settingAvailable(0, "paru") {
		t.Error("a helper that is not installed must not be available")
	}
	// the cycler only ever lands on installed tools
	m.installer = "pacman"
	for i := 0; i < 4; i++ {
		m.nextInstaller()
		if !m.settingAvailable(0, m.installer) {
			t.Fatalf("cycled onto unavailable installer %q", m.installer)
		}
	}
}

func TestSettingsRowsMarkMissingHelpers(t *testing.T) {
	m := newModel()
	m.tab = tabSettings
	m.installer = "pacman"
	m.hasYay, m.hasParu = false, false

	_, _, rows := m.listContent()
	if len(rows) != 1 {
		t.Fatalf("settings rows = %v", rows)
	}
	// every option stays visible; the absent ones are labelled, the
	// usable one is marked as the selection
	if !strings.Contains(rows[0], "yay (missing)") ||
		!strings.Contains(rows[0], "paru (missing)") {
		t.Errorf("missing helpers must be labelled: %q", rows[0])
	}
	if !strings.Contains(rows[0], "[ pacman ]") {
		t.Errorf("pacman must render as the selection: %q", rows[0])
	}
}

func TestFunctionKeysSwitchTabs(t *testing.T) {
	m := newModel()
	want := map[string]tabKind{
		"f1": tabSearch,
		"f2": tabUninstall,
		"f3": tabUpdates,
		"f4": tabSettings,
	}
	for key, kind := range want {
		got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		mm := got.(*model)
		if mm.tab != kind {
			t.Errorf("%s switched to %d, want %d", key, mm.tab, kind)
		}
	}
	// a key that is not bound must leave the tab alone (the map above is
	// unordered, so pin the tab first)
	m.tab = tabSettings
	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got.(*model).tab != tabSettings {
		t.Error("an unbound key must not switch tabs")
	}
	// the alternatives moved into the package view, so f5 has no tab
	got, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f5")})
	if got.(*model).tab != tabSettings {
		t.Error("f5 must not switch tabs any more")
	}
}

func TestSettingsKeyStepsRows(t *testing.T) {
	m := newModel()
	m.tab = tabSettings
	m.focus = focusList
	m.setCur = 0 // the installer row

	m.installer = "pacman"
	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	mm := got.(*model)
	if mm.installer == "pacman" {
		t.Error("right on the installer row must step the installer")
	}
	// the row cycles in place; capture the value first because handleKey
	// mutates the model in place
	before := mm.installer
	got, _ = mm.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if after := got.(*model).installer; after == before {
		t.Errorf("enter on a settings row must step its value, installer stayed %q", before)
	}
	// enter on settings must not jump to a button row: there is none
	if got.(*model).focus != focusList {
		t.Errorf("focus = %v, want the list", got.(*model).focus)
	}
}

func TestViewSmoke(t *testing.T) {
	sizes := []struct{ w, h int }{{100, 32}, {80, 24}, {60, 20}, {40, 14}}
	for _, s := range sizes {
		for _, tab := range []tabKind{tabSearch, tabUninstall, tabUpdates, tabSettings} {
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

func TestSearchTabShowsRowsAndTheirDetailPaneTogether(t *testing.T) {
	m := newModel()
	m.width, m.height = 120, 40
	m.results = []pkg{
		{name: "neofetch", repo: "aur", aur: true, desc: "a cli system information tool"},
		{name: "fastfetch", repo: "extra", desc: "a neofetch like tool"},
	}
	m.searchCur = 0
	m.info = &pkgInfoState{
		target: pkg{name: "neofetch", repo: "aur", aur: true},
		detail: pkgDetail{name: "neofetch", repo: "aur", version: "7.1.0-2",
			desc: "a cli system information tool", url: "https://example.test/",
			depends: []string{"bash"}},
	}

	const h = 20
	out := m.listView(90, h)
	if n := len(strings.Split(out, "\n")); n != h {
		t.Fatalf("the body is %d lines, want exactly %d", n, h)
	}
	bare := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(out, "")
	if !strings.Contains(bare, "> neofetch") {
		t.Errorf("the selected row must stay on screen:\n%s", bare)
	}
	if !strings.Contains(bare, "fastfetch") {
		t.Errorf("the rows must not be replaced by the pane:\n%s", bare)
	}
	if !strings.Contains(bare, "https://example.test/") {
		t.Errorf("the detail pane must be on screen as well:\n%s", bare)
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

func TestNewsPanelFillsWithAsManyStoriesAsFit(t *testing.T) {
	now := time.Now()
	var items []newsItem
	for i := 0; i < 8; i++ {
		items = append(items, newsItem{
			title: fmt.Sprintf("story 0%d", i+1),
			pub:   now.AddDate(0, 0, -i),
		})
	}
	m := newModel()
	m.news = items

	const h = 24
	out := m.newsView(40, h)
	if len(out) != h {
		t.Fatalf("panel returned %d lines, want exactly %d", len(out), h)
	}
	joined := strings.Join(out, "\n")
	// the panel is not limited to the newest two headlines: it keeps
	// reading down the feed until the column is full
	for _, want := range []string{"arch news", "story 01", "story 06", "story 07"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q must be on screen:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "story 08") {
		t.Errorf("the eighth story does not fit in %d lines:\n%s", h, joined)
	}
	if strings.Contains(joined, "STORY") {
		t.Error("headlines must be lowercased")
	}

	// a short panel still stops at its own height
	if got := len(m.newsView(40, 5)); got != 5 {
		t.Errorf("a five line panel returned %d lines", got)
	}
}

func TestParseNewsReadsTheRssFeed(t *testing.T) {
	rss := `<?xml version="1.0"?>
<rss version="2.0"><channel><title>arch news</title>
<item><title>Older story</title><link>https://a/1</link>
<pubDate>Mon, 05 Jan 2026 10:00:00 +0000</pubDate></item>
<item><title>Newer story</title><link>https://a/2</link>
<pubDate>Wed, 07 Jan 2026 10:00:00 +0000</pubDate></item>
<item><title>No date here</title><link>https://a/3</link></item>
</channel></rss>`
	items, err := parseNews(rss)
	if err != nil {
		t.Fatalf("parseNews: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	// the dated entries come out newest first, undated ones keep their place
	if items[0].title != "Newer story" || items[1].title != "Older story" {
		t.Errorf("order = %v, %v", items[0].title, items[1].title)
	}
	if items[2].title != "No date here" || !items[2].pub.IsZero() {
		t.Errorf("undated item = %#v", items[2])
	}

	if _, err := parseNews("not xml at all"); err == nil {
		t.Error("garbage must be reported as an error")
	}
}

func TestNewsViewShowsTheStoriesAndItsFailures(t *testing.T) {
	m := newModel()
	m.width, m.height = 120, 30
	now := time.Now()
	m.news = []newsItem{
		{title: "A Big News Headline", pub: now},
		{title: "Yesterday Story", pub: now.AddDate(0, 0, -1)},
	}
	out := strings.Join(m.newsView(40, 12), "\n")
	if !strings.Contains(out, "arch news") {
		t.Error("the panel must name itself")
	}
	if !strings.Contains(out, "a big news headline") ||
		!strings.Contains(out, "yesterday story") {
		t.Errorf("both stories must show, got:\n%s", out)
	}
	if strings.Contains(out, "A Big") {
		t.Error("headlines must be lowercased")
	}

	// a fetch failure is reported rather than left blank
	m.news, m.newsErr = nil, errors.New("no network")
	out = strings.Join(m.newsView(40, 8), "\n")
	if !strings.Contains(out, "cannot fetch news") {
		t.Errorf("error state = \n%s", out)
	}
}

func TestParsePkgInfoReadsDepends(t *testing.T) {
	out := "" +
		"Repository      : extra\n" +
		"Name            : mpv\n" +
		"Version         : 1:0.38.0-1\n" +
		"Description     : a free, open source media player\n" +
		"URL             : https://mpv.io/\n" +
		"Licenses        : GPL-2.0-only\n" +
		"Depends On      : libplacebo  libass  ffmpeg  libpulse\n" +
		"Download Size   : 4.50 MiB\n" +
		"Installed Size  : 12.30 MiB\n"
	d := parsePkgInfo(out)
	if d.name != "mpv" || d.repo != "extra" || d.version != "1:0.38.0-1" {
		t.Errorf("identity = %#v", d)
	}
	if len(d.depends) != 4 || d.depends[0] != "libplacebo" {
		t.Errorf("depends = %v", d.depends)
	}
	if len(d.licenses) != 1 || d.licenses[0] != "GPL-2.0-only" {
		t.Errorf("licenses = %v", d.licenses)
	}

	// "None" means no dependencies, not one dependency called none
	d = parsePkgInfo("Name            : base\nDepends On      : None\n")
	if len(d.depends) != 0 {
		t.Errorf("None must mean no depends, got %v", d.depends)
	}
	// long fields continue on indented lines and are glued back together
	d = parsePkgInfo("Depends On      : glibc\n                  gcc-libs\n")
	if len(d.depends) != 2 || d.depends[1] != "gcc-libs" {
		t.Errorf("wrapped depends = %v", d.depends)
	}
}

func TestInfoViewOmitsDependenciesWhenNone(t *testing.T) {
	m := newModel()
	m.width, m.height = 100, 30
	m.info = &pkgInfoState{
		target: pkg{name: "base", repo: "core"},
		detail: pkgDetail{name: "base", repo: "core", version: "3-2",
			desc: "minimal package set"},
	}
	out := m.infoView(80, 12)
	if !strings.Contains(out, "base") {
		t.Errorf("the name must show:\n%s", out)
	}
	if strings.Contains(out, "dependencies") {
		t.Errorf("a package with no dependencies must not show the row:\n%s", out)
	}

	m.info.detail.depends = []string{"glibc", "gcc-libs"}
	out = m.infoView(80, 12)
	if !strings.Contains(out, "glibc") || !strings.Contains(out, "gcc-libs") {
		t.Errorf("dependencies must show when they exist:\n%s", out)
	}
	// the label keeps a gap before its value and the values line up
	if strings.Contains(out, "dependenciesa") {
		t.Errorf("labels need a padded column:\n%s", out)
	}
	// strip the colours: the label is padded to a shared column width
	bare := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(out, "")
	if !strings.Contains(bare, "dependencies [ ] glibc") {
		t.Errorf("label column = %q", bare)
	}
	// the panel is outlined, and the name leads it just inside the frame
	if !strings.Contains(bare, "╭") || !strings.Contains(bare, "╰") {
		t.Errorf("the detail panel must be outlined:\n%s", bare)
	}
	if i, j := strings.Index(bare, "base  [core]"), strings.Index(bare, "description"); i < 0 || j < i {
		t.Errorf("the name must lead the panel:\n%s", bare)
	}

	// scrolling far past the end clamps instead of blanking the pane
	m.info.scroll = 999
	if s := m.infoView(80, 12); strings.TrimSpace(s) == "" {
		t.Error("scrolling past the end must not blank the detail pane")
	}
}

// a dependency already on the disk is marked, one that is not is not.
func TestInfoViewMarksInstalledDependencies(t *testing.T) {
	m := newModel()
	m.width, m.height = 100, 30
	m.info = &pkgInfoState{
		target: pkg{name: "neofetch", repo: "aur", aur: true},
		detail: pkgDetail{name: "neofetch", repo: "aur", version: "7.1.0-2",
			depends:      []string{"bash", "coreutils", "catimg"},
			depInstalled: map[string]bool{"bash": true, "coreutils": true}},
	}
	bare := regexp.MustCompile(`\x1b\[[0-9;]*m`).
		ReplaceAllString(strings.Join(m.infoRows(80), "\n"), "")
	for _, want := range []string{"[✓] bash", "[✓] coreutils", "[ ] catimg"} {
		if !strings.Contains(bare, want) {
			t.Errorf("the panel misses %q:\n%s", want, bare)
		}
	}
	// a dependency pinned to a version is shown as the package it is
	if got := depName("glibc>=2.34"); got != "glibc" {
		t.Errorf("depName(glibc>=2.34) = %q", got)
	}
	if got := depName("libreadline.so=8-64"); got != "libreadline.so" {
		t.Errorf("depName(libreadline.so=8-64) = %q", got)
	}
}

// the panel is read from an offset, so the first step moves it and the last
// one stops at the end instead of running past it.
func TestInfoPanelScrollsFromItsTop(t *testing.T) {
	m := newModel()
	m.tab = tabSearch
	m.width, m.height = 80, 30
	var deps []string
	for i := 0; i < 60; i++ {
		deps = append(deps, fmt.Sprintf("dep-%02d", i))
	}
	m.results = []pkg{{name: "big", repo: "extra", version: "1-1"}}
	m.info = &pkgInfoState{target: m.results[0],
		detail: pkgDetail{name: "big", repo: "extra", version: "1-1", depends: deps}}

	// the panel as it is on screen: its rows, scrolled to its offset
	panel := func() string {
		rows := m.infoRows(80)
		return strings.Join(scrollLines(rows, m.info.scroll,
			m.infoPaneLines(m.layout().listH)), "\n")
	}

	top := panel()
	m.moveInfo(10)
	moved := panel()
	if top == moved {
		t.Fatal("the first step must move the panel")
	}
	if strings.Contains(moved, "big  [extra]") {
		t.Error("the panel must scroll past its own first line")
	}
	for i := 0; i < 50; i++ {
		m.moveInfo(10)
	}
	end := panel()
	if !strings.Contains(end, "dep-59") {
		t.Errorf("scrolling to the end must show the last line:\n%s", end)
	}
	m.moveInfo(10)
	if panel() != end {
		t.Error("scrolling past the end must stop there")
	}
	m.moveInfo(-1000)
	if panel() != top {
		t.Error("scrolling back up must return to the first line")
	}
	// a pane that fits is never scrolled away from its own top
	m.info.detail = pkgDetail{name: "big", repo: "extra", version: "1-1"}
	m.moveInfo(10)
	if m.info.scroll != 0 {
		t.Errorf("a panel that fits must not scroll, offset = %d", m.info.scroll)
	}
}

func TestDetectOSIsLowercase(t *testing.T) {
	got := detectOS()
	if got == "" {
		t.Fatal("detectOS must name the running system")
	}
	if got != lower(got) {
		t.Errorf("os name must be lowercase: %q", got)
	}
}

func TestHeaderShowsGithubAndOS(t *testing.T) {
	m := newModel()
	m.width, m.height = 120, 30
	out := m.headerView(120)
	if !strings.Contains(out, "get my aur installer") {
		t.Error("header must keep the title")
	}
	if !strings.Contains(out, "github.com/keaneskiii") {
		t.Error("header must link the github")
	}
	m.osName = "Arch Linux"
	out = m.headerView(120)
	if !strings.Contains(out, "arch linux") {
		t.Errorf("the os name must sit in the header, got:\n%s", out)
	}
	if strings.Contains(out, "Arch Linux") {
		t.Error("the os name must be lowercase")
	}
}

func TestEnterOnSearchResultOpensInfo(t *testing.T) {
	m := newModel()
	m.tab = tabSearch
	m.focus = focusList
	m.results = []pkg{{name: "mpv", repo: "extra", version: "1-1"}}

	got, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	mm := got.(*model)
	if mm.info == nil {
		t.Fatal("enter on a search row must open the detail view")
	}
	if mm.info.target.name != "mpv" {
		t.Errorf("detail view is for %q", mm.info.target.name)
	}
	if cmd == nil {
		t.Error("opening the detail view must fetch the info")
	}
	if len(mm.buttonsFor()) != 2 {
		t.Errorf("detail view buttons = %v, want download and back", mm.buttonsFor())
	}

	// esc closes it again
	got, _ = mm.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got.(*model).info != nil {
		t.Error("esc must close the detail view")
	}
}

func TestDetailButtonsDownloadAndBack(t *testing.T) {
	m := newModel()
	m.tab = tabSearch
	m.installer = "yay"
	m.info = &pkgInfoState{target: pkg{name: "mpv", aur: true}}

	// back closes the view
	got, cmd := m.activate(1)
	if got.(*model).info != nil {
		t.Error("back must close the detail view")
	}
	if cmd != nil {
		t.Error("closing the view runs no command")
	}

	// the first button queues the download of the package on show
	m.info = &pkgInfoState{target: pkg{name: "mpv", aur: true}}
	_, cmd = m.activate(0)
	if cmd == nil {
		t.Fatal("download must queue a command")
	}
	msg, ok := cmd().(queueMsg)
	if !ok {
		t.Fatalf("download produced %#v", cmd())
	}
	if msg.title != "download mpv with yay" || msg.argv[1] != "-S" {
		t.Errorf("queued %q %v", msg.title, msg.argv)
	}
}

func TestSwitchTabClosesTheDetailView(t *testing.T) {
	m := newModel()
	m.info = &pkgInfoState{target: pkg{name: "mpv"}}
	got, _ := m.switchTab(tabUpdates)
	if got.(*model).info != nil {
		t.Error("switching tabs must close the detail view")
	}
}

func TestSearchQueriesTryThePhraseAndTheDashedForm(t *testing.T) {
	if got := searchQueries("neofetch"); len(got) != 1 || got[0] != "neofetch" {
		t.Errorf("a single word asks once, got %v", got)
	}
	got := searchQueries("  visual studio code ")
	want := []string{"visual studio code", "visual-studio-code"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("queries = %v, want %v", got, want)
	}
	if got := searchQueries("   "); got != nil {
		t.Errorf("a blank query must ask nothing, got %v", got)
	}
}

func TestMatchRankPutsWhatYouAskedForOnTop(t *testing.T) {
	rows := []pkg{
		{name: "hyfetch", repo: "extra", desc: "neofetch with pride flags"},
		{name: "neofetch-git", repo: "aur", aur: true, desc: "the git build of it"},
		{name: "fastfetch", repo: "extra", desc: "a feature rich neofetch like tool"},
		{name: "neofetch", repo: "aur", aur: true, desc: "a cli system information tool"},
		{name: "breefetch", repo: "extra", desc: "nothing to do with any of it"},
	}
	rankResults(rows, "neofetch")
	var got []string
	for _, p := range rows {
		got = append(got, p.name)
	}
	want := "neofetch,neofetch-git,fastfetch,hyfetch,breefetch"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}

	// a package whose name is the query itself beats a description that
	// spells the query out, and an official row wins a tie
	tie := []pkg{
		{name: "zeta", repo: "aur", aur: true, desc: "a neofetch like tool"},
		{name: "alpha", repo: "extra", desc: "another neofetch like tool"},
	}
	rankResults(tie, "neofetch")
	if tie[0].name != "alpha" {
		t.Errorf("official rows come first in a tie, got %v first", tie[0].name)
	}
}

func TestMatchRankUnderstandsAPhraseQuery(t *testing.T) {
	const q = "visual studio code"
	cases := []struct {
		p    pkg
		want int
	}{
		{pkg{name: "visual-studio-code"}, 0},
		{pkg{name: "visual-studio-code-bin"}, 1},
		{pkg{name: "code", repo: "extra",
			desc: "the open source build of visual studio code"}, 4},
		{pkg{name: "roslyn-ls", repo: "aur", aur: true,
			desc: "language server behind c# dev kit for visual studio code"}, 5},
		{pkg{name: "unrelated", repo: "extra", desc: "nothing at all"}, 6},
	}
	for _, c := range cases {
		if got := matchRank(c.p, q); got != c.want {
			t.Errorf("matchRank(%s) = %d, want %d", c.p.name, got, c.want)
		}
	}
	// so the exact package is the first row and the rest line up under it
	rows := []pkg{
		{name: "code", repo: "extra", desc: "the open source build of visual studio code"},
		{name: "visual-studio-code-bin", repo: "aur", aur: true, desc: "official binary version"},
		{name: "roslyn-ls", repo: "aur", aur: true, desc: "a language server"},
	}
	rankResults(rows, q)
	if rows[0].name != "visual-studio-code-bin" {
		t.Errorf("the package named after the query must lead, got %q first", rows[0].name)
	}
}

func TestMovingTheSelectionOpensItsDetailPane(t *testing.T) {
	m := newModel()
	m.tab = tabSearch
	m.results = []pkg{
		{name: "neofetch", repo: "aur", aur: true},
		{name: "fastfetch", repo: "extra"},
	}

	got, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	mm := got.(*model)
	if mm.searchCur != 1 {
		t.Fatalf("down moved the selection to %d", mm.searchCur)
	}
	if mm.info == nil || mm.info.target.name != "fastfetch" {
		t.Fatal("moving the selection must open the detail pane for it")
	}
	if cmd == nil {
		t.Error("the detail pane must fetch the package's info")
	}
	// the pane belongs to the row, so it never steals the focus
	if mm.focus == focusBtn {
		t.Error("browsing the results must not jump to the buttons")
	}

	// staying on the same row does not fetch it a second time
	got, cmd = mm.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Error("the selected package must not be fetched twice")
	}
	// and it scrolls with pgdown instead of moving off the package
	got, _ = mm.handleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	if got.(*model).searchCur != 1 {
		t.Error("pgdown scrolls the detail pane, not the list")
	}
}

func TestResultsOpenTheirDetailPaneAsTheyLand(t *testing.T) {
	m := newModel()
	m.tab = tabSearch
	m.searching = true
	m.Update(searchMsg{results: []pkg{{name: "neofetch"}}, query: "neofetch"})
	if m.searching {
		t.Error("searchMsg must clear the searching flag")
	}
	if m.info == nil || m.info.target.name != "neofetch" {
		t.Fatal("the first row is the selection, so its detail pane opens with it")
	}

	// a search with nothing in it leaves no pane behind
	m.Update(searchMsg{query: "zzzz"})
	if m.info != nil {
		t.Error("no results means no detail pane")
	}
}

func TestPackageViewCarriesTheStandInsFromTheSearchResults(t *testing.T) {
	m := newModel()
	m.tab = tabSearch
	m.results = []pkg{
		{name: "neofetch", repo: "aur", aur: true, version: "7.1.0-2",
			desc: "a cli system information tool"},
		{name: "fastfetch", repo: "extra", version: "2.70.0-1",
			desc: "a feature-rich and performance oriented neofetch like system information tool"},
	}
	// the pane is built exactly the way opening a package builds it
	cmd := m.showInfoFor(m.results[0])
	if cmd == nil {
		t.Fatal("an uncached package must ask for its facts")
	}
	// and the answer lands the way the program lands it
	m.Update(pkgInfoMsg{target: m.results[0], detail: pkgDetail{
		name: "neofetch", repo: "aur", version: "7.1.0-2",
		desc:    "a cli system information tool",
		url:     "https://github.com/dylanaraps/neofetch",
		depends: []string{"bash", "coreutils"}, licenses: []string{"MIT"},
	}})
	rows := strings.Join(m.infoRows(100), "\n")
	for _, want := range []string{
		"neofetch", "7.1.0-2", "https://github.com/dylanaraps/neofetch",
		"bash", "coreutils", "alternatives", "fastfetch",
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("the package view misses %q:\n%s", want, rows)
		}
	}
	// the stand-ins come from the results already on screen, so the pane
	// is drawn before anything is fetched
	if m.info == nil || len(m.info.alts) == 0 {
		t.Fatal("opening a package must list the stand-ins it already knows")
	}

	// a package already read this session opens with no request at all
	if cmd := m.showInfoFor(m.results[0]); cmd != nil {
		t.Error("a cached package must open without asking again")
	}
	if m.info.loading {
		t.Error("a cached package must not render as loading")
	}
}

func TestPackageViewCountsTheStandInsThatDoNotFit(t *testing.T) {
	m := newModel()
	var alts []pkg
	for i := 0; i < altShown+5; i++ {
		alts = append(alts, pkg{name: fmt.Sprintf("alt-%d", i), desc: "another tool"})
	}
	m.info = &pkgInfoState{target: pkg{name: "base"},
		detail: pkgDetail{name: "base"}, alts: alts}
	rows := strings.Join(m.infoRows(80), "\n")
	if !strings.Contains(rows, "+5 more") {
		t.Errorf("the stand-ins that do not fit must be counted:\n%s", rows)
	}
	shown := 0
	for _, p := range alts {
		if strings.Contains(rows, p.name) {
			shown++
		}
	}
	if shown != altShown {
		t.Errorf("the pane listed %d stand-ins, want %d", shown, altShown)
	}
}

func TestParseAurSearchAndInfo(t *testing.T) {
	raw := json.RawMessage(`{"resultcount":2,"results":[` +
		`{"Name":"Neofetch","Version":"7.1.0-2","Description":"A CLI system information tool"},` +
		`{"Name":"","Version":"0","Description":""}]}`)
	rows, err := parseAurSearch(raw)
	if err != nil {
		t.Fatalf("parseAurSearch: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want the one named package", rows)
	}
	if r := rows[0]; r.name != "neofetch" || !r.aur || r.repo != "aur" ||
		r.version != "7.1.0-2" || r.desc != "A CLI system information tool" {
		t.Errorf("row = %+v", r)
	}

	raw = json.RawMessage(`{"results":[{"Name":"neofetch","Version":"7.1.0-2",` +
		`"Description":" a cli system information tool written in bash ",` +
		`"URL":"https://github.com/dylanaraps/neofetch",` +
		`"Depends":["bash","coreutils"],"License":["MIT"]}]}`)
	d, err := parseAurInfo(raw, "neofetch")
	if err != nil {
		t.Fatalf("parseAurInfo: %v", err)
	}
	if d.name != "neofetch" || d.repo != "aur" || d.version != "7.1.0-2" ||
		d.url != "https://github.com/dylanaraps/neofetch" ||
		strings.Join(d.depends, " ") != "bash coreutils" ||
		strings.Join(d.licenses, ",") != "MIT" ||
		d.desc != "a cli system information tool written in bash" {
		t.Errorf("detail = %+v", d)
	}

	// the aur reports failures in the body of a 200 answer, so they have
	// to be read as failures too
	if _, err := parseAurSearch(
		json.RawMessage(`{"error":"the term must not be less than two characters"}`)); err == nil {
		t.Error("an api error must not read as an empty answer")
	}
	if _, err := parseAurInfo(json.RawMessage(`{"results":[]}`), "nothing"); err == nil {
		t.Error("an empty info answer must fail by name")
	}
}

func TestPickAlternativesKeepsTheOnesThatAdvertiseIt(t *testing.T) {
	base := pkg{name: "neofetch", repo: "aur", aur: true,
		desc: "a cli system information tool written in bash"}
	pool := []pkg{
		{name: "neofetch", repo: "aur", aur: true, desc: "a cli system information tool"},
		{name: "fastfetch", repo: "extra", desc: "a feature rich neofetch like system info tool"},
		{name: "neofetch-git", repo: "aur", aur: true, desc: "the git build"},
		{name: "breefetch", repo: "extra", desc: "nothing to do with any of it"},
	}
	got := pickAlternatives(pool, base)
	var names []string
	for _, p := range got {
		names = append(names, p.name)
	}
	// the package itself is never an alternative to itself, and neither is
	// a package with nothing in common
	want := "fastfetch,neofetch-git"
	if strings.Join(names, ",") != want {
		t.Errorf("alternatives = %v, want %s", names, want)
	}

	// a name too short to mean anything in prose never earns the top rank
	if r := altRank(pkg{name: "everything", desc: "code for code"},
		pkg{name: "code", desc: "an editor"}); r == 0 {
		t.Errorf("a four letter name must not claim the first rank, got %d", r)
	}
}

// the arch news keeps a place at every width: a column when there is room
// for one, a single line with the newest story when a package's own view
// has claimed the room.
func TestNewsColumnFoldsIntoAStripWhenAPackageIsOpen(t *testing.T) {
	m := newModel()
	m.results = []pkg{{name: "mpv", repo: "extra", version: "1-1", desc: "a media player"}}
	m.news = []newsItem{
		{title: "a new issue with the linux firmware package", pub: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)},
		{title: "older firmware note", pub: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	m.width, m.height = 120, 36

	// wide enough and nothing is open: the stories keep their column
	out := m.View()
	if !strings.Contains(out, "older firmware note") {
		t.Error("a wide window with no package open must show the news column")
	}

	// the moment a package is read the column gives its room back
	m.info = &pkgInfoState{target: m.results[0], detail: pkgDetail{name: "mpv"}}
	out = m.View()
	if !strings.Contains(out, "a new issue with the linux firmware package") {
		t.Errorf("the newest story must stay on its own line:\n%s", out)
	}
	if strings.Contains(out, "older firmware note") {
		t.Error("only the newest story fits on the strip")
	}

	// a narrow window has no room for a column at all, so it uses the strip
	m.info = nil
	m.width = 80
	out = m.View()
	if !strings.Contains(out, "a new issue with the linux firmware package") {
		t.Error("a narrow window must show the newest story on a line")
	}
}

// every tab, at every size the window is allowed to be, draws exactly the
// whole window and never a single cell wider than it.
func TestEveryLayoutFillsTheWindowWithoutOverflowing(t *testing.T) {
	m := newModel()
	m.busy = true // the log pane is open, the tightest case
	m.results = []pkg{
		{name: "neofetch", repo: "aur", aur: true, version: "7.1.0-2",
			desc: "a cli system information tool written in bash that supports displaying images"},
		{name: "fastfetch", repo: "extra", version: "2.70.0-1",
			desc: "a feature-rich and performance oriented neofetch like system information tool"},
	}
	m.info = &pkgInfoState{
		target: m.results[0],
		detail: pkgDetail{name: "neofetch", repo: "aur", version: "7.1.0-2",
			desc:     "a cli system information tool written in bash that supports displaying images",
			url:      "https://github.com/dylanaraps/neofetch",
			licenses: []string{"MIT"}, depends: []string{"bash", "coreutils"},
			installedSize: "1.2 mb"},
		// the stand-ins are part of the pane, so they are part of the
		// frame that has to fit the window exactly
		alts: pickAlternatives(m.results, m.results[0]),
	}
	m.installed = []pkg{{name: "mpv", repo: "extra", installed: true}}
	m.updates = []updateItem{{name: "mpv", oldver: "1", newver: "2"}}
	m.news = []newsItem{{title: "a headline long enough to wrap in the column", pub: time.Now()}}
	m.newsErr = errors.New("a long enough error message to wrap in the column")

	for _, size := range [][2]int{{40, 14}, {60, 20}, {80, 24},
		{96, 30}, {120, 36}, {160, 48}} {
		w, h := size[0], size[1]
		for _, tab := range []tabKind{tabSearch, tabUninstall, tabUpdates,
			tabSettings} {
			m.width, m.height = w, h
			m.tab = tab
			lines := strings.Split(m.View(), "\n")
			if len(lines) != h {
				t.Errorf("%d tab at %dx%d drew %d lines, want %d",
					tab, w, h, len(lines), h)
				continue
			}
			for i, ln := range lines {
				if width := lipgloss.Width(ln); width > w {
					t.Errorf("%d tab at %dx%d: line %d is %d cells wide: %q",
						tab, w, h, i+1, width, ln)
				}
			}
		}
	}
}
