package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Column kinds that left/right can stop on.
const (
	stopTicket   = "ticket"
	stopRelation = "relation"
	stopPR       = "pr"
	stopSymphony = "symphony"
)

// rowStop is one left/right column on a row, and what enter does there.
type rowStop struct {
	kind string
	url  string // what enter opens
	what string // shown in the flash when there is nothing to open
}

// selRow records what the cursor can act on for each selectable row. id is a
// stable identity so the cursor stays on the same row across a refresh, even
// when a status change reorders the groups.
type selRow struct {
	id        string
	label     string // PROJ-17552, or "sandbox #5" for a PR with no ticket
	ticketKey string // empty on rows that are only a pull request
	source    string // the tracker that owns ticketKey; empty on rows that are only a pull request
	summary   string
	status    string
	ticketURL string // empty on rows that are only a pull request
	prURLs    []string
	// stops are the left/right columns, in screen order.
	stops []rowStop
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
	jira *jiraClient
	// trackers holds every tracker with credentials configured, each paired
	// with the query that selects its tickets. jira is also kept on its own
	// because its write actions (status change, Symphony scheduling) have no
	// equivalent on other trackers.
	trackers    []trackerSource
	gh          *githubClient
	ghCfg       error // deferred configuration error, surfaced in the UI
	jql         string
	linearQuery string
	ghQuery     string
	// prScope names the repository the PR search is narrowed to, empty when it
	// spans every repository. It is shown in the header so a narrowed list is
	// never mistaken for the whole picture, hyperlinked to prScopeURL.
	prScope       string
	symphonyURL   string
	prScopeURL    string
	explicitQuery bool

	period          time.Duration
	autoRefresh     bool
	hyperlinks      bool
	snapshotMode    bool
	demo            bool
	includeArchived bool

	tickets []Ticket
	prs     []PullRequest
	groups  []group
	orphans []PullRequest
	sel     []selRow

	cursor int
	col    int // index into the current row's stops
	offset int
	width  int
	height int

	pendingTickets bool
	pendingGH      bool
	lastFetch      time.Time
	now            time.Time
	// trackerCfgErr is a configuration problem found at startup, such as a
	// JIRA_* group that is only partially set. It never changes once newApp
	// returns, so it is handed to every fetch to fold into that fetch's
	// result rather than being overwritten and lost after the first one.
	trackerCfgErr error
	// trackerErr and trackerWarn report the ticket fetch's outcome across
	// every configured tracker: err when nothing came back at all (including
	// "nothing is configured"), warn when some tickets loaded but part of the
	// fetch — another tracker, or JIRA's child-count enrichment — failed.
	trackerErr  error
	trackerWarn error
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
	// symphonyURL is where the local Symphony answered, empty when it did not.
	symphonyURL string
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

type scheduledMsg struct {
	key         string
	summary     string // what changed, for the flash
	unscheduled bool
	err         error
}

type tickMsg time.Time

func newApp(jql, linearQuery, ghQuery string, period time.Duration, hyperlinks, includeArchived bool) *app {
	a := &app{
		jql:             jql,
		linearQuery:     linearQuery,
		ghQuery:         ghQuery,
		period:          period,
		autoRefresh:     period > 0,
		hyperlinks:      hyperlinks,
		includeArchived: includeArchived,
		width:           100,
		height:          30,
		now:             time.Now(),
	}

	hc := &http.Client{Timeout: 25 * time.Second}

	if client, err := newJIRAClient(hc); err != nil {
		// JIRA is optional once another tracker can carry the dashboard, but a
		// JIRA_* group that is set and still wrong is a mistake worth
		// surfacing rather than a silent opt-out.
		if jiraConfigured() {
			a.trackerErr = fmt.Errorf("JIRA: %w", err)
		}
	} else {
		a.jira = client
		a.trackers = append(a.trackers, trackerSource{client, jql})
	}

	if client, err := newLinearClient(hc); err == nil {
		a.trackers = append(a.trackers, trackerSource{client, linearQuery})
	}

	if len(a.trackers) == 0 && a.trackerErr == nil {
		a.trackerErr = fmt.Errorf("no ticket tracker configured — set JIRA_URL, JIRA_USERNAME and JIRA_API_TOKEN, or LINEAR_API_KEY")
	}
	a.trackerCfgErr = a.trackerErr

	if client, err := newGitHubClient(&http.Client{Timeout: 25 * time.Second}); err != nil {
		a.ghCfg, a.ghErr = err, err
	} else {
		a.gh = client
	}
	return a
}

// jiraConfigured reports whether any JIRA environment variable is set, to
// tell "not using JIRA" apart from "meant to, but got it wrong."
func jiraConfigured() bool {
	return os.Getenv("JIRA_URL") != "" || os.Getenv("JIRA_USERNAME") != "" || os.Getenv("JIRA_API_TOKEN") != ""
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.refresh(), a.tick())
}

func (a *app) tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (a *app) loading() bool { return a.pendingTickets || a.pendingGH }

// refresh starts every fetch. Sources are independent, so a failure in one
// still lets the others update.
func (a *app) refresh() tea.Cmd {
	if a.demo {
		return nil // sample data, nothing to fetch
	}
	var cmds []tea.Cmd

	if !a.pendingTickets {
		a.pendingTickets = true
		trackers, jira, cfgErr := a.trackers, a.jira, a.trackerCfgErr
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			return fetchTickets(ctx, trackers, jira, cfgErr)
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

// fetchTickets runs every tracker concurrently and merges the results into
// one message. cfgErr is a configuration problem discovered at startup, such
// as a JIRA_* group that is only partially set; it never resolves on its own,
// so it is folded into the result on every call rather than just the first:
// it is what to report outright when trackers is empty, and a warning
// otherwise so it keeps showing even once another tracker succeeds. A
// tracker that fails once others succeed is likewise a warning, not a fatal
// error, so the dashboard still shows what it could reach.
func fetchTickets(ctx context.Context, trackers []trackerSource, jira *jiraClient, cfgErr error) ticketsMsg {
	if len(trackers) == 0 {
		return ticketsMsg{err: cfgErr}
	}

	results := make([][]Ticket, len(trackers))
	errs := make([]error, len(trackers))
	var wg sync.WaitGroup
	wg.Add(len(trackers))
	for i, src := range trackers {
		go func(i int, src trackerSource) {
			defer wg.Done()
			results[i], errs[i] = src.tracker.Tickets(ctx, src.query)
		}(i, src)
	}
	wg.Wait()

	var tickets []Ticket
	var problems []string
	if cfgErr != nil {
		problems = append(problems, cfgErr.Error())
	}
	for i, src := range trackers {
		if errs[i] != nil {
			problems = append(problems, src.tracker.Name()+": "+errs[i].Error())
			continue
		}
		tickets = append(tickets, results[i]...)
	}
	if len(tickets) == 0 && len(problems) > 0 {
		return ticketsMsg{err: fmt.Errorf("%s", strings.Join(problems, "; "))}
	}
	var warn error
	if len(problems) > 0 {
		warn = fmt.Errorf("%s", strings.Join(problems, "; "))
	}

	// Child counts are JIRA-only enrichment: JIRA's parent/child hierarchy has
	// no equivalent query wired up for other trackers, so only its own
	// tickets are asked about. A failure here is itself only enrichment: show
	// the tickets and say the counts are missing.
	if jira != nil {
		var jiraTickets []Ticket
		for _, t := range tickets {
			if t.Source == jira.Name() {
				jiraTickets = append(jiraTickets, t)
			}
		}
		if counts, cErr := jira.ChildCounts(ctx, childCandidates(jiraTickets)); cErr == nil {
			applyChildCounts(tickets, counts, jira.Name())
		} else {
			cErr = fmt.Errorf("child counts unavailable: %w", cErr)
			if warn == nil {
				warn = cErr
			} else {
				warn = fmt.Errorf("%s; %s", warn, cErr)
			}
		}
	}

	// Symphony is rediscovered and queried on every refresh.
	cwd, _ := os.Getwd()
	info := symphonyLookup(ctx, cwd)
	applySymphony(tickets, info, jira.Name())
	return ticketsMsg{tickets: tickets, warn: warn, symphonyURL: info.endpoint}
}

// symphonyLookup rediscovers the local Symphony instance and asks what it is
// working on. Discovery is repeated every refresh rather than cached, since
// Symphony is started and stopped independently of this tool and its port can
// change with the working tree.
//
// Symphony not running is the ordinary case, so every failure here is silent: a
// banner would cry wolf on most refreshes.
func symphonyLookup(ctx context.Context, dir string) symphonyInfo {
	var info symphonyInfo

	// The configuration is a local file, so the conditions are known even when
	// the server is not running: a ticket can be queued with Symphony stopped.
	if cfg, err := readSymphonyConfig(dir); err == nil {
		info.cfg, info.haveCfg = cfg, true
		if cfg.Server.Port > 0 {
			host := cfg.Server.Host
			if host == "" {
				host = symphonyDefaultHost
			}
			info.endpoint = fmt.Sprintf("http://%s:%d", host, cfg.Server.Port)
		}
	}
	if info.endpoint == "" {
		return info
	}

	ctx, cancel := context.WithTimeout(ctx, symphonyTimeout)
	defer cancel()

	if live, err := SymphonyState(ctx, &http.Client{Timeout: symphonyTimeout}, info.endpoint); err == nil {
		info.live = live
	}
	return info
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

	case scheduledMsg:
		if msg.err != nil {
			a.setFlash("scheduling " + msg.key + " failed: " + msg.err.Error())
			return a, nil
		}
		verb := "scheduled for Symphony"
		if msg.unscheduled {
			verb = "unscheduled"
		}
		a.setFlash(msg.key + " " + verb + " — " + msg.summary)
		return a, a.refresh()

	case ticketsMsg:
		a.pendingTickets = false
		a.trackerErr = msg.err
		a.trackerWarn = msg.warn
		a.symphonyURL = msg.symphonyURL
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
	case "left", "h":
		a.moveColumn(-1)
	case "right", "l":
		a.moveColumn(1)
	case "g", "home":
		a.cursor = 0
	case "G", "end":
		a.cursor = max(0, len(a.sel)-1)
	case "pgup":
		a.move(-10)
	case "pgdown":
		a.move(10)
	case "enter":
		a.openColumn()
	case "o":
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
	case "S":
		return a, a.scheduleForSymphony()
	case "r":
		return a, a.refresh()
	case "a":
		a.autoRefresh = !a.autoRefresh && a.period > 0
	case "?":
		a.showHelp = !a.showHelp
	}
	return a, nil
}

// openColumn does the default thing for the column left/right is on.
func (a *app) openColumn() {
	stop, ok := a.activeStop()
	if !ok {
		return
	}
	if stop.url == "" {
		// Only reachable for a Symphony stop when the endpoint is not known.
		a.setFlash("nothing to open for " + stop.what)
		return
	}
	_ = openURL(stop.url)
}

// childrenSearchURL turns a ticket's browse URL into a JIRA search for the
// issues that call it their parent.
func childrenSearchURL(browseURL, key string) string {
	base, _, found := strings.Cut(browseURL, "/browse/")
	if !found || base == "" {
		return ""
	}
	return base + "/issues/?jql=" + url.QueryEscape("parent = "+key)
}

// scheduleForSymphony puts the selected ticket into the state Symphony picks up,
// reading the conditions from WORKFLOW.md rather than assuming them.
func (a *app) scheduleForSymphony() tea.Cmd {
	row, ok := a.current()
	if !ok {
		return nil
	}
	if row.ticketKey == "" {
		a.setFlash("that row is a pull request with no ticket — nothing to schedule")
		return nil
	}
	if a.jira == nil {
		a.setFlash("JIRA is not configured; cannot schedule")
		return nil
	}
	if row.source != a.jira.Name() {
		a.setFlash("Symphony scheduling is only supported for JIRA tickets")
		return nil
	}

	// Resolved by the row's own source, not just a.jira.Name(): a key is only
	// guaranteed unique within its own tracker, so another tracker's ticket
	// sharing this key must never be picked up here instead of the one the
	// cursor is actually on.
	ticket, found := a.ticketByKey(row.ticketKey, row.source)
	if !found {
		return nil
	}

	cwd, _ := os.Getwd()
	cfg, err := readSymphonyConfig(cwd)
	if err != nil {
		a.setFlash("no Symphony configuration here: " + err.Error())
		return nil
	}

	plan := planToggle(cfg, ticket)
	if plan.refusal != "" {
		a.setFlash(plan.refusal)
		return nil
	}
	if plan.nothingToDo() {
		a.setFlash("nothing to change for " + ticket.Key)
		return nil
	}

	jira := a.jira
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := applySchedule(ctx, jira, plan); err != nil {
			return scheduledMsg{key: plan.key, err: err}
		}
		return scheduledMsg{
			key: plan.key, summary: plan.summary(), unscheduled: plan.unschedule,
		}
	}
}

// ticketByKey finds a loaded ticket from the given tracker, which carries the
// labels and status the plan is built from. A key is only guaranteed unique
// within its own tracker, so source must match alongside key: two trackers
// can issue the same key (e.g. a JIRA project and a Linear team sharing a
// short code), and a key-only match could silently resolve to the wrong
// tracker's ticket.
func (a *app) ticketByKey(key, source string) (Ticket, bool) {
	for _, g := range a.groups {
		for _, t := range g.Tickets {
			if t.Source == source && strings.EqualFold(t.Key, key) {
				return t, true
			}
		}
	}
	return Ticket{}, false
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
	if row.source != a.jira.Name() {
		a.setFlash("changing status is only supported for JIRA tickets")
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
	if next := clamp(a.cursor+delta, 0, len(a.sel)-1); next != a.cursor {
		a.cursor = next
		a.col = 0 // start each row on the ticket
	}
}

// moveColumn walks left or right along the selected row's columns.
func (a *app) moveColumn(delta int) {
	row, ok := a.current()
	if !ok || len(row.stops) == 0 {
		return
	}
	a.col = clamp(a.col+delta, 0, len(row.stops)-1)
}

// activeStop is the column enter acts on, if the row has any.
func (a *app) activeStop() (rowStop, bool) {
	row, ok := a.current()
	if !ok || len(row.stops) == 0 {
		return rowStop{}, false
	}
	return row.stops[clamp(a.col, 0, len(row.stops)-1)], true
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
				// Scoped by source, not just key: two trackers can issue the
				// same key (a JIRA/Linear collision), and an id collision here
				// would let a refresh's cursor-restore land the cursor on the
				// wrong tracker's ticket.
				id:        "ticket:" + t.Source + ":" + t.Key,
				label:     t.Key,
				ticketKey: t.Key,
				source:    t.Source,
				summary:   t.Summary,
				status:    t.Status,
				ticketURL: t.URL,
			}
			for _, pr := range t.PRs {
				s.prURLs = append(s.prURLs, pr.URL)
			}

			// Stops appear only where the column does, and in the order the
			// columns are drawn, so the walk matches what is on screen. Symphony
			// and the relation cell now sit left of the key, so they come first.
			if t.Symphony != "" {
				s.stops = append(s.stops, rowStop{
					kind: stopSymphony, url: a.symphonyURL,
					what: "the Symphony dashboard",
				})
			}
			// A subtask's ↳ is decoration: there is nothing to open, because the
			// ticket does not carry its parent's key. Only a child count is a stop.
			if t.ChildCount > 0 {
				s.stops = append(s.stops, rowStop{
					kind: stopRelation,
					url:  childrenSearchURL(t.URL, t.Key),
					what: fmt.Sprintf("%d sub-tickets of %s", t.ChildCount, t.Key),
				})
			}
			s.stops = append(s.stops, rowStop{kind: stopTicket, url: t.URL, what: t.Key})
			for _, pr := range t.PRs {
				s.stops = append(s.stops, rowStop{
					kind: stopPR, url: pr.URL,
					what: fmt.Sprintf("%s #%d", pr.Repo, pr.Number),
				})
			}
			a.sel = append(a.sel, s)
		}
	}
	for _, pr := range a.orphans {
		label := fmt.Sprintf("%s #%d", pr.Repo, pr.Number)
		a.sel = append(a.sel, selRow{
			id:      "pr:" + pr.URL,
			label:   label,
			summary: pr.Title,
			prURLs:  []string{pr.URL},
			stops:   []rowStop{{kind: stopPR, url: pr.URL, what: label}},
		})
	}

	a.cursor = clamp(a.cursor, 0, max(0, len(a.sel)-1))
	if row, ok := a.current(); ok {
		a.col = clamp(a.col, 0, max(0, len(row.stops)-1))
	}
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

func (a *app) notificationView(lay layout) string {
	text, style := "", normalStyle
	switch {
	case a.flash != "":
		text, style = "✓ "+a.flash, okStyle
	case a.trackerErr != nil:
		text, style = "! "+a.trackerErr.Error(), errStyle
	case a.ghErr != nil:
		text, style = "! github: "+a.ghErr.Error(), errStyle
	case a.trackerWarn != nil:
		text, style = "~ "+a.trackerWarn.Error(), warnStyle
	default:
		return ""
	}
	return style.Bold(true).Reverse(true).Render(trunc(" "+text+" ", lay.width))
}

func (a *app) View() string {
	lay := a.layout()

	// header + notification + body + blank + footer, with one row of
	// headroom kept unused at the bottom. Bubble Tea's renderer mishandles
	// the case where a frame fills the terminal exactly and a later frame
	// is shorter: it can fail to erase the trailing rows of the taller
	// frame, leaving stale content stuck at the bottom of the screen. Never
	// touching the last row sidesteps that edge case entirely.
	bodyHeight := max(a.height-5, 3)

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

	out := []string{a.headerView(lay), a.notificationView(lay)}
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
