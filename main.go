package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// --------------------------------------------------------------------------- styles

// palette is the app's colours. every style in the ui is derived from it,
// and each style carries the same background so no cell is ever left
// transparent and the whole window really is black.
type palette struct {
	// bg and fg are the canvas: the background every cell is painted with
	// and the default text colour. without an explicit bg the terminal's
	// own background shows through wherever the app does not draw, which
	// is why a light theme has to fill the whole frame, not just recolour
	// the glyphs.
	bg       string
	fg       string
	title    string
	dim      string
	tabOnFg  string
	tabOnBg  string
	tabOffFg string
	selFg    string
	selBg    string
	inputFg  string
	btnFg    string
	btnEdge  string
	btnOnFg  string
	btnOnBg  string
	status   string
	err      string
	log      string
	hint     string
	sep      string
}

// themePalette is the single look of the app: catppuccin mocha, the exact
// palette the app has always shipped with. there is no theme picker, so this
// one is simply the app's colours.
var themePalette = palette{
	bg:       "#1e1e2e",
	fg:       "#cdd6f4",
	title:    "#c6a0f6",
	dim:      "#6c7086",
	tabOnFg:  "#1e1e2e",
	tabOnBg:  "#c6a0f6",
	tabOffFg: "#89b4fa",
	selFg:    "#1e1e2e",
	selBg:    "#89b4fa",
	inputFg:  "#cdd6f4",
	btnFg:    "#cdd6f4",
	btnEdge:  "#585b70",
	btnOnFg:  "#1e1e2e",
	btnOnBg:  "#a6e3a1",
	status:   "#f9e2af",
	err:      "#f38ba8",
	log:      "#a6adc8",
	hint:     "#6c7086",
	sep:      "#313244",
}

// styles carries every lipgloss style the ui renders with.
type styles struct {
	// bg paints a whole line or block in the window's background, which is
	// what keeps the frame solid black instead of leaking the terminal's
	// own colour through the gaps.
	bg                 lipgloss.Style
	title, dim         lipgloss.Style
	tabActive, tabIdle lipgloss.Style
	selected           lipgloss.Style
	inputPrompt        lipgloss.Style
	inputFocused       lipgloss.Style
	inputBlurred       lipgloss.Style
	btn, btnFocused    lipgloss.Style
	status, err, ok    lipgloss.Style
	log, hint, sep     lipgloss.Style
}

// newStyles builds the style set for the app's single black theme.
func newStyles() styles {
	p := themePalette
	rounded := lipgloss.RoundedBorder()
	// every style carries the window's own background. a terminal shows its
	// own background in any cell an app leaves transparent, so the black
	// window has to be painted on each element rather than only recolour
	// the foreground.
	bg := lipgloss.Color(p.bg)
	return styles{
		bg: lipgloss.NewStyle().Background(bg),
		title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(p.title)).
			Background(bg),
		dim: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.dim)).
			Background(bg),
		tabActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(p.tabOnFg)).
			Background(lipgloss.Color(p.tabOnBg)).
			Padding(0, 2),
		tabIdle: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.tabOffFg)).
			Background(bg),
		selected: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.selFg)).
			Background(lipgloss.Color(p.selBg)),
		inputPrompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.btnOnBg)).
			Background(bg),
		inputFocused: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.inputFg)).
			Background(bg),
		inputBlurred: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.dim)).
			Background(bg),
		btn: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.btnFg)).
			Background(bg).
			BorderStyle(rounded).
			BorderForeground(lipgloss.Color(p.btnEdge)).
			Padding(0, 2),
		btnFocused: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(p.btnOnFg)).
			Background(lipgloss.Color(p.btnOnBg)).
			BorderStyle(rounded).
			BorderForeground(lipgloss.Color(p.btnOnBg)).
			Padding(0, 2),
		status: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.status)).
			Background(bg),
		err: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.err)).
			Background(bg),
		ok: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.btnOnBg)).
			Background(bg),
		log: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.log)).
			Background(bg),
		hint: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.hint)).
			Background(bg),
		sep: lipgloss.NewStyle().
			Foreground(lipgloss.Color(p.sep)).
			Background(bg),
	}
}

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

// searchQueries turns what was typed into the queries sent to pacman and the
// aur helpers. the phrase is used as it is, because descriptions contain it
// ("visual studio code" finds the official code package), and a dash joined
// form is used as well so the package literally named that way comes back
// too ("visual-studio-code").
func searchQueries(query string) []string {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	out := []string{q}
	if fields := strings.Fields(q); len(fields) > 1 {
		if dashed := strings.Join(fields, "-"); dashed != q {
			out = append(out, dashed)
		}
	}
	return out
}

// maxSearchResults caps how many rows one search can return, so a broad
// query cannot stall the list.
const maxSearchResults = 300

// ----------------------------------------------------------------- aur rpc

// aurRPC is the aur's http interface. asking it directly is what makes a
// search quick: one request answers a query, where a helper would first have
// to be started, read its own config and only then ask the same server.
const aurRPC = "https://aur.archlinux.org/rpc/v5"

// aurRPCRow is one package as the aur describes it over its api.
type aurRPCRow struct {
	Name        string   `json:"Name"`
	Version     string   `json:"Version"`
	Description string   `json:"Description"`
	URL         string   `json:"URL"`
	Depends     []string `json:"Depends"`
	License     []string `json:"License"`
	Maintainer  string   `json:"Maintainer"`
	OutOfDate   *int     `json:"OutOfDate"`
}

// aurDecode reads the shared rpc envelope and fails on the error the aur
// reports in the body of a successful response.
func aurDecode(raw json.RawMessage, v any) error {
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Error != "" {
		return errors.New(env.Error)
	}
	return json.Unmarshal(raw, v)
}

// aurSearch asks the aur for one term, by name and description, which is
// what the helpers ask for too. the rows come back ranked nowhere: the
// search's own ranking decides the order, whatever the row's source.
func aurSearch(ctx context.Context, term string) ([]pkg, error) {
	raw, err := aurRaw(ctx, "/search/"+url.PathEscape(term)+"?by=name-desc")
	if err != nil {
		return nil, err
	}
	return parseAurSearch(raw)
}

// parseAurSearch turns an aur search answer into rows.
func parseAurSearch(raw json.RawMessage) ([]pkg, error) {
	var out struct {
		Results []aurRPCRow `json:"results"`
	}
	if err := aurDecode(raw, &out); err != nil {
		return nil, err
	}
	items := make([]pkg, 0, len(out.Results))
	for _, r := range out.Results {
		if r.Name == "" {
			continue
		}
		items = append(items, pkg{
			name:    lower(r.Name),
			repo:    "aur",
			version: r.Version,
			desc:    r.Description,
			aur:     true,
		})
	}
	return items, nil
}

// aurInfo asks the aur for what one package is: its page, its description
// and the chain of packages it needs.
func aurInfo(ctx context.Context, name string) (pkgDetail, error) {
	raw, err := aurRaw(ctx, "/info?arg[]="+url.QueryEscape(name))
	if err != nil {
		return pkgDetail{}, err
	}
	return parseAurInfo(raw, name)
}

// parseAurInfo reads one aur package's facts out of an api answer.
func parseAurInfo(raw json.RawMessage, name string) (pkgDetail, error) {
	var out struct {
		Results []aurRPCRow `json:"results"`
	}
	if err := aurDecode(raw, &out); err != nil {
		return pkgDetail{}, err
	}
	if len(out.Results) == 0 {
		return pkgDetail{}, errors.New("no aur package named " + lower(name))
	}
	r := out.Results[0]
	return pkgDetail{
		name:       lower(r.Name),
		repo:       "aur",
		version:    r.Version,
		desc:       strings.TrimSpace(r.Description),
		url:        strings.TrimSpace(r.URL),
		maintainer: r.Maintainer,
		licenses:   r.License,
		depends:    r.Depends,
	}, nil
}

// aurRaw is one http round trip to the aur api.
func aurRaw(ctx context.Context, path string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, aurRPC+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gmai/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aur rpc: http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// helperSearch is the fallback aur search: it starts yay, then paru, and
// takes the first one that answers. only reached when the aur api itself
// cannot be reached.
func helperSearch(ctx context.Context, q string) ([]pkg, error) {
	var errs []string
	for _, helper := range []string{"yay", "paru"} {
		if !hasBin(helper) {
			continue
		}
		b, err := exec.CommandContext(ctx, helper, "-Ssa", q).Output()
		if err != nil && len(b) == 0 {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				errs = append(errs, helper+": "+err.Error())
			}
			continue
		}
		return parseSearch(string(b), true), nil
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return nil, errors.New("no aur search available")
}

// runSearch asks pacman for the official repos and the aur api for the aur,
// and merges both into one list. official rows are collected first, so an
// official package always wins a name clash.
func runSearch(queries []string) ([]pkg, []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	type res struct {
		src  string
		rows []pkg
		err  error
		aur  bool
	}
	out := make([]res, len(queries)*2)
	var wg sync.WaitGroup
	for i, q := range queries {
		// the official repos live on disk, so pacman answers this one
		// without going near the network at all
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			b, err := exec.CommandContext(ctx, "pacman", "-Ss", q).Output()
			if err != nil && len(b) == 0 {
				out[i*2] = res{src: "pacman", err: err}
				return
			}
			out[i*2] = res{src: "pacman", rows: parseSearch(string(b), false)}
		}(i, q)

		// the aur is one http request for the whole answer
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			rows, err := aurSearch(ctx, q)
			if err != nil {
				rows, err = helperSearch(ctx, q)
			}
			out[i*2+1] = res{src: "aur", rows: rows, err: err, aur: true}
		}(i, q)
	}
	wg.Wait()

	var results []pkg
	var errs []string
	seen := map[string]bool{}
	collect := func(wantAur bool) {
		for _, r := range out {
			if r.aur != wantAur {
				continue
			}
			if r.err != nil && len(r.rows) == 0 {
				// exit errors with no output are "no results"
				var ee *exec.ExitError
				if !errors.As(r.err, &ee) {
					errs = append(errs, r.src+": "+r.err.Error())
				}
				continue
			}
			for _, p := range r.rows {
				// the same package can live in several repos and in
				// several queries; keep one row per name, and because
				// official rows are collected first that row is the
				// official one
				if seen[p.name] {
					continue
				}
				seen[p.name] = true
				results = append(results, p)
			}
		}
	}
	collect(false)
	collect(true)
	return results, errs
}

// norm reduces a name or a query to letters and digits, lowercased, so
// "visual studio code", "visual-studio-code" and "VisualStudioCode" all key
// out the same way. packages are named with dashes while people type
// spaces, and matching on this key is what lets the exact package win.
func norm(s string) string {
	var b strings.Builder
	for _, r := range lower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchRank scores how well one package answers a query, lower is better:
// 0 the name is the query, 1 the name starts with it, 2 the name contains
// it, 3 every word of the query is somewhere in the name, 4 only some of
// them are, 5 the description mentions it, 6 no real match.
func matchRank(p pkg, query string) int {
	n := lower(p.name)
	q := lower(strings.TrimSpace(query))
	if n == "" || q == "" {
		return 6
	}
	nn, nq := norm(n), norm(q)
	switch {
	case nn == nq:
		return 0
	case strings.HasPrefix(nn, nq):
		return 1
	case strings.Contains(nn, nq):
		return 2
	}
	// the words of a query can be spread over the name in any order, which
	// is how "visual studio code" finds visual-studio-code at all
	if words := strings.Fields(q); len(words) > 1 {
		hits := 0
		for _, w := range words {
			if nw := norm(w); nw != "" && strings.Contains(nn, nw) {
				hits++
			}
		}
		if hits == len(words) {
			return 3
		}
		if hits > 0 {
			return 4
		}
		// nothing in the name: the description may still spell the
		// whole phrase out
		nd := norm(p.desc)
		if nd != "" && strings.Contains(nd, nq) {
			return 5
		}
	}
	if strings.Contains(lower(p.desc), q) {
		return 5
	}
	return 6
}

// repoRank orders packages by where they come from: core, then extra, then
// any other repo (the cachyos mirrors and friends), the aur last. it only
// decides ties, after the name match has had its say.
func repoRank(p pkg) int {
	if p.aur || lower(p.repo) == "aur" {
		return 3
	}
	switch lower(p.repo) {
	case "core":
		return 0
	case "extra":
		return 1
	}
	return 2
}

// rankResults orders a result set: the package asked for is the first row,
// everything merely related to it lines up underneath, and within one rank
// official packages come before aur ones.
func rankResults(items []pkg, query string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ra, rb := matchRank(a, query), matchRank(b, query); ra != rb {
			return ra < rb
		}
		if ra, rb := repoRank(a), repoRank(b); ra != rb {
			return ra < rb
		}
		return lower(a.name) < lower(b.name)
	})
}

// searchCmd queries the arch repos (pacman) plus the aur via yay and paru,
// then ranks what comes back so the best match sits on top.
func searchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		queries := searchQueries(query)
		if len(queries) == 0 {
			return searchMsg{query: query}
		}
		results, errs := runSearch(queries)
		rankResults(results, query)
		if len(results) > maxSearchResults {
			results = results[:maxSearchResults]
		}
		var err error
		if len(results) == 0 && len(errs) > 0 {
			err = errors.New(strings.Join(errs, "; "))
		}
		return searchMsg{results: results, err: err, query: query}
	}
}

// ----------------------------------------------------------- alternatives

// the alternatives shown under a package are taken from the results already
// on screen, so opening them costs nothing: the search that found the
// package found the packages that mention it too.

// nameWords splits a package name into its words on every non letter, so
// "neofetch-git" reads as neofetch, git.
func nameWords(s string) []string {
	return strings.FieldsFunc(lower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// sharedContent reports whether two descriptions talk about the same thing:
// at least one word of real content (six letters or more, which leaves out
// the filler every description opens with) appears in both.
func sharedContent(a, b string) bool {
	long := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, w := range strings.Fields(s) {
			if len([]rune(w)) >= 6 {
				out[w] = true
			}
		}
		return out
	}
	aw := long(a)
	if len(aw) == 0 {
		return false
	}
	for w := range long(b) {
		if aw[w] {
			return true
		}
	}
	return false
}

// altRank scores a candidate as a stand-in for base, lower is better: 0 the
// description names base itself ("a neofetch like tool"), 1 the two names
// are built from the same first word, 2 the descriptions talk about the
// same things, 3 nothing in common.
func altRank(c, base pkg) int {
	bn := norm(base.name)
	// a name short enough to turn up in unrelated prose says nothing, so
	// the strongest signal needs a name worth mentioning
	if len(bn) >= 5 && strings.Contains(norm(c.desc), bn) {
		return 0
	}
	bw, cw := nameWords(base.name), nameWords(c.name)
	if len(bw) > 0 && len(cw) > 0 && bw[0] == cw[0] {
		return 1
	}
	if sharedContent(lower(base.desc), lower(c.desc)) {
		return 2
	}
	return 3
}

// pickAlternatives keeps the candidates that really do stand in for base and
// orders them: the ones whose description names base first, then the ones
// built from the same word, then the ones that talk about the same subject.
func pickAlternatives(results []pkg, base pkg) []pkg {
	var items []pkg
	for _, p := range results {
		if lower(p.name) == lower(base.name) {
			continue
		}
		if altRank(p, base) > 2 {
			continue
		}
		items = append(items, p)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ra, rb := altRank(a, base), altRank(b, base); ra != rb {
			return ra < rb
		}
		if ra, rb := repoRank(a), repoRank(b); ra != rb {
			return ra < rb
		}
		return lower(a.name) < lower(b.name)
	})
	if len(items) > 120 {
		items = items[:120]
	}
	return items
}

// altShown is how many alternatives fit under a package's own facts before
// the rest is counted instead of listed.
const altShown = 10

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

// ------------------------------------------------------------------- arch news

// archNewsRSS is the official arch linux news feed.
const archNewsRSS = "https://archlinux.org/feeds/news/"

// newsItem is one published arch news entry.
type newsItem struct {
	title string
	link  string
	pub   time.Time
}

type newsMsg struct {
	items   []newsItem
	err     error
	checked time.Time
}

// parseNews reads the arch news rss feed into dated items, newest first.
func parseNews(data string) ([]newsItem, error) {
	var doc struct {
		Channel struct {
			Items []struct {
				Title   string `xml:"title"`
				Link    string `xml:"link"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(data), &doc); err != nil {
		return nil, err
	}
	var out []newsItem
	for _, it := range doc.Channel.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		// an unparsable date still leaves the story usable, it just loses
		// its position in the ordering. the arch feed writes rfc1123 with
		// a numeric offset, which http.ParseTime does not try.
		var pub time.Time
		for _, layout := range []string{time.RFC1123Z, time.RFC1123,
			time.RFC850, time.ANSIC} {
			if t, err := time.Parse(layout, strings.TrimSpace(it.PubDate)); err == nil {
				pub = t
				break
			}
		}
		out = append(out, newsItem{
			title: title,
			link:  strings.TrimSpace(it.Link),
			pub:   pub,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].pub.After(out[j].pub) })
	return out, nil
}

// newsCmd pulls the arch news feed over http. nothing else is spawned.
func newsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, archNewsRSS, nil)
		if err != nil {
			return newsMsg{err: err, checked: time.Now()}
		}
		req.Header.Set("User-Agent", "gmai/"+version)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return newsMsg{err: err, checked: time.Now()}
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return newsMsg{err: err, checked: time.Now()}
		}
		items, err := parseNews(string(b))
		return newsMsg{items: items, err: err, checked: time.Now()}
	}
}

// ----------------------------------------------------------- package info

// pkgDetail is everything `pacman -Si` / the aur api says about one package.
type pkgDetail struct {
	name, repo, version, desc string
	url                       string
	maintainer                string
	licenses                  []string
	depends                   []string
	// depInstalled records which of the dependencies are already on the
	// disk, so the chain reads at a glance
	depInstalled  map[string]bool
	downloadSize  string
	installedSize string
	installed     bool
}

type pkgInfoMsg struct {
	target pkg
	detail pkgDetail
	err    error
}

// pkgInfoState is the open package detail view: the package's own facts,
// the stand-ins picked for it, and how far the pane is scrolled.
type pkgInfoState struct {
	target  pkg
	detail  pkgDetail
	alts    []pkg
	loading bool
	err     error
	scroll  int
}

// splitField splits a pacman info field like "glibc  gcc-libs" into words.
// pacman writes "None" when a field is empty.
func splitField(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || lower(s) == "none" {
		return nil
	}
	return strings.Fields(s)
}

// parsePkgInfo parses `pacman -Si` / `pacman -Qi` output. long values
// continue on indented lines, so those are glued back onto their key.
func parsePkgInfo(out string) pkgDetail {
	var d pkgDetail
	fields := map[string]string{}
	cur := ""
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			cur = ""
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' {
			if cur != "" {
				fields[cur] += " " + strings.TrimSpace(ln)
			}
			continue
		}
		i := strings.Index(ln, ":")
		if i <= 0 {
			cur = ""
			continue
		}
		key := strings.TrimSpace(ln[:i])
		fields[key] = strings.TrimSpace(ln[i+1:])
		cur = key
	}
	d.name = fields["Name"]
	d.repo = fields["Repository"]
	d.version = fields["Version"]
	d.desc = strings.TrimSpace(fields["Description"])
	d.url = fields["URL"]
	d.maintainer = fields["Packager"]
	d.licenses = splitField(fields["Licenses"])
	d.depends = splitField(fields["Depends On"])
	d.downloadSize = fields["Download Size"]
	d.installedSize = fields["Installed Size"]
	return d
}

// pkgInfoCmd reads one package's facts. no sudo: reading a package's
// details never needs root, whatever installer is selected. the aur is
// asked over its api, which answers in one request what a helper only
// answers after being started and reading its own configuration first.
func pkgInfoCmd(p pkg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// the installed set is read locally and instantly, and it is what
		// marks the dependencies that are already on the disk
		installed := localInstalled(ctx)
		if !p.aur {
			b, err := exec.CommandContext(ctx, "pacman", "-Si", p.name).CombinedOutput()
			if err != nil {
				return pkgInfoMsg{target: p, err: infoErr(string(b), err)}
			}
			d := parsePkgInfo(string(b))
			d.depInstalled = markInstalled(d.depends, installed)
			return pkgInfoMsg{target: p, detail: d}
		}
		// an installed aur package sits on the disk already and pacman
		// knows it without asking anyone, sizes included
		if b, err := exec.CommandContext(ctx, "pacman", "-Qi", p.name).CombinedOutput(); err == nil {
			d := parsePkgInfo(string(b))
			d.repo = "aur"
			d.installed = true
			d.depInstalled = markInstalled(d.depends, installed)
			return pkgInfoMsg{target: p, detail: d}
		}
		d, err := aurInfo(ctx, p.name)
		if err != nil {
			return pkgInfoMsg{target: p, err: err}
		}
		d.depInstalled = markInstalled(d.depends, installed)
		return pkgInfoMsg{target: p, detail: d}
	}
}

// localInstalled reads every installed package's name straight off the
// system, which needs no sudo and answers in one call.
func localInstalled(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	b, err := exec.CommandContext(ctx, "pacman", "-Qq").Output()
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if n := strings.TrimSpace(ln); n != "" {
			out[n] = true
		}
	}
	return out
}

// markInstalled records which of a package's dependencies are already
// present, so the chain reads at a glance instead of needing a guess.
func markInstalled(deps []string, installed map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, dep := range deps {
		if installed[depName(dep)] {
			out[dep] = true
		}
	}
	return out
}

// depName is a dependency's name without the version it was pinned to:
// "glibc>=2.34" and "libreadline.so=8-64" are both just what has to be
// there, and the version is a note on the side.
func depName(dep string) string {
	dep = strings.TrimSpace(dep)
	if i := strings.IndexAny(dep, "<>=:"); i > 0 {
		dep = dep[:i]
	}
	return lower(dep)
}

// infoErr is the failure a read-only query reports: whatever the tool wrote,
// and the exit status when it wrote nothing.
func infoErr(out string, err error) error {
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(msg)
}

// detectOS reads /etc/os-release so the header can name the distro the app
// is running on. it is only ever read, never sourced.
func detectOS() string {
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		vals := map[string]string{}
		for _, ln := range strings.Split(string(b), "\n") {
			i := strings.Index(ln, "=")
			if i <= 0 {
				continue
			}
			key := strings.TrimSpace(ln[:i])
			val := strings.Trim(strings.TrimSpace(ln[i+1:]), `"`)
			vals[key] = val
		}
		for _, key := range []string{"PRETTY_NAME", "NAME"} {
			if v := strings.TrimSpace(vals[key]); v != "" {
				return lower(v)
			}
		}
	}
	return runtime.GOOS
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
	tabSettings
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

	// setCur is the cursor within the settings tab's rows.
	setCur int

	// details caches every package fact the detail view has already read,
	// so going back to a package already looked at draws at once.
	details map[string]pkgDetail

	// installer is the tool used for install/update actions: "pacman",
	// "yay" or "paru". pickable in the settings tab and with the h key.
	installer string
	hasYay    bool
	hasParu   bool

	// st holds the app's lipgloss styles (one theme, catppuccin dark).
	st styles

	// updStamp/instStamp record the last successful refresh of the updates
	// and installed lists, so switching tabs re-fetches when they are stale.
	updStamp  time.Time
	instStamp time.Time

	// osName is the distro the app runs on, read once from os-release and
	// shown centred in the header.
	osName string

	// news holds the arch news feed, newest first, shown in the side panel.
	news      []newsItem
	newsErr   error
	newsStamp time.Time

	// info is the search tab's detail view. it follows the selection: the
	// row the cursor sits on decides which package it shows, and it is nil
	// while there is no selection to describe.
	info *pkgInfoState

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
	st := newStyles()

	si := textinput.New()
	si.Prompt = "> "
	si.PromptStyle = st.inputPrompt
	si.TextStyle = st.inputFocused
	si.PlaceholderStyle = st.dim
	si.Placeholder = "type a package name and press enter"
	si.CharLimit = 128

	fi := textinput.New()
	fi.Prompt = "> "
	fi.PromptStyle = st.inputPrompt
	fi.TextStyle = st.inputFocused
	fi.PlaceholderStyle = st.dim
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

	m := &model{
		width:       80,
		height:      24,
		instLoading: true,
		updLoading:  true,
		installer:   installer,
		hasYay:      hasYay,
		hasParu:     hasParu,
		st:          st,
		spin:        sp,
		status:      "ready",
		osName:      detectOS(),
	}

	// the inputs are built from the model's styles, so a theme swap can
	// restyle them through syncInputs
	si.Prompt = "> "
	si.CharLimit = 128
	si.Placeholder = "type a package name and press enter"
	fi.Prompt = "> "
	fi.CharLimit = 128
	fi.Placeholder = "filter installed packages"
	m.searchInput, m.filterInput = si, fi
	m.syncInputs()

	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.searchInput.Focus(), installedCmd(), updatesCmd(), newsCmd())
}

// syncInputs applies focus and the active theme's styles to both inputs.
func (m *model) syncInputs() {
	si, fi := m.searchInput, m.filterInput

	si.PromptStyle = m.st.inputPrompt
	fi.PromptStyle = m.st.inputPrompt
	if m.tab == tabSearch && m.focus == focusInput {
		si.TextStyle = m.st.inputFocused
		si.PlaceholderStyle = m.st.dim
		si.Focus()
	} else {
		si.TextStyle = m.st.inputBlurred
		si.PlaceholderStyle = m.st.inputBlurred
		si.Blur()
	}
	if m.tab == tabUninstall && m.focus == focusInput {
		fi.TextStyle = m.st.inputFocused
		fi.PlaceholderStyle = m.st.dim
		fi.Focus()
	} else {
		fi.TextStyle = m.st.inputBlurred
		fi.PlaceholderStyle = m.st.inputBlurred
		fi.Blur()
	}

	m.searchInput, m.filterInput = si, fi
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
			// nothing is selected any more, so nothing is described
			m.info = nil
			m.setStatus("no results for '"+msg.query+"'", false)
			return m, nil
		}
		m.setStatus(fmt.Sprintf("found %d packages", len(msg.results)), false)
		// the first row is already the selection, so its detail view is
		// open the moment the results land
		return m, m.followSelection()

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

	case newsMsg:
		m.newsStamp = msg.checked
		if msg.err != nil && len(msg.items) == 0 {
			m.newsErr = msg.err
			return m, nil
		}
		m.newsErr = nil
		m.news = msg.items
		return m, nil

	case pkgInfoMsg:
		// only the view that asked for this package may show it
		if m.info == nil || m.info.target != msg.target {
			return m, nil
		}
		m.info.loading = false
		if msg.err != nil {
			m.info.err = msg.err
			m.setStatus("no info: "+lower(msg.err.Error()), true)
			return m, nil
		}
		m.info.detail = msg.detail
		// remembering it keeps the second look instant
		if m.details == nil {
			m.details = map[string]pkgDetail{}
		}
		m.details[msg.target.name] = msg.detail
		m.setStatus("package info: "+msg.target.name, false)
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
		// the detail view runs on its own selection
		if m.info != nil {
			return []string{"download", "back"}
		}
		return []string{"download"}
	case tabUninstall:
		return []string{"uninstall"}
	case tabUpdates:
		return []string{"update", "update all"}
	}
	// the settings tab edits its rows in place, no action buttons
	return nil
}

// settingsRows are the editable rows of the settings tab: the value each
// row holds is the current selection, cycled with left/right or enter.
func (m *model) settingsRows() []string {
	return []string{"install with"}
}

// settingValue returns the current value of a settings row.
func (m *model) settingValue(row int) string {
	return m.installer
}

// settingOptions lists every choice a settings row offers, in the order it
// is cycled. the installers row offers all three tools so the picker reads
// as a real choice, even though helpers that are not installed are skipped.
func (m *model) settingOptions(row int) []string {
	return []string{"pacman", "yay", "paru"}
}

// settingAvailable reports whether a row's option can actually be used. the
// option that is currently selected always counts, so a stored choice whose
// tool has since been removed still renders as the selection rather than
// leaving the row with nothing marked.
func (m *model) settingAvailable(row int, opt string) bool {
	if opt == m.settingValue(row) {
		return true
	}
	return opt == "pacman" || (opt == "yay" && m.hasYay) || (opt == "paru" && m.hasParu)
}

// cycleSetting steps the given settings row to its next value.
func (m *model) cycleSetting(row int) {
	m.nextInstaller()
	m.setStatus("install with: "+m.installer, false)
}

func (m *model) listLen() int {
	switch m.tab {
	case tabSearch:
		return len(m.results)
	case tabUninstall:
		return len(m.filteredInstalled())
	case tabUpdates:
		return len(m.updates)
	default:
		return len(m.settingsRows())
	}
}

// frameLayout is the vertical budget of one rendered frame: how many lines
// every piece around the body takes, and what is left for it. it is worked
// out once and read by both the renderer and the panel's scroll, so the two
// can never disagree about how tall the panel actually is.
type frameLayout struct {
	used   int // header, tabs, input, strip, buttons, status, hints
	logH   int
	listH  int
	newsW  int
	mainW  int
	strip  string
	hasStr bool
}

func (m *model) layout() frameLayout {
	w, h := m.width, m.height
	if w < 24 {
		w = 24
	}
	if h < 8 {
		h = 8
	}
	var l frameLayout
	l.mainW = w

	// the arch news panel lives in a column on the side, but only when the
	// window is wide enough to spare the room for it. a package that is
	// being read claims that room back, so the column folds into a strip
	// holding just the newest story.
	infoOpen := m.tab == tabSearch && m.info != nil
	if w >= 96 && !infoOpen {
		l.newsW = w * 30 / 100
		if l.newsW < 30 {
			l.newsW = 30
		}
		if l.newsW > 46 {
			l.newsW = 46
		}
	}
	if l.newsW > 0 {
		l.mainW = w - l.newsW - 2
		if l.mainW < 44 {
			l.newsW, l.mainW = 0, w
		}
	}

	// the newest story keeps its line even when the column is gone, so
	// opening a package never hides the news completely
	if l.newsW == 0 {
		l.strip, l.hasStr = m.newsStrip(w)
	}

	l.used = 4 // header, tabs, status, hints
	if m.hasInput() {
		l.used++
	}
	if m.hasButtons() {
		l.used += 3 // the bordered button block
	}
	if l.hasStr {
		l.used++
	}

	if m.busy || len(m.logLines) > 0 {
		l.logH = h / 3
		if l.logH < 4 {
			l.logH = 4
		}
		if l.logH > 14 {
			l.logH = 14
		}
		l.logH++ // label line
	}

	l.listH = h - l.used - l.logH
	// the body is the one thing that can never be squeezed out, so in a
	// window that leaves nothing for it the log gives its lines back
	// first and the news strip after it
	if l.listH < 1 {
		l.logH = h - l.used - 1
		if l.logH < 0 {
			l.logH = 0
		}
		l.listH = h - l.used - l.logH
	}
	if l.listH < 1 && l.hasStr {
		l.hasStr, l.used = false, l.used-1
		l.logH = h - l.used - 1
		if l.logH < 0 {
			l.logH = 0
		}
		l.listH = h - l.used - l.logH
	}
	if l.listH < 1 {
		l.listH = 1
	}
	return l
}

// paneWidth is the width the detail panel is rendered at: the window's own
// width, clamped the way the frame clamps it.
func (m *model) paneWidth() int {
	if m.width < 24 {
		return 24
	}
	return m.width
}

// infoPaneLines is how many lines the detail panel is drawn into, given the
// body is listH lines tall.
func (m *model) infoPaneLines(listH int) int {
	if m.tab != tabSearch || m.info == nil {
		return 0
	}
	return infoPaneH(len(m.infoRows(m.paneWidth())), listH)
}

// moveInfo scrolls an open detail pane by delta lines. it reports whether
// there was a pane to scroll, so the caller can fall back to the list.
func (m *model) moveInfo(delta int) bool {
	if m.tab != tabSearch || m.info == nil {
		return false
	}
	m.info.scroll += delta
	// the panel is read from its offset, so the offset is held between the
	// top and the last line that still has the panel to sit in: one that
	// fits on screen never moves off its own first line
	if max := len(m.infoRows(m.paneWidth())) - m.infoPaneLines(m.layout().listH); m.info.scroll > max {
		m.info.scroll = max
	}
	if m.info.scroll < 0 {
		m.info.scroll = 0
	}
	return true
}

func (m *model) moveCursor(delta int) {
	switch m.tab {
	case tabSearch:
		m.searchCur = clamp(m.searchCur+delta, len(m.results))
	case tabUninstall:
		m.instCur = clamp(m.instCur+delta, len(m.filteredInstalled()))
	case tabUpdates:
		m.updCur = clamp(m.updCur+delta, len(m.updates))
	case tabSettings:
		m.setCur = clamp(m.setCur+delta, len(m.settingsRows()))
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
		// f1-f4 switch tabs from anywhere, including while typing
		case "f1":
			return m.switchTab(tabSearch)
		case "f2":
			return m.switchTab(tabUninstall)
		case "f3":
			return m.switchTab(tabUpdates)
		case "f4":
			return m.switchTab(tabSettings)
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
		// the updates and settings tabs have no input field
		if (m.tab == tabUpdates || m.tab == tabSettings) && f == int(focusInput) {
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

	// list navigation works from anywhere. up and down move the selection,
	// and the search tab's detail view follows that selection, so on that
	// tab pgup/pgdown are what scroll the detail pane instead
	switch k {
	case "up":
		m.moveCursor(-1)
		return m, m.followSelection()
	case "down":
		m.moveCursor(1)
		return m, m.followSelection()
	case "pgup":
		if m.moveInfo(-10) {
			return m, nil
		}
		m.moveCursor(-10)
		return m, m.followSelection()
	case "pgdown":
		if m.moveInfo(10) {
			return m, nil
		}
		m.moveCursor(10)
		return m, m.followSelection()
	}

	switch k {
	case "esc":
		// esc leaves the detail view before it leaves the focus area
		if m.info != nil {
			m.closeInfo()
			m.setStatus("ready", false)
			return m, nil
		}
		if m.focus == focusInput {
			m.focus = focusList
		} else if m.focus == focusBtn {
			m.focus = focusList
		} else {
			m.focus = focusInput
		}
		if m.tab == tabUpdates || m.tab == tabSettings {
			m.focus = focusList
		}
		m.syncInputs()
		return m, nil

	case "home":
		if m.info != nil {
			m.info.scroll = 0
			return m, nil
		}
		if m.focus != focusInput {
			m.moveCursor(-1 << 30)
			return m, m.followSelection()
		}
	case "end":
		if m.info != nil {
			m.info.scroll = 1 << 30
			return m, nil
		}
		if m.focus != focusInput {
			m.moveCursor(1 << 30)
			return m, m.followSelection()
		}

	case "left":
		// on the settings tab left/right step the selected row's value
		if m.tab == tabSettings && m.focus != focusInput {
			m.cycleSetting(m.setCur)
			return m, nil
		}
		if m.focus == focusBtn && m.btnIdx > 0 {
			m.btnIdx--
		} else if m.focus == focusInput {
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		return m, nil

	case "right":
		// step the value the other way on the settings tab
		if m.tab == tabSettings && m.focus != focusInput {
			m.cycleSetting(m.setCur)
			return m, nil
		}
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
			// on the settings tab h edits the selected row, so it agrees
			// with left/right/enter instead of always touching the installer
			if m.tab == tabSettings {
				m.cycleSetting(m.setCur)
				return m, nil
			}
			m.nextInstaller()
			m.setStatus("install with: "+m.installer, false)
			return m, nil
		}

	case "r":
		// re-fetch the active tab's data: updates are pulled fresh from the
		// repos and the aur so newly published updates show up. the arch
		// news panel is re-pulled too when its copy is over half an hour old
		if m.focus != focusInput {
			cmd := m.refreshTab()
			if m.newsStamp.IsZero() || time.Since(m.newsStamp) > 30*time.Minute {
				cmd = tea.Batch(cmd, newsCmd())
			}
			return m, cmd
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
			// the old results and their detail view are about to be
			// replaced, so the pane goes first
			m.info = nil
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
		// the settings tab has no action buttons; enter steps the value
		if m.tab == tabSettings {
			m.cycleSetting(m.setCur)
			return m, nil
		}
		// enter on a search result takes over the buttons, which is how
		// the row underneath it gets downloaded
		var cmd tea.Cmd
		if m.tab == tabSearch && m.info == nil {
			cmd = m.openInfo()
		}
		m.focus = focusBtn
		m.btnIdx = 0
		return m, cmd

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

// openInfo loads the selected search result's detail view.
func (m *model) openInfo() tea.Cmd {
	list := m.results
	if len(list) == 0 {
		return nil
	}
	return m.showInfoFor(list[clamp(m.searchCur, len(list))])
}

// showInfoFor points the detail view at one package. the alternatives come
// out of the results already on screen, so they cost nothing, and a package
// already read this session opens without asking for it again. it never
// moves the focus: the pane belongs to the selection, not to the key that
// revealed it.
func (m *model) showInfoFor(p pkg) tea.Cmd {
	in := &pkgInfoState{target: p, loading: true, alts: pickAlternatives(m.results, p)}
	if d, ok := m.details[p.name]; ok {
		in.detail, in.loading = d, false
		m.info = in
		m.setStatus("package info: "+p.name, false)
		return nil
	}
	m.info = in
	m.setStatus("reading info for "+p.name+" ...", false)
	return pkgInfoCmd(p)
}

// followSelection keeps the detail view on whichever package the cursor sits
// on, so moving through the results reads as browsing their details. it
// returns the fetch for the package that just became the selection.
func (m *model) followSelection() tea.Cmd {
	if m.tab != tabSearch {
		return nil
	}
	list := m.results
	if len(list) == 0 {
		return nil
	}
	p := list[clamp(m.searchCur, len(list))]
	if m.info != nil && m.info.target == p {
		return nil
	}
	return m.showInfoFor(p)
}

// closeInfo drops the detail view and its text.
func (m *model) closeInfo() {
	m.info = nil
	m.focus = focusList
}

// activate runs the action of the chosen button.
func (m *model) activate(btn int) (tea.Model, tea.Cmd) {
	// the detail view runs its own buttons: download, or leave the view
	if m.tab == tabSearch && m.info != nil {
		if btn == 1 {
			m.closeInfo()
			m.setStatus("ready", false)
			return m, nil
		}
		p := m.info.target
		if !m.canInstall(p.aur) {
			return m, nil
		}
		return m, queue("download "+p.name+" with "+m.installer, m.installArgs(p.name, p.aur))
	}
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
	// the detail view belongs to the search tab only
	m.closeInfo()
	if t == tabSearch {
		m.focus = focusInput
	}
	if t == tabSettings {
		m.setCur = clamp(m.setCur, len(m.settingsRows()))
	}
	m.syncInputs()
	var cmd tea.Cmd
	if m.focus == focusInput {
		cmd = m.searchInput.Focus()
	}
	m.setStatus("ready", false)
	switch t {
	case tabSearch:
		// the detail view belongs to the selection, so it comes back
		// with the row the cursor sits on
		cmd = tea.Batch(cmd, m.followSelection())
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
	return m, cmd
}

// -------------------------------------------------------------------- rendering

// View renders the whole screen: header, tabs, the active tab body,
// an optional log pane for running commands, status and key hints.
// hasInput reports whether the active tab renders a text input. only the
// search and uninstall tabs have one: updates and settings edit nothing
// typed.
func (m *model) hasInput() bool {
	return m.tab == tabSearch || m.tab == tabUninstall
}

// hasButtons reports whether the active tab renders action buttons. the
// settings tab edits its rows in place, so it has none.
func (m *model) hasButtons() bool { return m.tab != tabSettings }

func (m *model) View() string {
	w := m.width
	if w < 24 {
		w = 24
	}
	h := m.height
	if h < 8 {
		h = 8
	}
	// the whole vertical and horizontal budget is worked out once, so the
	// panel's scroll and the render always agree on how tall it is
	l := m.layout()

	lines := []string{m.headerView(w), m.tabsView(w)}
	if l.hasStr {
		lines = append(lines, l.strip)
	}
	if m.hasInput() {
		lines = append(lines, m.inputView(w))
	}
	if l.newsW > 0 {
		left := strings.Split(m.listView(l.mainW, l.listH), "\n")
		right := m.newsView(l.newsW, l.listH)
		for i := 0; i < l.listH; i++ {
			ln := ""
			if i < len(left) {
				ln = left[i]
			}
			if n := l.mainW - lipgloss.Width(ln); n > 0 {
				ln += m.st.bg.Render(strings.Repeat(" ", n))
			}
			sep := m.st.sep.Render(" │")
			r := ""
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, ln+sep+r)
		}
	} else {
		lines = append(lines, m.listView(w, l.listH))
	}
	if m.hasButtons() {
		lines = append(lines, m.buttonsView())
	}
	if l.logH > 0 {
		lines = append(lines, m.logView(w, l.logH)...)
	}
	lines = append(lines, m.statusView(w), m.hintView(w))
	// each element paints its own text, but the empty tail of a line would
	// still show the terminal's own background, so pad every line out to
	// the full width in the theme's colour
	return m.canvas(strings.Join(lines, "\n"), w)
}

// canvas pads each line of a rendered frame out to w cells with the active
// theme's background. text runs carry their own colours, but the gap after
// them would otherwise be transparent and reveal a dark terminal behind a
// light theme, so the fill is what actually makes the window white.
func (m *model) canvas(frame string, w int) string {
	lines := strings.Split(frame, "\n")
	for i, ln := range lines {
		if n := w - lipgloss.Width(ln); n > 0 {
			lines[i] = ln + m.st.bg.Render(strings.Repeat(" ", n))
		}
	}
	return strings.Join(lines, "\n")
}

// headerView renders the title with the github link next to it, the running
// distribution centred in the middle, and the tool/version on the right.
func (m *model) headerView(w int) string {
	left := m.st.title.Render("get my aur installer") +
		m.st.bg.Render("  ") +
		m.st.dim.Render("github.com/keaneskiii")
	rightText := "install with: " + m.installer
	if version != "" {
		rightText = version + " | install with: " + m.installer
	}
	right := m.st.dim.Render(rightText)

	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	// the os name sits centred between the two ends, and steps aside
	// entirely when the window is too narrow to hold all three
	if name := lower(strings.TrimSpace(m.osName)); name != "" {
		mid := m.st.ok.Render(name)
		mw := lipgloss.Width(mid)
		start := (w - mw) / 2
		if start >= lw+2 && start+mw <= w-rw-2 {
			return left + m.st.bg.Render(strings.Repeat(" ", start-lw)) +
				mid + m.st.bg.Render(strings.Repeat(" ", w-rw-(start+mw))) + right
		}
	}
	gap := w - lw - rw
	if gap >= 1 {
		// the gap sits mid-line, so the canvas tail-fill cannot cover it
		// and it has to be painted here
		return left + m.st.bg.Render(strings.Repeat(" ", gap)) + right
	}
	// not even the two ends fit: keep what leads and add on the rest only
	// while it still fits, so the header never runs past the window
	parts := []struct {
		text string
		st   lipgloss.Style
	}{{"get my aur installer", m.st.title},
		{"github.com/keaneskiii", m.st.dim},
		{rightText, m.st.dim}}
	out, avail := "", w
	for i, p := range parts {
		sep := " "
		if i == 0 {
			sep = ""
		}
		n := lipgloss.Width(sep + p.text)
		if avail-n < 0 {
			break
		}
		out += m.st.bg.Render(sep) + p.st.Render(p.text)
		avail -= n
	}
	if out == "" {
		return m.st.title.Render(trunc("get my aur installer", w))
	}
	return out
}

// tabsView renders the tab row. with room to spare every tab carries its
// function key; a narrower window drops the key hints first and then the
// tabs that do not fit, but never the tab the app is sitting on, so the row
// always stays on one line.
func (m *model) tabsView(w int) string {
	tabs := []struct {
		key   string
		kind  tabKind
		label string
	}{
		{"f1", tabSearch, "search"},
		{"f2", tabUninstall, "uninstall"},
		{"f3", tabUpdates, "updates"},
		{"f4", tabSettings, "settings"},
	}
	active := 0
	for i, t := range tabs {
		if t.kind == m.tab {
			active = i
		}
	}

	label := func(i int, withKeys bool) string {
		s := tabs[i].label
		if withKeys {
			s = "[" + tabs[i].key + "] " + s
		}
		return s
	}
	// a gap between two tabs is painted with the window colour, because a
	// plain space there would show the terminal's own background through
	gap := m.st.bg.Render(" ")

	paint := func(from int, withKeys bool) string {
		parts := make([]string, 0, len(tabs)*2)
		for i := from; i < len(tabs); i++ {
			if i > from {
				parts = append(parts, gap)
			}
			if m.tab == tabs[i].kind {
				parts = append(parts, m.st.tabActive.Render(label(i, withKeys)))
			} else {
				parts = append(parts, m.st.tabIdle.Render(label(i, withKeys)))
			}
		}
		return strings.Join(parts, "")
	}

	for _, withKeys := range []bool{true, false} {
		for _, from := range []int{0, active} {
			if line := paint(from, withKeys); lipgloss.Width(line) <= w {
				return line
			}
		}
	}
	// narrower still: take tabs from the current one onward until the next
	// one would push the row past the window
	line := ""
	for i := active; i < len(tabs); i++ {
		s := tabs[i].label
		if m.tab == tabs[i].kind {
			s = m.st.tabActive.Render(s)
		} else {
			s = m.st.tabIdle.Render(s)
		}
		next := s
		if line != "" {
			next = gap + s
		}
		if line != "" && lipgloss.Width(line)+lipgloss.Width(next) > w {
			break
		}
		line = next
	}
	return line
}

// inputView renders the active tab's text field. it is laid out inside a
// fixed-width background block rather than left to size itself, because
// the textinput pads its own tail with unstyled spaces, which would show
// the terminal's background through a light theme.
func (m *model) inputView(w int) string {
	in := m.searchInput.View()
	if m.tab == tabUninstall {
		in = m.filterInput.View()
	}
	return m.st.bg.Width(w).Render(in)
}

// infoPaneH is how many lines the detail panel is drawn into for a body of
// h lines: the panel takes as much as its text holds and the rows keep
// just enough to stay browsable.
func infoPaneH(rows, h int) int {
	infoH := rows
	listMin := 4
	if h < 12 {
		listMin = 3
	}
	if infoH > h-listMin {
		infoH = h - listMin
	}
	if infoH < 1 {
		infoH = 1
	}
	return infoH
}

// listView renders the body of the active tab. on the search tab the
// selected package's detail view sits under the rows instead of replacing
// them, so the selection and what it says stay visible at once.
func (m *model) listView(w, h int) string {
	// a body this short has no room for two panes: the rows take it
	if m.tab == tabSearch && m.info != nil && h > 1 {
		rows := m.infoRows(w)
		infoH := infoPaneH(len(rows), h)
		listH := h - infoH
		if listH < 1 {
			listH, infoH = 1, h-1
		}
		out := m.rowsView(w, listH)
		out = append(out, scrollLines(rows, m.info.scroll, infoH)...)
		return strings.Join(out, "\n")
	}
	return strings.Join(m.rowsView(w, h), "\n")
}

// windowLines returns the h rows around sel, padded with blank lines.
func windowLines(rows []string, sel, h int) []string {
	start := windowStart(len(rows), sel, h)
	out := make([]string, 0, h)
	for i := start; i < len(rows) && len(out) < h; i++ {
		out = append(out, rows[i])
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// scrollLines returns the h rows from a scroll offset, padded with blank
// lines. unlike windowLines the offset is where the window starts, not a
// row to keep visible, which is what reading a long text needs: the first
// pgdown always moves it.
func scrollLines(rows []string, offset, h int) []string {
	start := offset
	if max := len(rows) - h; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, h)
	for i := start; i < len(rows) && len(out) < h; i++ {
		out = append(out, rows[i])
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// rowsView renders a scrolling window of the active tab's rows.
func (m *model) rowsView(w, h int) []string {
	loading, empty, rows := m.listContent()
	if len(rows) == 0 {
		out := make([]string, 0, h)
		if loading {
			out = append(out, m.st.dim.Render(m.spin.View()+" "+trunc(empty, w-3)))
		} else {
			out = append(out, m.st.dim.Render(trunc(empty, w)))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return out
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
			out = append(out, m.st.selected.Render(pad(content, w)))
		} else {
			out = append(out, m.st.log.Render(pad(content, w)))
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// wrapText hard-wraps s into lines of at most w cells, breaking on spaces
// where it can. it is used for the news panel and the info pane.
func wrapText(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
			// a single word longer than the line is hard-split
			for runewidth.StringWidth(line) > w {
				head := runewidth.Truncate(line, w, "")
				out = append(out, head)
				line = line[len(head):]
			}
		}
		out = append(out, line)
	}
	return out
}

// infoRows renders the detail pane for the selected package as one outlined
// block, the way a package panel reads: the package's name sits on the
// frame itself, then its description, version, license, maintainer, the
// dependencies it needs one per line (the ones already on disk marked) and
// the page it comes from, with the stand-ins the search turned up for it
// underneath. every line of the pane is built here; the caller decides how
// much of it fits.
func (m *model) infoRows(w int) []string {
	in := m.info
	if in == nil {
		return nil
	}

	d := in.detail
	head := d.name
	if head == "" {
		head = in.target.name
	}
	if d.repo != "" {
		head += "  [" + lower(d.repo) + "]"
	}

	inner := w - 4 // the frame takes two cells, the text keeps one of air
	if inner < 8 {
		inner = 8
	}
	const keyW = 13

	var body []string
	switch {
	case in.loading:
		body = append(body,
			m.st.dim.Render(trunc(m.spin.View()+" reading package info ...", inner)))
	case in.err != nil:
		body = append(body,
			m.st.err.Render(trunc("no info: "+lower(in.err.Error()), inner)))
	default:
		// each fact is one label in a shared column, with long values
		// wrapping under the label rather than running off the panel
		put := func(k string, lines []string) {
			if len(lines) == 0 {
				return
			}
			indent := strings.Repeat(" ", keyW)
			for i, ln := range lines {
				if i == 0 {
					body = append(body, m.st.dim.Render(pad(k, keyW))+
						m.st.log.Render(trunc(ln, inner-keyW)))
				} else {
					body = append(body, m.st.dim.Render(indent)+
						m.st.log.Render(trunc(ln, inner-keyW)))
				}
			}
		}
		wrap := func(s string) []string { return wrapText(lower(s), inner-keyW) }
		one := func(k, v string) {
			v = strings.TrimSpace(v)
			if v == "" {
				return
			}
			put(k, wrap(v))
		}
		one("description", d.desc)
		one("version", d.version)
		one("license", strings.Join(d.licenses, ", "))
		one("maintainer", d.maintainer)
		// a dependency gets a line of its own, and the ones already on
		// the disk are marked so the chain reads at a glance
		for i, dep := range d.depends {
			k := "dependencies"
			if i > 0 {
				k = ""
			}
			mark := "[ ]"
			if d.depInstalled[depName(dep)] {
				mark = "[✓]"
			}
			put(k, []string{mark + " " + depName(dep)})
		}
		one("url", d.url)
		one("size", d.downloadSize)
		one("on disk", d.installedSize)

		// the stand-ins the search already turned up, so the question
		// after "what is it" is answered on the same panel
		if len(in.alts) > 0 {
			body = append(body, "")
			body = append(body, m.st.dim.Render(trunc("alternatives", inner)))
			for i, a := range in.alts {
				if i >= altShown {
					body = append(body, m.st.dim.Render(
						trunc(fmt.Sprintf("+%d more", len(in.alts)-i), inner)))
					break
				}
				body = append(body, m.st.log.Render(trunc("  "+searchRow(a), inner)))
			}
		}
	}
	if len(body) == 0 {
		body = append(body, m.st.dim.Render(trunc("no info for this package", inner)))
	}
	// the name leads the panel, on the line just inside its frame, the way
	// a package panel reads: name first, then what it says about itself
	body = append([]string{m.st.title.Render(trunc(head, inner))}, body...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(themePalette.sep)).
		Width(w-2). // the borders are what makes this w cells in total
		Padding(0, 1)
	return strings.Split(box.Render(strings.Join(body, "\n")), "\n")
}

// infoView renders the detail pane scrolled to its own offset.
func (m *model) infoView(w, h int) string {
	rows := m.infoRows(w)
	scroll := 0
	if m.info != nil {
		scroll = m.info.scroll
	}
	return strings.Join(scrollLines(rows, scroll, h), "\n")
}

// newsView renders the arch news side panel: as many of the newest stories
// as the panel can hold, each dated. it returns exactly h painted lines.
func (m *model) newsView(w, h int) []string {
	out := make([]string, 0, h)
	put := func(s string) bool {
		if len(out) >= h {
			return false
		}
		out = append(out, s)
		return true
	}
	put(m.st.title.Render(trunc("arch news", w)))
	put(m.st.sep.Render(trunc(strings.Repeat("-", w), w)))

	if len(m.news) == 0 {
		switch {
		case m.newsErr != nil:
			put(m.st.err.Render(trunc("cannot fetch news", w)))
			for _, ln := range wrapText(lower(m.newsErr.Error()), w) {
				if !put(m.st.dim.Render(trunc(ln, w))) {
					break
				}
			}
		default:
			put(m.st.dim.Render(trunc("no news loaded", w)))
		}
	}
	for _, it := range m.news {
		// a story needs room for its gap, its date and its first line
		// before it is worth starting, so the panel never opens one it
		// cannot show
		if len(out)+3 > h {
			break
		}
		put("")
		when := ""
		if !it.pub.IsZero() {
			when = it.pub.In(time.Local).Format("2006-01-02")
		}
		put(m.st.ok.Render(trunc(when, w)))
		for _, ln := range wrapText(lower(it.title), w) {
			if !put(m.st.log.Render(trunc(ln, w))) {
				break
			}
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

// newsStrip is the news when there is no room for the column: the newest
// story on one line, dated, so a package's own view can take the whole
// width without taking the news with it.
func (m *model) newsStrip(w int) (string, bool) {
	if len(m.news) == 0 {
		return "", false
	}
	it := m.news[0]
	when := "news"
	if !it.pub.IsZero() {
		when = "news " + it.pub.In(time.Local).Format("2006-01-02")
	}
	head := pad(trunc(when, 16), 16)
	return m.st.dim.Render(head) +
		m.st.log.Render(trunc(lower(it.title), w-16)), true
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
	case tabSettings:
		// each row is "label   option  [ current ]  option", so every
		// choice is visible; left/right/enter step the selection
		for i, label := range m.settingsRows() {
			parts := make([]string, 0, 3)
			for _, opt := range m.settingOptions(i) {
				if !m.settingAvailable(i, opt) {
					parts = append(parts, opt+" (missing)")
					continue
				}
				if opt == m.settingValue(i) {
					parts = append(parts, "[ "+opt+" ]")
					continue
				}
				parts = append(parts, opt)
			}
			rows = append(rows, pad(label, 16)+"  "+strings.Join(parts, "   "))
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
	case tabUpdates:
		return clamp(m.updCur, len(m.updates))
	default:
		return clamp(m.setCur, len(m.settingsRows()))
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
	parts := make([]string, 0, len(btns)*2)
	for i, b := range btns {
		if i > 0 {
			// same as the tabs: an unpainted space would show through
			parts = append(parts, m.st.bg.Render(" "))
		}
		if m.focus == focusBtn && i == m.btnIdx {
			parts = append(parts, m.st.btnFocused.Render(b))
		} else {
			parts = append(parts, m.st.btn.Render(b))
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
	out = append(out, m.st.dim.Render(trunc(head, w)))

	src := m.logLines
	if len(src) > inner {
		src = src[len(src)-inner:]
	}
	for _, ln := range src {
		t := lower(ansi.Strip(ln))
		t = strings.TrimSpace(t)
		out = append(out, m.st.log.Render(trunc(t, w)))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

func (m *model) statusView(w int) string {
	if m.busy {
		return m.st.status.Render(m.spin.View() + " " + trunc(lower(m.actionTitle), w-4))
	}
	if m.searching {
		return m.st.status.Render(m.spin.View() + " " + trunc(lower(m.status), w-4))
	}
	st := m.st.status
	if m.statusErr {
		st = m.st.err
	} else if strings.TrimSpace(m.status) == "" || m.status == "ready" {
		st = m.st.dim
	}
	return st.Render(trunc(lower(m.status), w))
}

func (m *model) hintView(w int) string {
	h := "tab focus | enter run | up/down move | pgup/pgdown detail | " +
		"f1-f4 tabs | h installer (" + m.installer + ") | r recheck | q quit"
	return m.st.hint.Render(trunc(h, w))
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
