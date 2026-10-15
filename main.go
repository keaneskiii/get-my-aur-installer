package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// --------------------------------------------------------------------------- styles

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#c6a0f6"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6c7086"))

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1e1e2e")).
			Background(lipgloss.Color("#c6a0f6")).
			Padding(0, 2)

	tabIdleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#89b4fa")).
			Padding(0, 2)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1e1e2e")).
			Background(lipgloss.Color("#89b4fa"))

	inputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#a6e3a1"))

	inputFocusedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#cdd6f4"))

	inputBlurredStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6c7086"))

	btnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#cdd6f4")).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#585b70")).
			Padding(0, 2)

	btnFocusedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1e1e2e")).
			Background(lipgloss.Color("#a6e3a1")).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#a6e3a1")).
			Padding(0, 2)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f9e2af"))

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f38ba8"))

	okStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a6e3a1"))

	logStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a6adc8"))

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6c7086"))

	sepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#313244"))
)

// trunc shortens s so it renders at most w cells wide, marking the cut
// with an ellipsis.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return runewidth.Truncate(s, w-1, "") + "…"
}

// pad right-pads s with spaces up to w cells wide.
func pad(s string, w int) string {
	dw := runewidth.StringWidth(s)
	if dw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-dw)
}

// windowStart picks the first index of a scrolling window of height h
// that keeps the selected row sel visible.
func windowStart(total, sel, h int) int {
	if h <= 0 || total <= h {
		return 0
	}
	if sel < h {
		return 0
	}
	start := sel - h + 1
	if start > total-h {
		start = total - h
	}
	return start
}

func lower(s string) string { return strings.ToLower(s) }

// ------------------------------------------------------- system calls & parsing

// pkg is one package row in a list.
type pkg struct {
	name      string
	repo      string
	version   string
	desc      string
	installed bool
	aur       bool
}

// updateItem is one pending update.
type updateItem struct {
	name   string
	oldver string
	newver string
	aur    bool
}

type searchMsg struct {
	results []pkg
	err     error
	query   string
}

type updatesMsg struct {
	items   []updateItem
	err     error
	checked time.Time
}

type installedMsg struct {
	pkgs    []pkg
	err     error
	checked time.Time
}

type sudoProbeMsg struct{ ok bool }

type sudoWarmedMsg struct{ err error }

type logLineMsg struct{ text string }

type actionDoneMsg struct {
	title string
	err   error
}

func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// parseSearch parses `pacman -Ss` / `yay -Ssa` / `paru -Ssa` output.
func parseSearch(out string, aur bool) []pkg {
	var pkgs []pkg
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' {
			// indented description line
			if len(pkgs) > 0 {
				d := strings.TrimSpace(ln)
				if d != "" {
					pkgs[len(pkgs)-1].desc = strings.TrimSpace(pkgs[len(pkgs)-1].desc + " " + d)
				}
			}
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		head := f[0]
		repo, name, ok := strings.Cut(head, "/")
		if !ok {
			repo, name = "", head
		}
		if repo == "" {
			repo = "aur"
		}
		p := pkg{name: name, repo: repo, version: f[1], aur: aur}
		if strings.Contains(ln, "[installed]") {
			p.installed = true
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// searchCmd queries the arch repos (pacman) plus the aur via yay and paru.
func searchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		type res struct {
			src string
			out string
			err error
			aur bool
		}
		jobs := []struct {
			bin  string
			args []string
			aur  bool
		}{
			{"pacman", []string{"-Ss", query}, false},
			{"yay", []string{"-Ssa", query}, true},
			{"paru", []string{"-Ssa", query}, true},
		}

		out := make([]res, len(jobs))
		var wg sync.WaitGroup
		for i, j := range jobs {
			if !hasBin(j.bin) {
				out[i] = res{src: j.bin, aur: j.aur}
				continue
			}
			wg.Add(1)
			go func(i int, bin string, args []string, aur bool) {
				defer wg.Done()
				b, err := exec.CommandContext(ctx, bin, args...).Output()
				out[i] = res{src: bin, out: string(b), err: err, aur: aur}
			}(i, j.bin, j.args, j.aur)
		}
		wg.Wait()

		var results []pkg
		var errs []string
		seen := map[string]bool{}
		for _, r := range out {
			if r.err != nil && r.out == "" {
				// ignore exit errors with no output (e.g. "no results")
				var ee *exec.ExitError
				if !errors.As(r.err, &ee) {
					errs = append(errs, r.src+": "+r.err.Error())
				}
				continue
			}
			for _, p := range parseSearch(r.out, r.aur) {
				// the same package can live in several repos (mirrors,
				// aur); keep the first hit only, pacman runs first so
				// official packages win over aur duplicates.
				if seen[p.name] {
					continue
				}
				seen[p.name] = true
				results = append(results, p)
			}
		}
		var err error
		if len(results) == 0 && len(errs) > 0 {
			err = errors.New(strings.Join(errs, "; "))
		}
		return searchMsg{results: results, err: err, query: query}
	}
}

// installedCmd lists every installed package and marks aur (foreign) ones.
func installedCmd() tea.Cmd {
	return func() tea.Msg {
		b, err := exec.Command("pacman", "-Q").Output()
		if err != nil {
			return installedMsg{err: err}
		}
		foreign := map[string]bool{}
		if fm, ferr := exec.Command("pacman", "-Qm").Output(); ferr == nil {
			for _, ln := range strings.Split(string(fm), "\n") {
				f := strings.Fields(ln)
				if len(f) > 0 {
					foreign[f[0]] = true
				}
			}
		}
		var pkgs []pkg
		for _, ln := range strings.Split(string(b), "\n") {
			f := strings.Fields(ln)
			if len(f) < 2 {
				continue
			}
			pkgs = append(pkgs, pkg{
				name:      f[0],
				version:   f[1],
				aur:       foreign[f[0]],
				installed: true,
			})
		}
		return installedMsg{pkgs: pkgs, checked: time.Now()}
	}
}

// parseUpdates parses `checkupdates` and `yay -Qua` / `paru -Qua` output.
func parseUpdates(out string, aur bool) []updateItem {
	var items []updateItem
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// strip trailing age annotation like " [14h24m]"
		if i := strings.LastIndex(ln, " ["); i != -1 && strings.HasSuffix(ln, "]") {
			ln = ln[:i]
		}
		left, right, ok := strings.Cut(ln, " -> ")
		if !ok {
			continue
		}
		lf := strings.Fields(left)
		rf := strings.Fields(right)
		if len(lf) < 2 || len(rf) < 1 {
			continue
		}
		items = append(items, updateItem{
			name:   lf[0],
			oldver: strings.Join(lf[1:], " "),
			newver: rf[0],
			aur:    aur,
		})
	}
	return items
}

// updatesCmd collects available updates from the arch repos plus yay and paru.
func updatesCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		var items []updateItem
		var errs []string
		seen := map[string]bool{}

		collect := func(bin string, args []string, aur bool) {
			if !hasBin(bin) {
				return
			}
			b, err := exec.CommandContext(ctx, bin, args...).Output()
			if err != nil && len(b) == 0 {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					errs = append(errs, bin+": "+err.Error())
				}
				return
			}
			for _, it := range parseUpdates(string(b), aur) {
				if seen[it.name] {
					continue
				}
				seen[it.name] = true
				items = append(items, it)
			}
		}

		collect("checkupdates", nil, false)
		collect("yay", []string{"-Qua"}, true)
		collect("paru", []string{"-Qua"}, true)

		var err error
		if len(items) == 0 && len(errs) > 0 {
			err = errors.New(strings.Join(errs, "; "))
		}
		// a stamp marks this as a real check, not just an empty initial list
		return updatesMsg{items: items, err: err, checked: time.Now()}
	}
}

// lineWriter streams command output into the tui as log messages.
type lineWriter struct {
	mu  sync.Mutex
	buf string
	p   *tea.Program
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(b)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		if strings.TrimSpace(line) != "" {
			w.p.Send(logLineMsg{text: line})
		}
	}
	return len(b), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if strings.TrimSpace(w.buf) != "" {
		w.p.Send(logLineMsg{text: w.buf})
		w.buf = ""
	}
}

// startActionCmd runs argv, streaming its output line by line into the tui.
// The child gets no stdin and a neutral editor/pager/browser, so a build can
// never pop open another program or wait for input we cannot give it.
func startActionCmd(p *tea.Program, title string, argv []string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = nil
		cmd.Env = append(os.Environ(),
			"EDITOR=/bin/true",
			"VISUAL=/bin/true",
			"GIT_EDITOR=/bin/true",
			"PAGER=/bin/true",
			"BROWSER=/bin/true",
		)
		w := &lineWriter{p: p}
		cmd.Stdout = w
		cmd.Stderr = w
		err := cmd.Run()
		w.flush()
		return actionDoneMsg{title: title, err: err}
	}
}

// aurFlags are appended to every yay/paru call so the helper stays silent:
// no confirmation, no PKGBUILD review, no diff or edit menu. sudo is still
// used internally by the helper to install, which is expected.
func aurFlags(helper string) []string {
	if helper == "paru" {
		return []string{"--noconfirm", "--skipreview"}
	}
	return []string{"--noconfirm", "--answerdiff", "n", "--answeredit", "n"}
}

// sudoProbeCmd checks whether a cached sudo timestamp is available.
func sudoProbeCmd() tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("sudo", "-n", "true").Run()
		return sudoProbeMsg{ok: err == nil}
	}
}

// ------------------------------------------------------------ model & key input

type focusArea int

const (
	focusInput focusArea = iota
	focusList
	focusBtn
)

type tabKind int

const (
	tabSearch tabKind = iota
	tabUninstall
	tabUpdates
)

type pendingAction struct {
	title string
	argv  []string
}

type model struct {
	width  int
	height int

	tab    tabKind
	focus  focusArea
	btnIdx int

	searchInput textinput.Model
	filterInput textinput.Model

	results     []pkg
	searchCur   int
	searching   bool
	installed   []pkg
	instCur     int
	instLoading bool
	updates     []updateItem
	updCur      int
	updLoading  bool

	// installer is the tool used for install/update actions: "pacman",
	// "yay" or "paru". It is cycled with the h key.
	installer string
	hasYay    bool
	hasParu   bool

	// updStamp/instStamp record the last successful refresh of the updates
	// and installed lists, so switching tabs re-fetches when they are stale.
	updStamp  time.Time
	instStamp time.Time

	busy        bool
	actionTitle string
	pending     *pendingAction
	logLines    []string

	spin      spinner.Model
	status    string
	statusErr bool

	program *tea.Program
}

func newModel() *model {
	si := textinput.New()
	si.Prompt = "> "
	si.PromptStyle = inputPromptStyle
	si.TextStyle = inputFocusedStyle
	si.PlaceholderStyle = dimStyle
	si.Placeholder = "type a package name and press enter"
	si.CharLimit = 128

	fi := textinput.New()
	fi.Prompt = "> "
	fi.PromptStyle = inputPromptStyle
	fi.TextStyle = inputFocusedStyle
	fi.PlaceholderStyle = dimStyle
	fi.Placeholder = "filter installed packages"
	fi.CharLimit = 128

	hasYay := hasBin("yay")
	hasParu := hasBin("paru")
	// pick the first available installer; aur-capable helpers win because
	// they can install both repo and aur packages
	installer := "pacman"
	switch {
	case hasYay:
		installer = "yay"
	case hasParu:
		installer = "paru"
	}

	sp := spinner.New(spinner.WithSpinner(spinner.Line))

	return &model{
		width:       80,
		height:      24,
		searchInput: si,
		filterInput: fi,
		instLoading: true,
		updLoading:  true,
		installer:   installer,
		hasYay:      hasYay,
		hasParu:     hasParu,
		spin:        sp,
		status:      "ready",
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.searchInput.Focus(), installedCmd(), updatesCmd())
}

func (m *model) syncInputs() {
	if m.tab == tabSearch && m.focus == focusInput {
		m.searchInput.Focus()
	} else {
		m.searchInput.Blur()
	}
	if m.tab == tabUninstall && m.focus == focusInput {
		m.filterInput.Focus()
	} else {
		m.filterInput.Blur()
	}
}

// installers returns the install tools available on this machine, in the
// order they are cycled through with the h key.
func (m *model) installers() []string {
	out := []string{"pacman"}
	if m.hasYay {
		out = append(out, "yay")
	}
	if m.hasParu {
		out = append(out, "paru")
	}
	return out
}

// nextInstaller cycles pacman -> yay -> paru -> pacman, skipping tools that
// are not installed.
func (m *model) nextInstaller() {
	list := m.installers()
	for i, name := range list {
		if name == m.installer {
			m.installer = list[(i+1)%len(list)]
			return
		}
	}
	m.installer = list[0]
}

// canInstall reports whether the current installer can handle the target.
// pacman only sees the official repos, so aur packages need yay or paru.
func (m *model) canInstall(aur bool) bool {
	if aur && m.installer == "pacman" {
		m.setStatus("aur packages need yay or paru - press h to switch", true)
		return false
	}
	return true
}

// filteredInstalled returns installed packages matching the filter.
func (m *model) filteredInstalled() []pkg {
	q := lower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		return m.installed
	}
	var out []pkg
	for _, p := range m.installed {
		if strings.Contains(lower(p.name), q) ||
			strings.Contains(lower(p.version), q) {
			out = append(out, p)
		}
	}
	return out
}

func clamp(v, max int) int {
	if max <= 0 {
		return 0
	}
	if v >= max {
		return max - 1
	}
	if v < 0 {
		return 0
	}
	return v
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.searchInput.Width = m.width - 4
		m.filterInput.Width = m.width - 4
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if m.busy || m.searching {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil

	case searchMsg:
		m.searching = false
		if msg.err != nil && len(msg.results) == 0 {
			m.setStatus("search failed: "+msg.err.Error(), true)
			return m, nil
		}
		m.results = msg.results
		m.searchCur = 0
		if len(msg.results) == 0 {
			m.setStatus("no results for '"+msg.query+"'", false)
		} else {
			m.setStatus(fmt.Sprintf("found %d packages", len(msg.results)), false)
		}
		return m, nil

	case installedMsg:
		m.instLoading = false
		if msg.err != nil {
			m.setStatus("cannot read installed packages: "+msg.err.Error(), true)
			return m, nil
		}
		m.installed = msg.pkgs
		m.instStamp = msg.checked
		m.instCur = clamp(m.instCur, len(m.installed))
		// refresh installed marks on search results too
		byName := map[string]bool{}
		for _, p := range m.installed {
			byName[p.name] = true
		}
		for i := range m.results {
			m.results[i].installed = byName[m.results[i].name]
		}
		return m, nil

	case updatesMsg:
		m.updLoading = false
		if msg.err != nil {
			m.setStatus("cannot check updates: "+msg.err.Error(), true)
			return m, nil
		}
		m.updates = msg.items
		m.updStamp = msg.checked
		m.updCur = clamp(m.updCur, len(m.updates))
		repo, aurCount := 0, 0
		for _, u := range m.updates {
			if u.aur {
				aurCount++
			} else {
				repo++
			}
		}
		m.setStatus(fmt.Sprintf("%d updates available (%d repos, %d aur)",
			len(m.updates), repo, aurCount), false)
		return m, nil

	case logLineMsg:
		m.logLines = append(m.logLines, msg.text)
		if len(m.logLines) > 300 {
			m.logLines = m.logLines[len(m.logLines)-300:]
		}
		return m, nil

	case actionDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.setStatus("failed: "+msg.err.Error(), true)
		} else {
			m.setStatus("done: "+msg.title, false)
		}
		return m, tea.Batch(installedCmd(), updatesCmd())

	case queueMsg:
		m.pending = &pendingAction{title: msg.title, argv: msg.argv}
		// only a command that literally starts with sudo needs us to warm
		// credentials; an aur helper asks for root itself, so leave it alone
		if len(msg.argv) > 0 && msg.argv[0] == "sudo" {
			return m, sudoProbeCmd()
		}
		return m, m.startPending()

	case sudoProbeMsg:
		if msg.ok {
			return m, m.startPending()
		}
		// ask for the password interactively, then continue
		return m, tea.ExecProcess(exec.Command("sudo", "-v"), func(err error) tea.Msg {
			return sudoWarmedMsg{err: err}
		})

	case sudoWarmedMsg:
		if msg.err != nil {
			m.pending = nil
			m.setStatus("sudo authentication failed", true)
			return m, nil
		}
		return m, m.startPending()
	}

	// forward cursor blink messages to the focused input
	if m.focus == focusInput {
		var cmd tea.Cmd
		if m.tab == tabSearch {
			m.searchInput, cmd = m.searchInput.Update(msg)
		} else if m.tab == tabUninstall {
			m.filterInput, cmd = m.filterInput.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m *model) setStatus(s string, isErr bool) {
	m.status = s
	m.statusErr = isErr
}

func (m *model) buttonsFor() []string {
	switch m.tab {
	case tabSearch:
		return []string{"download"}
	case tabUninstall:
		return []string{"uninstall"}
	default:
		return []string{"update", "update all"}
	}
}

func (m *model) listLen() int {
	switch m.tab {
	case tabSearch:
		return len(m.results)
	case tabUninstall:
		return len(m.filteredInstalled())
	default:
		return len(m.updates)
	}
}

func (m *model) moveCursor(delta int) {
	switch m.tab {
	case tabSearch:
		m.searchCur = clamp(m.searchCur+delta, len(m.results))
	case tabUninstall:
		m.instCur = clamp(m.instCur+delta, len(m.filteredInstalled()))
	case tabUpdates:
		m.updCur = clamp(m.updCur+delta, len(m.updates))
	}
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	if k == "ctrl+c" {
		return m, tea.Quit
	}

	// tab switching works everywhere except while a command runs
	if !m.busy {
		switch k {
		// ctrl+q/w/e switch tabs from anywhere, including while typing
		case "ctrl+q":
			return m.switchTab(tabSearch)
		case "ctrl+w":
			return m.switchTab(tabUninstall)
		case "ctrl+e":
			return m.switchTab(tabUpdates)
		}
	}

	if m.busy {
		return m, nil
	}

	// cycle focus: input -> list -> buttons -> input
	if k == "tab" || k == "shift+tab" {
		dir := 1
		if k == "shift+tab" {
			dir = -1
		}
		f := int(m.focus) + dir
		if f > int(focusBtn) {
			f = int(focusInput)
		}
		if f < int(focusInput) {
			f = int(focusBtn)
		}
		// updates tab has no input
		if m.tab == tabUpdates && f == int(focusInput) {
			f = int(focusList)
		}
		m.focus = focusArea(f)
		m.syncInputs()
		var cmd tea.Cmd
		if m.focus == focusInput {
			if m.tab == tabSearch {
				cmd = m.searchInput.Focus()
			} else {
				cmd = m.filterInput.Focus()
			}
		}
		return m, cmd
	}

	// list navigation works from anywhere
	switch k {
	case "up":
		m.moveCursor(-1)
		return m, nil
	case "down":
		m.moveCursor(1)
		return m, nil
	case "pgup":
		m.moveCursor(-10)
		return m, nil
	case "pgdown":
		m.moveCursor(10)
		return m, nil
	}

	switch k {
	case "esc":
		if m.focus == focusInput {
			m.focus = focusList
		} else if m.focus == focusBtn {
			m.focus = focusList
		} else {
			m.focus = focusInput
		}
		if m.tab == tabUpdates {
			m.focus = focusList
		}
		m.syncInputs()
		return m, nil

	case "home":
		if m.focus != focusInput {
			m.moveCursor(-1 << 30)
			return m, nil
		}
	case "end":
		if m.focus != focusInput {
			m.moveCursor(1 << 30)
			return m, nil
		}

	case "left":
		if m.focus == focusBtn && m.btnIdx > 0 {
			m.btnIdx--
		} else if m.focus == focusInput {
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		return m, nil

	case "right":
		if m.focus == focusBtn && m.btnIdx < len(m.buttonsFor())-1 {
			m.btnIdx++
		} else if m.focus == focusInput {
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		return m, nil

	case "h":
		if m.focus != focusInput {
			m.nextInstaller()
			m.setStatus("install with: "+m.installer, false)
			return m, nil
		}

	case "r":
		// re-fetch the active tab's data: updates are pulled fresh from the
		// repos and the aur so newly published updates show up
		if m.focus != focusInput {
			return m, m.refreshTab()
		}

	case "enter":
		return m.handleEnter()

	case "q":
		if m.focus != focusInput {
			return m, tea.Quit
		}
	}

	// typing goes to the focused input
	if m.focus == focusInput {
		var cmd tea.Cmd
		if m.tab == tabSearch {
			m.searchInput, cmd = m.searchInput.Update(msg)
		} else if m.tab == tabUninstall {
			m.filterInput, cmd = m.filterInput.Update(msg)
		}
		if m.tab == tabUninstall {
			m.instCur = 0
		}
		return m, cmd
	}
	return m, nil
}

func (m *model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusInput:
		if m.tab == tabSearch {
			q := strings.TrimSpace(m.searchInput.Value())
			if q == "" {
				m.setStatus("type something to search", false)
				return m, nil
			}
			m.searching = true
			m.setStatus("searching '"+q+"' ...", false)
			return m, tea.Batch(searchCmd(q), func() tea.Msg { return m.spin.Tick() })
		}
		m.focus = focusList
		m.syncInputs()
		return m, nil

	case focusList:
		if m.listLen() == 0 {
			m.setStatus("nothing selected", false)
			return m, nil
		}
		m.focus = focusBtn
		m.btnIdx = 0
		return m, nil

	default: // focusBtn
		return m.activate(m.btnIdx)
	}
}

// installArgs builds the command for installing a single package with the
// currently selected installer (pacman, yay or paru).
func (m *model) installArgs(name string, aur bool) []string {
	if m.installer == "pacman" {
		return []string{"sudo", "pacman", "-S", "--noconfirm", name}
	}
	return append([]string{m.installer, "-S"}, append(aurFlags(m.installer), name)...)
}

// activate runs the action of the chosen button.
func (m *model) activate(btn int) (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabSearch:
		if len(m.results) == 0 {
			m.setStatus("no results to download", false)
			return m, nil
		}
		p := m.results[clamp(m.searchCur, len(m.results))]
		if !m.canInstall(p.aur) {
			return m, nil
		}
		return m, queue("download "+p.name+" with "+m.installer, m.installArgs(p.name, p.aur))

	case tabUninstall:
		list := m.filteredInstalled()
		if len(list) == 0 {
			m.setStatus("no package selected", false)
			return m, nil
		}
		p := list[clamp(m.instCur, len(list))]
		argv := []string{"sudo", "pacman", "-Rns", "--noconfirm", p.name}
		return m, queue("uninstall "+p.name, argv)

	case tabUpdates:
		if len(m.updates) == 0 {
			m.setStatus("nothing to update", false)
			return m, nil
		}
		if btn == 1 || (btn == 0 && m.listLen() == 0) {
			// update all
			var argv []string
			if m.installer == "pacman" {
				argv = []string{"sudo", "pacman", "-Syu", "--noconfirm"}
			} else {
				argv = append([]string{m.installer, "-Syu"}, aurFlags(m.installer)...)
			}
			return m, queue("update all with "+m.installer, argv)
		}
		u := m.updates[clamp(m.updCur, len(m.updates))]
		if !m.canInstall(u.aur) {
			return m, nil
		}
		return m, queue("update "+u.name+" with "+m.installer, m.installArgs(u.name, u.aur))
	}
	return m, nil
}

// queue stores the action and asks for sudo access first if needed.
func queue(title string, argv []string) tea.Cmd {
	return func() tea.Msg {
		return queueMsg{title: title, argv: argv}
	}
}

type queueMsg struct {
	title string
	argv  []string
}

// startPending begins the queued action once sudo is usable.
func (m *model) startPending() tea.Cmd {
	if m.pending == nil {
		return nil
	}
	p := m.pending
	m.pending = nil
	m.busy = true
	m.actionTitle = p.title
	m.logLines = nil
	m.setStatus("running: "+p.title, false)
	return tea.Batch(
		startActionCmd(m.program, p.title, p.argv),
		func() tea.Msg { return m.spin.Tick() },
	)
}

// refreshTab re-fetches the active tab's underlying data.
func (m *model) refreshTab() tea.Cmd {
	switch m.tab {
	case tabUpdates:
		m.updLoading = true
		m.setStatus("checking repos and aur for updates ...", false)
		return updatesCmd()
	case tabUninstall:
		m.instLoading = true
		m.setStatus("re-reading installed packages ...", false)
		return installedCmd()
	}
	return nil
}

func (m *model) switchTab(t tabKind) (tea.Model, tea.Cmd) {
	m.tab = t
	m.btnIdx = 0
	m.focus = focusList
	if t == tabSearch {
		m.focus = focusInput
	}
	m.syncInputs()
	var cmd tea.Cmd
	if m.focus == focusInput {
		cmd = m.searchInput.Focus()
	}
	switch t {
	case tabUninstall:
		// re-fetch when empty, still loading, or the list is more than
		// five minutes old so removals made outside the tui show up
		if !m.instLoading && (len(m.installed) == 0 ||
			m.instStamp.IsZero() || time.Since(m.instStamp) > 5*time.Minute) {
			m.instLoading = true
			cmd = tea.Batch(cmd, installedCmd())
		}
	case tabUpdates:
		// always re-check the repos and the aur when the data is stale so
		// freshly published updates are picked up
		if !m.updLoading && (len(m.updates) == 0 ||
			m.updStamp.IsZero() || time.Since(m.updStamp) > 5*time.Minute) {
			m.updLoading = true
			cmd = tea.Batch(cmd, updatesCmd())
		}
	}
	m.setStatus("ready", false)
	return m, cmd
}

// -------------------------------------------------------------------- rendering

// View renders the whole screen: header, tabs, the active tab body,
// an optional log pane for running commands, status and key hints.
func (m *model) View() string {
	w := m.width
	if w < 24 {
		w = 24
	}
	h := m.height
	if h < 8 {
		h = 8
	}

	hasInput := m.tab != tabUpdates
	used := 7 // header, tabs, button block (3 lines), status, hints
	if hasInput {
		used++
	}

	logH := 0
	if m.busy || len(m.logLines) > 0 {
		logH = h / 3
		if logH < 4 {
			logH = 4
		}
		if logH > 14 {
			logH = 14
		}
		logH++ // label line
	}

	listH := h - used - logH
	if listH < 3 {
		listH = 3
		if h-used-logH > 0 {
			listH = h - used - logH
		}
	}

	lines := []string{m.headerView(w), m.tabsView()}
	if hasInput {
		lines = append(lines, m.inputView())
	}
	lines = append(lines, m.listView(w, listH), m.buttonsView())
	if logH > 0 {
		lines = append(lines, m.logView(w, logH)...)
	}
	lines = append(lines, m.statusView(w), m.hintView(w))
	return strings.Join(lines, "\n")
}

func (m *model) headerView(w int) string {
	left := titleStyle.Render("get my aur installer")
	right := dimStyle.Render("install with: " + m.installer)
	if version != "" {
		right = dimStyle.Render(version + " | install with: " + m.installer)
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left + " " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *model) tabsView() string {
	tabs := []struct {
		key   string
		kind  tabKind
		label string
	}{
		{"^q", tabSearch, "search"},
		{"^w", tabUninstall, "uninstall"},
		{"^e", tabUpdates, "updates"},
	}
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		label := "[" + t.key + "] " + t.label
		if m.tab == t.kind {
			parts = append(parts, tabActiveStyle.Render(label))
		} else {
			parts = append(parts, tabIdleStyle.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

func (m *model) inputView() string {
	if m.tab == tabUninstall {
		return m.filterInput.View()
	}
	return m.searchInput.View()
}

// listView renders a scrolling window of the active tab's rows.
func (m *model) listView(w, h int) string {
	loading, empty, rows := m.listContent()
	if len(rows) == 0 {
		out := make([]string, 0, h)
		if loading {
			out = append(out, dimStyle.Render(m.spin.View()+" "+empty))
		} else {
			out = append(out, dimStyle.Render(empty))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return strings.Join(out, "\n")
	}

	sel := m.selectedIndex()
	start := windowStart(len(rows), sel, h)
	out := make([]string, 0, h)
	for i := start; i < len(rows) && len(out) < h; i++ {
		marker := "  "
		if i == sel {
			marker = "> "
		}
		content := trunc(marker+rows[i], w)
		if i == sel {
			out = append(out, selectedStyle.Render(pad(content, w)))
		} else {
			out = append(out, logStyle.Render(pad(content, w)))
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// listContent returns the rows of the active tab plus its empty-state text.
func (m *model) listContent() (loading bool, empty string, rows []string) {
	switch m.tab {
	case tabSearch:
		loading = m.searching
		if loading {
			empty = "searching ..."
		} else if len(m.results) == 0 {
			empty = "no results - type a query and press enter"
		}
		for _, p := range m.results {
			rows = append(rows, searchRow(p))
		}
	case tabUninstall:
		loading = m.instLoading
		if loading {
			empty = "reading installed packages ..."
		} else if len(m.installed) == 0 {
			empty = "no installed packages found"
		} else if len(m.filteredInstalled()) == 0 {
			empty = "no installed package matches the filter"
		}
		for _, p := range m.filteredInstalled() {
			rows = append(rows, installedRow(p))
		}
	default:
		loading = m.updLoading
		if loading {
			empty = "checking for updates ..."
		} else if len(m.updates) == 0 {
			empty = "everything is up to date"
		}
		for _, u := range m.updates {
			rows = append(rows, updateRow(u))
		}
	}
	return loading, empty, rows
}

func (m *model) selectedIndex() int {
	switch m.tab {
	case tabSearch:
		return clamp(m.searchCur, len(m.results))
	case tabUninstall:
		return clamp(m.instCur, len(m.filteredInstalled()))
	default:
		return clamp(m.updCur, len(m.updates))
	}
}

func searchRow(p pkg) string {
	s := p.name
	if p.repo != "" {
		s += "  [" + p.repo + "]"
	}
	if p.version != "" {
		s += "  " + p.version
	}
	if p.installed {
		s += "  [installed]"
	}
	if d := strings.TrimSpace(p.desc); d != "" {
		s += "  " + lower(d)
	}
	return s
}

func installedRow(p pkg) string {
	src := "[repo]"
	if p.aur {
		src = "[aur]"
	}
	s := p.name + "  " + p.version + "  " + src
	return s
}

func updateRow(u updateItem) string {
	src := "[repo]"
	if u.aur {
		src = "[aur]"
	}
	return u.name + "  " + u.oldver + " -> " + u.newver + "  " + src
}

func (m *model) buttonsView() string {
	btns := m.buttonsFor()
	parts := make([]string, 0, len(btns))
	for i, b := range btns {
		if i > 0 {
			parts = append(parts, " ")
		}
		if m.focus == focusBtn && i == m.btnIdx {
			parts = append(parts, btnFocusedStyle.Render(b))
		} else {
			parts = append(parts, btnStyle.Render(b))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// logView renders the command log pane, one line per slice entry.
func (m *model) logView(w, h int) []string {
	inner := h - 1
	out := make([]string, 0, h)

	head := "log"
	if m.actionTitle != "" {
		head = "log: " + lower(m.actionTitle)
	}
	out = append(out, dimStyle.Render(trunc(head, w)))

	src := m.logLines
	if len(src) > inner {
		src = src[len(src)-inner:]
	}
	for _, ln := range src {
		t := lower(ansi.Strip(ln))
		t = strings.TrimSpace(t)
		out = append(out, logStyle.Render(trunc(t, w)))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

func (m *model) statusView(w int) string {
	if m.busy {
		return statusStyle.Render(m.spin.View() + " " + trunc(lower(m.actionTitle), w-4))
	}
	if m.searching {
		return statusStyle.Render(m.spin.View() + " " + trunc(lower(m.status), w-4))
	}
	st := statusStyle
	if m.statusErr {
		st = errStyle
	} else if strings.TrimSpace(m.status) == "" || m.status == "ready" {
		st = dimStyle
	}
	return st.Render(trunc(lower(m.status), w))
}

func (m *model) hintView(w int) string {
	h := "tab focus | enter select/run | up/down move | esc back | " +
		"ctrl+q/w/e tabs | h install with (" + m.installer + ") | r recheck | q quit"
	return hintStyle.Render(trunc(h, w))
}

// ------------------------------------------------------------------------ entry

// version is stamped at build time by the PKGBUILD (-ldflags -X main.version=...)
var version string

func main() {
	m := newModel()
	if version == "" {
		version = "dev"
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.program = p
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
