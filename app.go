package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// selRow records what the cursor can act on for each selectable row. id is a
// stable identity so the cursor stays on the same row across a refresh, even
// when a status change reorders the groups.
type selRow struct {
	id        string
	label     string // PROJ-17552, or "sandbox #5" for a PR with no ticket
	ticketKey string // empty on rows that are only a pull request
	summary   string
	status    string
	ticketURL string // empty on rows that are only a pull request
	prURLs    []string
}

// openTarget is what enter opens: the ticket, or the pull request on rows that
// have no ticket.
func (s selRow) openTarget() string {
	if s.ticketURL != "" {
		return s.ticketURL
	}
	return s.prTarget()
}

func (s selRow) prTarget() string {
	if len(s.prURLs) == 0 {
		return ""
	}
	return s.prURLs[0]
}

// snippet is the shareable text for this row: what it is, then every link
// someone would need to review it.
func (s selRow) snippet() string {
	head := s.label
	if s.summary != "" {
		head += " — " + s.summary
	}

	lines := []string{head}
	if s.ticketURL != "" {
		lines = append(lines, s.ticketURL)
	}
	lines = append(lines, s.prURLs...)
	return strings.Join(lines, "\n")
}

type app struct {
	jira    *jiraClient
	jiraCfg error // deferred configuration error, surfaced in the UI
	gh      *githubClient
	ghCfg   error // deferred configuration error, surfaced in the UI
	jql     string
	ghQuery string
	// prScope names the repository the PR search is narrowed to, empty when it
	// spans every repository. It is shown in the header so a narrowed list is
	// never mistaken for the whole picture, hyperlinked to prScopeURL.
	prScope       string
	prScopeURL    string
	explicitQuery bool

	period          time.Duration
	autoRefresh     bool
	hyperlinks      bool
	snapshotMode    bool
	includeArchived bool

	tickets []Ticket
	prs     []PullRequest
	groups  []group
	orphans []PullRequest
	sel     []selRow

	cursor int
	offset int
	width  int
	height int

	pendingJIRA bool
	pendingGH   bool
	lastFetch   time.Time
	now         time.Time
	jiraErr     error
	jiraWarn    error
	ghErr       error
	showHelp    bool

	picker     *picker
	flash      string
	flashUntil time.Time
}

type ticketsMsg struct {
	tickets []Ticket
	err     error
	// warn reports a non-fatal problem: the tickets are usable but some
	// enrichment failed, so it is shown alongside the data rather than instead.
	warn error
}

type prsMsg struct {
	prs []PullRequest
	err error
}

type transitionsMsg struct {
	key   string
	items []Transition
	err   error
}

type transitionAppliedMsg struct {
	key string
	to  string
	err error
}

type tickMsg time.Time

func newApp(jql, ghQuery string, period time.Duration, hyperlinks, includeArchived bool) *app {
	a := &app{
		jql:             jql,
		ghQuery:         ghQuery,
		period:          period,
		autoRefresh:     period > 0,
		hyperlinks:      hyperlinks,
		includeArchived: includeArchived,
		width:           100,
		height:          30,
		now:             time.Now(),
	}
	if client, err := newJIRAClient(&http.Client{Timeout: 25 * time.Second}); err != nil {
		a.jiraCfg, a.jiraErr = err, err
	} else {
		a.jira = client
	}

	if client, err := newGitHubClient(&http.Client{Timeout: 25 * time.Second}); err != nil {
		a.ghCfg, a.ghErr = err, err
	} else {
		a.gh = client
	}
	return a
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.refresh(), a.tick())
}

func (a *app) tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (a *app) loading() bool { return a.pendingJIRA || a.pendingGH }

// refresh starts both fetches. Sources are independent, so a failure in one
// still lets the other update.
func (a *app) refresh() tea.Cmd {
	var cmds []tea.Cmd

	if !a.pendingJIRA {
		a.pendingJIRA = true
		jira, jql, cfgErr := a.jira, a.jql, a.jiraCfg
		cmds = append(cmds, func() tea.Msg {
			if jira == nil {
				return ticketsMsg{err: cfgErr}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()

			tickets, err := jira.Tickets(ctx, jql)
			if err != nil {
				return ticketsMsg{err: err}
			}

			// Child counts are enrichment: if the query fails, still show the
			// tickets and say the counts are missing.
			counts, warn := jira.ChildCounts(ctx, childCandidates(tickets))
			if warn == nil {
				applyChildCounts(tickets, counts)
			} else {
				warn = fmt.Errorf("child counts unavailable: %w", warn)
			}
			// Symphony is rediscovered and queried on every refresh.
			applySymphony(tickets, symphonyLookup(ctx))
			return ticketsMsg{tickets: tickets, warn: warn}
		})
	}

	if !a.pendingGH {
		a.pendingGH = true
		gh, q, archived, cfgErr := a.gh, a.ghQuery, a.includeArchived, a.ghCfg
		explicit := a.explicitQuery
		cmds = append(cmds, func() tea.Msg {
			if gh == nil {
				return prsMsg{err: cfgErr}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()

			// An explicit query is the user's own and is run exactly as written.
			if explicit {
				prs, err := gh.PullRequests(ctx, q, archived)
				return prsMsg{prs: prs, err: err}
			}
			prs, err := gh.OpenAndMergedPRs(ctx, q,
				mergedPRQuery(q, time.Now(), mergedWindow), archived)
			return prsMsg{prs: prs, err: err}
		})
	}

	return tea.Batch(cmds...)
}

// symphonyLookup rediscovers the local Symphony instance and asks what it is
// working on. Discovery is repeated every refresh rather than cached, since
// Symphony is started and stopped independently of this tool and its port can
// change with the working tree.
//
// Symphony not running is the ordinary case, so every failure here is silent: a
// banner would cry wolf on most refreshes.
func symphonyLookup(ctx context.Context) map[string]string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	endpoint, err := symphonyEndpoint(cwd)
	if err != nil {
		return nil // no WORKFLOW.md, or no server block in it
	}

	ctx, cancel := context.WithTimeout(ctx, symphonyTimeout)
	defer cancel()

	state, err := SymphonyState(ctx, &http.Client{Timeout: symphonyTimeout}, endpoint)
	if err != nil {
		return nil // not listening, or not answering
	}
	return state
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tickMsg:
		a.now = time.Time(msg)
		if a.flash != "" && a.now.After(a.flashUntil) {
			a.flash = ""
		}
		var cmds []tea.Cmd
		// Hold off refreshing while the picker is open; rows must not move
		// under a panel the user is acting through.
		if a.autoRefresh && a.period > 0 && !a.loading() && a.picker == nil &&
			(a.lastFetch.IsZero() || a.now.Sub(a.lastFetch) >= a.period) {
			cmds = append(cmds, a.refresh())
		}
		return a, tea.Batch(append(cmds, a.tick())...)

	case transitionsMsg:
		if a.picker == nil || a.picker.key != msg.key {
			return a, nil // a stale response for a picker that has since closed
		}
		a.picker.stage = stageTransitions
		a.picker.items = msg.items
		switch {
		case msg.err != nil:
			a.picker.err = msg.err
		case len(msg.items) == 0:
			a.picker.err = fmt.Errorf("JIRA offers no transitions for %s", msg.key)
		}
		return a, nil

	case transitionAppliedMsg:
		if a.picker == nil || a.picker.key != msg.key {
			return a, nil
		}
		if msg.err != nil {
			a.picker.stage = stageTransitions
			a.picker.err = msg.err
			return a, nil
		}
		a.picker = nil
		a.setFlash(fmt.Sprintf("%s moved to %s", msg.key, msg.to))
		return a, a.refresh()

	case ticketsMsg:
		a.pendingJIRA = false
		a.jiraErr = msg.err
		a.jiraWarn = msg.warn
		if msg.err == nil {
			a.tickets = msg.tickets
		}
		a.settle()
		return a, nil

	case prsMsg:
		a.pendingGH = false
		a.ghErr = msg.err
		if msg.err == nil {
			a.prs = msg.prs
		}
		a.settle()
		return a, nil

	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a, nil
}

func (a *app) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.picker != nil {
		return a.handlePickerKey(msg)
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return a, tea.Quit
	case "up", "k":
		a.move(-1)
	case "down", "j":
		a.move(1)
	case "g", "home":
		a.cursor = 0
	case "G", "end":
		a.cursor = max(0, len(a.sel)-1)
	case "pgup":
		a.move(-10)
	case "pgdown":
		a.move(10)
	case "enter", "o":
		if s, ok := a.current(); ok {
			_ = openURL(s.openTarget())
		}
	case "p":
		if s, ok := a.current(); ok {
			_ = openURL(s.prTarget())
		}
	case "c":
		a.copyRow()
	case "s":
		return a, a.openPicker()
	case "r":
		return a, a.refresh()
	case "a":
		a.autoRefresh = !a.autoRefresh && a.period > 0
	case "?":
		a.showHelp = !a.showHelp
	}
	return a, nil
}

// copyRow puts the selected row's shareable snippet on the clipboard.
func (a *app) copyRow() {
	row, ok := a.current()
	if !ok {
		return
	}
	if err := copyToClipboard(row.snippet()); err != nil {
		a.setFlash("copy failed: " + err.Error())
		return
	}

	what := row.label
	switch n := len(row.prURLs); {
	case row.ticketURL != "" && n == 1:
		what += " + PR link"
	case row.ticketURL != "" && n > 1:
		what += fmt.Sprintf(" + %d PR links", n)
	}
	a.setFlash("copied " + what)
}

// openPicker starts a status change for the selected ticket.
func (a *app) openPicker() tea.Cmd {
	row, ok := a.current()
	if !ok {
		return nil
	}
	if row.ticketKey == "" {
		a.setFlash("that row is a pull request with no ticket — nothing to move")
		return nil
	}
	if a.jira == nil {
		a.setFlash("JIRA is not configured; cannot change status")
		return nil
	}

	a.picker = &picker{
		key:       row.ticketKey,
		summary:   row.summary,
		current:   row.status,
		ticketURL: row.ticketURL,
		stage:     stageLoading,
	}

	jira, key := a.jira, row.ticketKey
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		items, err := jira.Transitions(ctx, key)
		return transitionsMsg{key: key, items: items, err: err}
	}
}

func (a *app) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := a.picker
	switch key := msg.String(); key {
	case "ctrl+c":
		return a, tea.Quit
	case "esc", "q":
		if p.back() {
			a.picker = nil
		}
	case "up", "k":
		p.moveCursor(-1)
	case "down", "j":
		p.moveCursor(1)
	case "home":
		p.moveCursor(-len(p.items) - 1)
	case "end":
		p.moveCursor(len(p.items) + 1)
	case "o":
		_ = openURL(p.ticketURL)
	case "enter":
		return a, a.pickerConfirm()
	default:
		// Digits jump straight to a choice and act on it.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			n := int(key[0] - '1')
			if n < p.choiceCount() {
				if p.stage == stageField {
					p.fieldCursor = n
				} else {
					p.cursor = n
				}
				return a, a.pickerConfirm()
			}
		}
	}
	return a, nil
}

// pickerConfirm acts on the highlighted choice, moving to the next stage or
// applying the transition.
func (a *app) pickerConfirm() tea.Cmd {
	p := a.picker
	switch p.stage {
	case stageTransitions:
		if p.err != nil {
			p.err = nil
			return nil
		}
		ready, err := p.selectTransition()
		if err != nil {
			p.err = err
			return nil
		}
		if ready {
			return a.applyTransition()
		}
	case stageField:
		if p.selectFieldValue() {
			return a.applyTransition()
		}
	}
	return nil
}

func (a *app) applyTransition() tea.Cmd {
	p := a.picker
	p.stage = stageApplying
	p.err = nil

	jira := a.jira
	key, id, to := p.key, p.chosen.ID, p.chosen.To
	fields := p.values

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		err := jira.ApplyTransition(ctx, key, id, fields)
		return transitionAppliedMsg{key: key, to: to, err: err}
	}
}

func (a *app) setFlash(msg string) {
	a.flash = msg
	a.flashUntil = time.Now().Add(5 * time.Second)
}

func (a *app) move(delta int) {
	if len(a.sel) == 0 {
		return
	}
	a.cursor = clamp(a.cursor+delta, 0, len(a.sel)-1)
}

func (a *app) current() (selRow, bool) {
	if a.cursor < 0 || a.cursor >= len(a.sel) {
		return selRow{}, false
	}
	return a.sel[a.cursor], true
}

// settle recomputes the correlated view whenever either source changes.
func (a *app) settle() {
	if !a.loading() {
		a.lastFetch = time.Now()
	}

	tickets := make([]Ticket, len(a.tickets))
	copy(tickets, a.tickets)
	for i := range tickets {
		tickets[i].PRs = nil
	}
	a.groups, a.orphans = build(tickets, a.prs)

	// Remember which row the cursor was on: a status change reorders the
	// groups, so an index alone would silently move the cursor to a different
	// ticket.
	wasOn := ""
	if cur, ok := a.current(); ok {
		wasOn = cur.id
	}

	a.sel = a.sel[:0]
	for _, g := range a.groups {
		for _, t := range g.Tickets {
			s := selRow{
				id:        "ticket:" + t.Key,
				label:     t.Key,
				ticketKey: t.Key,
				summary:   t.Summary,
				status:    t.Status,
				ticketURL: t.URL,
			}
			for _, pr := range t.PRs {
				s.prURLs = append(s.prURLs, pr.URL)
			}
			a.sel = append(a.sel, s)
		}
	}
	for _, pr := range a.orphans {
		a.sel = append(a.sel, selRow{
			id:      "pr:" + pr.URL,
			label:   fmt.Sprintf("%s #%d", pr.Repo, pr.Number),
			summary: pr.Title,
			prURLs:  []string{pr.URL},
		})
	}

	a.cursor = clamp(a.cursor, 0, max(0, len(a.sel)-1))
	if wasOn != "" {
		for i, s := range a.sel {
			if s.id == wasOn {
				a.cursor = i
				break
			}
		}
	}
}

// prCount counts the pull requests actually on screen, not everything fetched:
// merged PRs matching no active ticket are dropped, and claiming them in the
// header would make the count disagree with the rows.
func (a *app) prCount() int {
	n := len(a.orphans)
	for _, g := range a.groups {
		for _, t := range g.Tickets {
			n += len(t.PRs)
		}
	}
	return n
}

func (a *app) statusPlain() string {
	if a.loading() {
		return "⟳ refreshing…"
	}
	stamp := "—"
	if !a.lastFetch.IsZero() {
		stamp = a.lastFetch.Format("15:04:05")
	}
	switch {
	case a.snapshotMode:
		return stamp
	case a.autoRefresh && a.period > 0:
		return fmt.Sprintf("⟳ %s · %s", compactDuration(a.period), stamp)
	default:
		return fmt.Sprintf("⏸ paused · %s", stamp)
	}
}

func (a *app) statusText() string {
	if a.loading() {
		return warnStyle.Render(a.statusPlain())
	}
	return faintStyle.Render(a.statusPlain())
}

func (a *app) View() string {
	lay := a.layout()

	var banners []string
	if a.flash != "" {
		banners = append(banners, okStyle.Render("  ✓ ")+normalStyle.Render(trunc(a.flash, lay.width-6)))
	}
	if a.jiraErr != nil {
		banners = append(banners, errStyle.Render("  ! jira: ")+mutedStyle.Render(trunc(a.jiraErr.Error(), lay.width-12)))
	}
	if a.jiraWarn != nil {
		banners = append(banners, warnStyle.Render("  ~ jira: ")+mutedStyle.Render(trunc(a.jiraWarn.Error(), lay.width-12)))
	}
	if a.ghErr != nil {
		banners = append(banners, errStyle.Render("  ! github: ")+mutedStyle.Render(trunc(a.ghErr.Error(), lay.width-14)))
	}

	// header + blank + banners + body + blank + footer
	bodyHeight := a.height - 4 - len(banners)
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	var body []string
	var rowLine []int
	switch {
	case a.picker != nil:
		body = a.pickerView(lay, bodyHeight)
	case a.showHelp:
		body = a.helpView(lay)
	default:
		body, rowLine = a.buildBody(lay)
		if len(body) == 0 && !a.loading() {
			body = []string{mutedStyle.Render("  Nothing open. Enjoy the quiet.")}
		}
	}

	if a.picker == nil && !a.showHelp && a.cursor >= 0 && a.cursor < len(rowLine) {
		want := rowLine[a.cursor]
		if want < a.offset {
			a.offset = want
		}
		if want >= a.offset+bodyHeight {
			a.offset = want - bodyHeight + 1
		}
	}
	a.offset = clamp(a.offset, 0, max(0, len(body)-bodyHeight))

	end := min(a.offset+bodyHeight, len(body))
	visible := body[min(a.offset, len(body)):end]

	out := []string{a.headerView(lay), ""}
	out = append(out, banners...)
	out = append(out, visible...)
	if len(body) > bodyHeight {
		out = append(out, faintStyle.Render(fmt.Sprintf("  … %d more", len(body)-end+a.offset)))
	} else {
		out = append(out, "")
	}
	out = append(out, a.footerView())
	return strings.Join(out, "\n")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// compactDuration formats a refresh interval the way a person would say it:
// 10s, 1m, 1m30s rather than Go's 10s, 1m0s, 1m30s.
func compactDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
