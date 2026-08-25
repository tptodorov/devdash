package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	minKeyW = 8
	maxKeyW = 15 // long project keys such as LONGPROJ-308 must still align
	// maxRelW caps the relation cell so one ticket with a thousand children
	// cannot widen the column for every other row.
	maxRelW = 4
	// The summary is the column that flexes: it takes whatever the fixed columns
	// leave, down to minSummaryW, below which it says nothing worth reading.
	minSummaryW = 10
	// A PR title is a hint at what the pull request does, not the whole sentence,
	// so it is capped however long the titles are, and below minTitleW there is
	// nothing worth drawing at all.
	maxTitleW = 30
	minTitleW = 12
	// The reference column fits the widest reference on screen, between what it
	// takes to name a repository at all and what one pathological name may cost
	// every other row. minRefW is the floor on a terminal narrower than either.
	maxRefW = 30
	minRefW = 6

	// prBranch joins a ticket's second and later pull requests to the first. The
	// arrow that used to lead the first one is gone: being on the ticket's own row
	// already said "this ticket's pull request", so it was three columns of chrome
	// on every row of the dashboard.
	prBranch = "↳"

	// selBar is the cursor marker in column 0.
	selBar = "▌"
)

// prBadgeCols is the fixed tail of a pull request cell: two spaces, the check
// slot, two spaces, the review slot. Holding it constant is what lets the badges
// be read as columns down the page rather than as a sentence per row.
const prBadgeCols = 2 + 1 + 2 + 2

// widths are the column sizes derived from the loaded data.
// Every field is the widest value of its kind on screen, so no column is ever
// wider than the data it holds.
type widths struct {
	relW     int // the widest relation cell: +N children, or the subtask glyph
	keyW     int
	summaryW int
	refW     int // the widest "repo #number"
	titleW   int // the longest pull request title
	archived bool
	symW     int // 1 when Symphony has any ticket in hand, otherwise the column is gone
}

// layout holds the column widths for one render. The relation, key and Symphony
// columns are sized from the data, so each collapses away entirely when nothing
// on screen needs it.
type layout struct {
	width   int
	relW    int // 0 when no ticket has children and none is a subtask
	keyW    int
	symW    int // 0 when Symphony is not running anything
	summary int
	ref     int // the reference cell, padded so the badges beside it line up
	pr      int // the whole pull request cell: glyph, reference, badges
	title   int // the PR title filling the rest of the row, 0 when there is no room
	// archived is whether the row has slack for the archived note. It is not part
	// of pr: counting it there would let one opt-in flag squeeze the reference on
	// every row, and it cannot be squeezed itself.
	archived bool
}

// archivedBlock is what the archived note costs when there is room to draw it.
func (l layout) archivedBlock() int {
	if !l.archived {
		return 0
	}
	return runewidth.StringWidth(archivedNote)
}

// titleBlock is the PR title plus its separating space, or nothing when the row
// is too narrow to carry one.
func (l layout) titleBlock() int {
	if l.title == 0 {
		return 0
	}
	return l.title + 2
}

// symBlock is the Symphony column plus its separating space, or nothing when
// Symphony has no sessions.
func (l layout) symBlock() int {
	if l.symW == 0 {
		return 0
	}
	return l.symW + 1
}

// relBlock is the relation column plus its separating space, or nothing when the
// column is collapsed.
func (l layout) relBlock() int {
	if l.relW == 0 {
		return 0
	}
	return l.relW + 1
}

// prefix is everything left of the summary: the cursor bar, the attention
// marker, Symphony, the relation cell and the key.
func (l layout) prefix() int {
	return 1 + 1 + 1 + l.symBlock() + l.relBlock() + l.keyW + 2
}

// prColumn is the screen column the PR cell starts at.
func (l layout) prColumn() int {
	return l.prefix() + l.summary + 2
}

// layout sizes the columns for the current terminal and the loaded data.
func (a *app) layout() layout {
	return computeLayout(a.width, a.measure())
}

// relationText is the relation cell: how many children a ticket has, or that it
// is somebody's child. The two are mutually exclusive — JIRA forbids sub-tasks
// of sub-tasks — so one column carries both, and neither needs a legend to read.
func relationText(t Ticket) string {
	switch {
	case t.ChildCount > 0:
		return iconChild + strconv.Itoa(t.ChildCount)
	case t.IsSubtask:
		return iconSubtask
	}
	return ""
}

// prLabel is how a pull request is named: the repository and the number.
func prLabel(pr PullRequest) string {
	return fmt.Sprintf("%s #%d", pr.Repo, pr.Number)
}

// measure derives the data-dependent column widths from the loaded tickets.
func (a *app) measure() widths {
	w := widths{keyW: minKeyW}
	measurePR := func(pr PullRequest) {
		if n := runewidth.StringWidth(prLabel(pr)); n > w.refW {
			w.refW = n
		}
		if n := runewidth.StringWidth(prTitle(pr)); n > w.titleW {
			w.titleW = n
		}
		if pr.Archived {
			w.archived = true
		}
	}

	for _, g := range a.groups {
		for _, t := range g.Tickets {
			if n := runewidth.StringWidth(relationText(t)); n > w.relW {
				w.relW = n
			}
			if n := runewidth.StringWidth(t.Key); n > w.keyW {
				w.keyW = n
			}
			if n := runewidth.StringWidth(t.Summary); n > w.summaryW {
				w.summaryW = n
			}
			for _, pr := range t.PRs {
				measurePR(pr)
			}
			if t.Symphony != "" {
				w.symW = 1
			}
		}
	}
	// Orphan rows put the PR title in the summary column, so they size it too.
	for _, pr := range a.orphans {
		measurePR(pr)
		if n := runewidth.StringWidth(pr.Title); n > w.summaryW {
			w.summaryW = n
		}
	}
	return w
}

// prCellWidth is the columns a pull request cell needs once the reference is
// padded to refW: the state glyph, the reference, and the two badge slots. The
// archived note is deliberately not counted — see layout.archived.
func prCellWidth(refW int) int {
	return 2 + refW + prBadgeCols
}

// archivedNote is the word appended to a pull request whose repository is
// archived. It is prose rather than a badge: it sits after the two slots so it
// cannot shift them out of line, and it is dropped on a terminal with no room,
// since it only ever appears under an opt-in flag.
const archivedNote = " archived"

func computeLayout(width int, w widths) layout {
	if width < 60 {
		width = 60
	}

	l := layout{
		width: width,
		relW:  clamp(w.relW, 0, maxRelW),
		keyW:  clamp(w.keyW, minKeyW, maxKeyW),
		symW:  clamp(w.symW, 0, 1),
	}

	// The summary and the pull request cell share whatever the prefix leaves, a
	// column short of the terminal edge:
	// prefix + summary + gap + pr + gap + title.
	room := width - 1 - l.prefix() - 2

	// The reference fits the widest reference on screen, so references line up
	// down the page. On a terminal too narrow for that it is what gives way,
	// since the summary is already down to its own floor by then.
	fits := max(room-minSummaryW-prCellWidth(0), minRefW)
	l.ref = min(clamp(w.refW, minRefW, maxRefW), fits)
	l.pr = prCellWidth(l.ref)

	// The summary flexes: it takes whatever the reference leaves, up to the
	// longest summary there is, so it fills the row without ever padding past
	// its own content.
	l.summary = max(min(w.summaryW, room-l.pr), minSummaryW)

	// The title has whatever is left after that, capped, since it describes a
	// pull request the row already names. Below minTitleW there is nothing worth
	// drawing and the column collapses.
	if rest := room - l.pr - l.summary - 2; rest >= minTitleW {
		l.title = min(min(w.titleW, maxTitleW), rest)
	}

	// The archived note is drawn only out of whatever slack is left, so it can
	// never be the thing that pushes a row past the terminal's edge.
	l.archived = w.archived &&
		room-l.pr-l.summary-l.titleBlock() >= runewidth.StringWidth(archivedNote)
	return l
}

// reviewSeg is the review slot. Awaiting review is what a fresh pull request is
// supposed to be, so it draws nothing: only a deviation from that earns ink.
func reviewSeg(pr PullRequest) seg {
	switch {
	case pr.Review == "CHANGES_REQUESTED":
		return seg{text: pad(iconChanges, 2), st: badStyle}
	case pr.Review == "APPROVED" || pr.Approvals > 0:
		// GitHub reports no decision at all when the base branch requires no
		// review, so an approving review has to be counted separately or a
		// genuinely approved PR would show nothing.
		badge := iconApproved
		if pr.Approvals > 1 {
			badge += strconv.Itoa(pr.Approvals)
		}
		return seg{text: pad(badge, 2), st: okStyle}
	}
	return seg{text: "  ", st: faintStyle}
}

// prSegs renders a pull request reference: the state glyph, the reference padded
// to refW, then the check and review slots in fixed positions.
func prSegs(pr PullRequest, refW int, archived bool, stop int) []seg {
	stateIcon, stateStyle := prStateIcon(pr)

	check, checkStyle := prCheckIcon(pr)
	if check == "" {
		check = " "
	}

	segs := []seg{
		{text: stateIcon, st: stateStyle},
		{text: " ", st: normalStyle},
		{text: pad(trunc(prLabel(pr), refW), refW), st: prStateStyle(pr), link: pr.URL},
		{text: "  ", st: normalStyle},
		{text: check, st: checkStyle},
		{text: "  ", st: normalStyle},
		reviewSeg(pr),
	}
	// Archived goes after the badge slots so it cannot push them out of line.
	if pr.Archived && archived {
		segs = append(segs, seg{text: archivedNote, st: warnStyle})
	}
	for i := range segs {
		segs[i].stop = stop
	}
	return segs
}

// prCellSegs is a pull request as a row occupies it: the reference in a column
// of its own so the titles beside it line up, then the title, which is what
// fills the row out to the edge of the terminal.
func prCellSegs(pr PullRequest, lay layout, stop int) []seg {
	segs := prSegs(pr, lay.ref, lay.archived, stop)
	if lay.title == 0 {
		return segs
	}
	// Whatever the archived note spent past the cell's column comes out of the
	// title, so an archived pull request cannot push the row off the screen.
	title := lay.title - max(segsWidth(segs)-lay.pr, 0)
	if title < 1 {
		return segs
	}
	if n := lay.pr - segsWidth(segs); n > 0 {
		segs = append(segs, seg{text: strings.Repeat(" ", n), stop: stop})
	}
	return append(segs, seg{text: "  " + trunc(prTitle(pr), lay.title), st: mutedStyle, stop: stop})
}

// prTitle is the title with the leading ticket key removed. The convention puts
// the key at the front of every PR title, and the row it is drawn on already
// carries it, so repeating it would spend the widest column on nothing.
func prTitle(pr PullRequest) string {
	if pr.Ticket == "" || !strings.HasPrefix(strings.ToUpper(pr.Title), strings.ToUpper(pr.Ticket)) {
		return pr.Title
	}
	rest := strings.TrimLeft(pr.Title[len(pr.Ticket):], " :-–—")
	if rest == "" {
		return pr.Title // the key was the whole title; keep it rather than show nothing
	}
	return rest
}

// buildBody renders the dashboard rows and reports, for each selectable row,
// the line it landed on so the viewport can keep the cursor visible.
//
// Sections are not separated by a blank line: the coloured bar and the rule of a
// group header are enough to break the page, and with six statuses on screen the
// blanks were costing a sixth of the terminal to say nothing.
func (a *app) buildBody(lay layout) (lines []string, rowLine []int) {
	row := 0

	for _, g := range a.groups {
		lines = append(lines, groupHeader(g.Status, g.Category, len(g.Tickets), lay.width))
		for _, t := range g.Tickets {
			selected := row == a.cursor
			stops := a.stopsFor(row)
			active := noStop
			if selected {
				active = a.col
			}
			rowLine = append(rowLine, len(lines))
			lines = append(lines, a.ticketLine(t, lay, selected, stops, active))
			for i, extra := range t.PRs[min(1, len(t.PRs)):] {
				lines = append(lines,
					a.prContinuationLine(extra, lay, selected, stopTag(prStopIndex(stops, i+1)), active))
			}
			row++
		}
	}

	if len(a.orphans) > 0 {
		lines = append(lines, groupHeader("PRS WITHOUT AN ACTIVE TICKET", "", len(a.orphans), lay.width))
		for _, pr := range a.orphans {
			selected := row == a.cursor
			active := noStop
			if selected {
				active = a.col
			}
			rowLine = append(rowLine, len(lines))
			lines = append(lines, a.orphanLine(pr, lay, selected, active))
			row++
		}
	}

	return lines, rowLine
}

// stopsFor returns the left/right columns of a selectable row.
func (a *app) stopsFor(row int) []rowStop {
	if row < 0 || row >= len(a.sel) {
		return nil
	}
	return a.sel[row].stops
}

// prStopIndex finds the column index of the nth pull request on a row.
func prStopIndex(stops []rowStop, nth int) int {
	seen := 0
	for i, st := range stops {
		if st.kind != stopPR {
			continue
		}
		if seen == nth {
			return i
		}
		seen++
	}
	return noStop
}

// groupHeader draws a section rule. A count of zero or less is omitted, so
// sections that are not a tally (the help panels) read cleanly.
func groupHeader(status, category string, count int, width int) string {
	color := statusColor(status, category)
	bar := lipgloss.NewStyle().Foreground(color).Render("▌")
	name := lipgloss.NewStyle().Bold(true).Foreground(color).Render(strings.ToUpper(status))

	countTxt, countW := "", 0
	if count > 0 {
		s := fmt.Sprintf(" %d", count)
		countTxt, countW = countStyle.Render(s), runewidth.StringWidth(s)
	}

	rule := ""
	if n := width - 4 - runewidth.StringWidth(status) - countW; n > 0 {
		rule = faintStyle.Render(" " + strings.Repeat("─", n))
	}
	return bar + " " + name + rule + countTxt
}

// gutterSeg is column 0, carrying the cursor bar when the row is selected.
func gutterSeg(selected bool) seg {
	if selected {
		return seg{text: selBar, st: lipgloss.NewStyle().Foreground(accent)}
	}
	return seg{text: " "}
}

// attentionSeg is column 1: the one thing this row wants from you, or a space.
func attentionSeg(a attention) seg {
	marker, style := a.marker()
	return seg{text: marker, st: style}
}

func (a *app) ticketLine(t Ticket, lay layout, selected bool, stops []rowStop, active int) string {
	// stopFor finds which left/right column a given kind occupies on this row.
	stopFor := func(kind string, nth int) int {
		seen := 0
		for i, st := range stops {
			if st.kind != kind {
				continue
			}
			if seen == nth {
				return i
			}
			seen++
		}
		return noStop
	}

	segs := []seg{
		gutterSeg(selected),
		attentionSeg(attentionFor(t)),
		{text: " ", st: normalStyle, stop: noStop},
	}

	// Symphony sits here, beside the attention marker, rather than at the far
	// right of the row: it is ticket state, and at the end of a cell of variable
	// length it was the least noticeable thing on screen.
	if lay.symW > 0 {
		marker, style := symphonyMarker(t.Symphony)
		segs = append(segs,
			seg{text: marker, st: style, stop: stopTag(stopFor(stopSymphony, 0))},
			seg{text: " ", st: normalStyle, stop: noStop})
	}

	if lay.relW > 0 {
		segs = append(segs,
			seg{
				text: padLeft(relationText(t), lay.relW),
				st:   mutedStyle,
				stop: stopTag(stopFor(stopRelation, 0)),
			},
			seg{text: " ", st: normalStyle, stop: noStop})
	}

	ticketStop := stopTag(stopFor(stopTicket, 0))
	segs = append(segs,
		seg{text: pad(trunc(t.Key, lay.keyW), lay.keyW), st: keyStyle, link: t.URL, stop: ticketStop},
		seg{text: "  ", st: normalStyle, stop: ticketStop},
		seg{text: pad(trunc(t.Summary, lay.summary), lay.summary), st: normalStyle, stop: ticketStop},
		seg{text: "  ", st: normalStyle, stop: noStop},
	)

	// A ticket with no pull request draws nothing here. Blank space already says
	// "no PR", and spelling it out made the widest element on more than half the
	// rows an announcement that there was nothing to see.
	if len(t.PRs) > 0 {
		segs = append(segs, prCellSegs(t.PRs[0], lay, stopTag(stopFor(stopPR, 0)))...)
	}

	if selected {
		segs = highlight(segs, lay.width, active)
	}
	return segsRender(segs, a.hyperlinks)
}

// prContinuationLine renders a ticket's second and later pull requests, aligned
// under the first and highlighted with the ticket they belong to. It keeps its
// own attention marker, so a pull request wanting something under one that does
// not still announces itself in the same column as every other row.
func (a *app) prContinuationLine(pr PullRequest, lay layout, selected bool, stop, active int) string {
	segs := []seg{
		gutterSeg(selected),
		attentionSeg(attentionForPR(pr)),
		{text: strings.Repeat(" ", max(lay.prColumn()-4, 0)), stop: noStop},
		{text: prBranch + " ", st: faintStyle, stop: noStop},
	}
	segs = append(segs, prCellSegs(pr, lay, stop)...)
	if selected {
		segs = highlight(segs, lay.width, active)
	}
	return segsRender(segs, a.hyperlinks)
}

// orphanLine renders a pull request that matches no active ticket. It uses the
// shared grid — the title in the summary column, the reference in the pull
// request column — so this section lines up with every row above it instead of
// starting back at the left margin.
func (a *app) orphanLine(pr PullRequest, lay layout, selected bool, active int) string {
	stop := stopTag(0)
	segs := []seg{
		gutterSeg(selected),
		attentionSeg(attentionForPR(pr)),
		{text: strings.Repeat(" ", max(lay.prefix()-2, 0)), stop: noStop},
		{text: pad(trunc(pr.Title, lay.summary), lay.summary), st: mutedStyle, stop: stop},
		{text: "  ", st: normalStyle, stop: noStop},
	}
	segs = append(segs, prSegs(pr, lay.ref, lay.archived, stop)...)

	if selected {
		segs = highlight(segs, lay.width, active)
	}
	return segsRender(segs, a.hyperlinks)
}

func (a *app) headerView(lay layout) string {
	const title = "◈ devdash"

	tickets := 0
	for _, g := range a.groups {
		tickets += len(g.Tickets)
	}
	stats := fmt.Sprintf("  %d tickets · %d PRs", tickets, a.prCount())

	needs := ""
	if n := needsYou(a.groups, a.orphans); n > 0 {
		needs = fmt.Sprintf(" · %d need you", n)
	}

	status := a.statusPlain()
	fixed := runewidth.StringWidth(title) + runewidth.StringWidth(stats) +
		runewidth.StringWidth(needs) + runewidth.StringWidth(status) + 2

	// Name the repository the PRs come from. The short name is what a person
	// calls the repository they are standing in, and the reference is a link
	// anyway, so the full URL is only worth the room on a wide terminal.
	scope, scopeURL := "", ""
	if a.prScope != "" {
		for _, candidate := range []string{shortRepo(a.prScope), a.prScope} {
			if candidate == "" {
				continue
			}
			if fixed+runewidth.StringWidth("  "+candidate)+1 <= lay.width {
				scope, scopeURL = candidate, a.prScopeURL
			}
		}
	}

	scopeText := ""
	if scope != "" {
		scopeText = "  " + scope
	}

	gap := lay.width - fixed - runewidth.StringWidth(scopeText)
	if gap < 1 {
		gap = 1
	}

	rendered := titleStyle.Render(title)
	if scope != "" {
		link := keyStyle.Render(scope)
		if a.hyperlinks && scopeURL != "" {
			link = hyperlink(scopeURL, link)
		}
		rendered += mutedStyle.Render("  ") + link
	}
	rendered += mutedStyle.Render(stats)
	if needs != "" {
		rendered += errStyle.Render(needs)
	}
	return rendered + strings.Repeat(" ", gap) + a.statusText()
}

// footerHints are the key reminders in display order. keep ranks them by how
// essential they are, so a narrow terminal sheds the least useful first rather
// than overflowing the line.
var footerHints = []struct {
	text string
	keep int
}{
	{"↑↓ move", 2},
	{"←→ column", 3},
	{"⏎ open", 3},
	{"p PR", 5},
	{"c copy", 4},
	{"s status", 4},
	{"S symphony", 5},
	{"r refresh", 5},
	{"? help", 1},
	{"q quit", 1},
}

func (a *app) footerView() string {
	if a.picker != nil {
		return faintStyle.Render(trunc("  "+a.pickerKeys(), a.width))
	}

	line := ""
	for threshold := 1; threshold <= 5; threshold++ {
		var parts []string
		for _, h := range footerHints {
			if h.keep <= threshold {
				parts = append(parts, h.text)
			}
		}
		candidate := "  " + strings.Join(parts, "   ")
		if runewidth.StringWidth(candidate) > a.width {
			break
		}
		line = candidate
	}
	if line == "" {
		line = trunc("  ? help   q quit", a.width)
	}
	return faintStyle.Render(line)
}

func (a *app) helpView(lay layout) []string {
	rows := [][2]string{
		{"↑/k, ↓/j", "move between rows"},
		{"←/h, →/l", "move between the columns of the selected row"},
		{"g / G", "jump to first / last row"},
		{"enter", "open whatever the selected column points at"},
		{"o", "open the ticket, whichever column is selected"},
		{"p", "open the selected row's pull request"},
		{"c", "copy a shareable snippet of the row to the clipboard"},
		{"s", "change the selected ticket's status"},
		{"S", "schedule the ticket for Symphony, or take it back unless an agent is running"},
		{"r", "refresh now"},
		{"a", "toggle automatic refresh"},
		{"?", "toggle this help"},
		{"q, esc, ctrl+c", "quit"},
	}
	out := []string{groupHeader("KEYS", "", 0, lay.width)}
	for _, r := range rows {
		out = append(out, "  "+keyStyle.Render(pad(r[0], 16))+mutedStyle.Render(r[1]))
	}

	out = append(out, "", groupHeader("COLUMNS", "", 0, lay.width))
	out = append(out,
		"  "+normalStyle.Render(pad("attention", 16))+
			mutedStyle.Render("leftmost: the one thing this row wants from you, if anything"),
		"  "+normalStyle.Render(pad("symphony", 16))+
			mutedStyle.Render("what an agent is doing with the ticket, when one has it"),
		"  "+normalStyle.Render(pad("relation", 16))+
			mutedStyle.Render("+N children, or ↳ when the ticket is somebody's sub-task"),
		"  "+normalStyle.Render(pad("summary", 16))+
			mutedStyle.Render("takes the room the other columns leave, up to the longest there is"),
		"  "+normalStyle.Render(pad("PR title", 16))+
			mutedStyle.Render("up to 30 columns; the ticket key it repeats is dropped"))

	out = append(out, "", groupHeader("ATTENTION", "", 0, lay.width))
	for _, s := range []struct {
		a    attention
		what string
	}{
		{attnMerge, "approved and green — merge it"},
		{attnChanges, "changes requested — respond to the review"},
		{attnAlert, "the build failed, or Symphony is blocked waiting on you"},
	} {
		marker, style := s.a.marker()
		out = append(out, "  "+style.Render(pad(marker, 16))+mutedStyle.Render(s.what))
	}

	// One glyph for every Symphony state: the note says Symphony has the ticket,
	// and the colour says what it is doing with it.
	out = append(out, "", groupHeader("SYMPHONY", "", 0, lay.width))
	for _, s := range []struct{ state, what string }{
		{SymphonyScheduled, "grey — scheduled, waiting for Symphony to pick it up"},
		{SymphonyRunning, "magenta — Symphony is working on this ticket"},
		{SymphonyRetrying, "yellow — waiting for the next retry window"},
		{SymphonyBlocked, "red — paused waiting for operator input or approval"},
	} {
		marker, style := symphonyMarker(s.state)
		out = append(out, "  "+style.Render(pad(marker, 16))+mutedStyle.Render(s.what))
	}

	// The PR state is carried by colour, so the samples are rendered in it, and
	// a draft is the same glyph hollowed out rather than a colour of its own.
	out = append(out, "", groupHeader("PR STATE", "", 0, lay.width))
	for _, s := range []struct {
		pr   PullRequest
		what string
	}{
		{PullRequest{State: "OPEN"}, "open"},
		{PullRequest{State: "MERGED"}, "violet — merged"},
		{PullRequest{State: "CLOSED"}, "red — closed without merging"},
		{PullRequest{State: "OPEN", Draft: true}, "hollow — a draft, not offered for review yet"},
	} {
		icon, style := prStateIcon(s.pr)
		out = append(out, "  "+style.Render(pad(icon+" repo #123", 16))+mutedStyle.Render(s.what))
	}

	out = append(out, "", groupHeader("CHECKS AND REVIEW", "", 0, lay.width))
	legend := [][2]string{
		{iconCheckPass, "checks passing — drawn faintly, because passing is expected"},
		{iconCheckFail, "checks failing"},
		{iconCheckRun, "checks still running"},
		{iconApproved, "approved (" + iconApproved + "N for N approvals)"},
		{iconChanges, "changes requested"},
		{"", "the review slot is blank while a pull request awaits review"},
	}
	for _, r := range legend {
		out = append(out, "  "+normalStyle.Render(pad(r[0], 16))+mutedStyle.Render(r[1]))
	}

	out = append(out, "", groupHeader("LEGEND", "", 0, lay.width))
	for _, r := range [][2]string{
		{prBranch, "a further pull request for the ticket above"},
		{"archived", "the PR's repository is archived"},
		{selBar, "the cursor; this colour means only that"},
	} {
		out = append(out, "  "+normalStyle.Render(pad(r[0], 16))+mutedStyle.Render(r[1]))
	}

	out = append(out, "", faintStyle.Render("  Ticket keys and PR references are OSC 8 hyperlinks; ⌘-click or"),
		faintStyle.Render("  ctrl-click them in a terminal that supports links."))
	return out
}
