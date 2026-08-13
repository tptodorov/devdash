package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	gutterCols = 2
	maxChildW  = 4
	minKeyW    = 8
	maxKeyW    = 15 // long project keys such as LONGPROJ-308 must still align
)

// widths are the column sizes derived from the loaded data.
type widths struct {
	typeW  int
	childW int
	keyW   int
	symW   int // 1 when Symphony has any ticket in hand, otherwise the column is gone
}

// layout holds the column widths for one render. The type, child and key columns
// are sized from the data, so they stay as narrow as the actual issue types,
// child counts and project keys allow.
type layout struct {
	width   int
	typeW   int
	childW  int // 0 when no ticket has children, collapsing the column away
	keyW    int
	symW    int // 0 when Symphony is not running anything
	summary int
	pr      int
}

// symBlock is the Symphony column plus its separating space, or nothing when
// Symphony has no sessions.
func (l layout) symBlock() int {
	if l.symW == 0 {
		return 0
	}
	return l.symW + 1
}

// childBlock is the child column plus its separating space, or nothing when the
// column is collapsed.
func (l layout) childBlock() int {
	if l.childW == 0 {
		return 0
	}
	return l.childW + 1
}

// prefix is everything left of the summary: gutter, type, children, key.
func (l layout) prefix() int {
	return gutterCols + l.typeW + 1 + l.childBlock() + l.keyW + 2
}

// prColumn is the screen column the PR cell starts at.
func (l layout) prColumn() int {
	return l.prefix() + l.summary + 2 + 1 + 2
}

// layout sizes the columns for the current terminal and the loaded data.
func (a *app) layout() layout {
	return computeLayout(a.width, a.measure())
}

// measure derives the data-dependent column widths from the loaded tickets.
func (a *app) measure() widths {
	w := widths{typeW: 1, keyW: minKeyW}
	for _, g := range a.groups {
		for _, t := range g.Tickets {
			if n := runewidth.StringWidth(typeCode(t.Type)); n > w.typeW {
				w.typeW = n
			}
			if n := runewidth.StringWidth(t.Key); n > w.keyW {
				w.keyW = n
			}
			if t.ChildCount > 0 {
				if n := len(strconv.Itoa(t.ChildCount)); n > w.childW {
					w.childW = n
				}
			}
			if t.Symphony != "" {
				w.symW = 1
			}
		}
	}
	return w
}

func computeLayout(width int, w widths) layout {
	if width < 60 {
		width = 60
	}
	prW := 30
	if width >= 120 {
		prW = 34
	}

	l := layout{
		width:  width,
		typeW:  clamp(w.typeW, 1, maxTypeCode),
		childW: clamp(w.childW, 0, maxChildW),
		keyW:   clamp(w.keyW, minKeyW, maxKeyW),
		symW:   clamp(w.symW, 0, 1),
		pr:     prW,
	}

	// total = prefix + summary + gap + connector + gap + pr + symphony, kept a
	// column short of the terminal edge.
	spend := func(pr int) int { return width - 1 - l.prefix() - 5 - pr - l.symBlock() }

	l.summary = spend(prW)
	if l.summary < 24 {
		// Squeeze the PR column before the summary; the summary carries more meaning.
		if shrink := 24 - l.summary; prW-shrink < 18 {
			prW = 18
		} else {
			prW -= shrink
		}
		l.pr = prW
		l.summary = spend(prW)
	}
	if l.summary < 10 {
		l.summary = 10
	}
	return l
}

// prSegs renders a pull request reference: repo, number and status badges.
func prSegs(pr PullRequest, w int) []seg {
	var badges []seg
	if pr.Archived {
		badges = append(badges, seg{text: " archived", st: warnStyle})
	}
	if icon, style := prCheckIcon(pr); icon != "" {
		badges = append(badges, seg{text: " " + icon, st: style})
	}
	// The approval state. GitHub reports no decision at all when the base branch
	// requires no review, so an approving review has to be counted separately or
	// a genuinely approved PR would show nothing.
	switch {
	case pr.Review == "CHANGES_REQUESTED":
		badges = append(badges, seg{text: " rev±", st: badStyle})
	case pr.Review == "APPROVED" || pr.Approvals > 0:
		badge := " rev✓"
		if pr.Approvals > 1 {
			badge += strconv.Itoa(pr.Approvals)
		}
		badges = append(badges, seg{text: badge, st: okStyle})
	case pr.Review == "REVIEW_REQUIRED":
		badges = append(badges, seg{text: " rev?", st: warnStyle})
	}

	// The state icon leads, as in workmux: <state> repo #number <checks>.
	stateIcon, stateStyle := prStateIcon(pr)
	lead := []seg{{text: stateIcon + " ", st: stateStyle}}

	labelBudget := w - segsWidth(badges) - segsWidth(lead)
	if labelBudget < 6 {
		labelBudget = 6
	}
	// The reference also carries the state in its colour, so the state reads even
	// where the glyph cannot be drawn.
	label := trunc(fmt.Sprintf("%s #%d", pr.Repo, pr.Number), labelBudget)

	segs := append(lead, seg{text: label, st: prStateStyle(pr), link: pr.URL})
	return append(segs, badges...)
}

// buildBody renders the dashboard rows and reports, for each selectable row,
// the line it landed on so the viewport can keep the cursor visible.
// typeLegend maps each abbreviation on screen back to the issue type names it
// stands for, revealing any collision instead of hiding it.
func (a *app) typeLegend() [][2]string {
	byCode := map[string][]string{}
	for _, g := range a.groups {
		for _, t := range g.Tickets {
			name := strings.TrimSpace(t.Type)
			if name == "" {
				continue
			}
			code := typeCode(name)
			if !slices.Contains(byCode[code], name) {
				byCode[code] = append(byCode[code], name)
			}
		}
	}

	codes := make([]string, 0, len(byCode))
	for code := range byCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	out := make([][2]string, 0, len(codes))
	for _, code := range codes {
		names := byCode[code]
		sort.Strings(names)
		out = append(out, [2]string{code, strings.Join(names, ", ")})
	}
	return out
}

func (a *app) buildBody(lay layout) (lines []string, rowLine []int) {
	row := 0

	for _, g := range a.groups {
		lines = append(lines, groupHeader(g.Status, g.Category, len(g.Tickets), lay.width))
		for _, t := range g.Tickets {
			selected := row == a.cursor
			rowLine = append(rowLine, len(lines))
			lines = append(lines, a.ticketLine(t, lay, selected))
			for _, extra := range t.PRs[min(1, len(t.PRs)):] {
				lines = append(lines, a.prContinuationLine(extra, lay, selected))
			}
			row++
		}
		lines = append(lines, "")
	}

	if len(a.orphans) > 0 {
		lines = append(lines, groupHeader("PRS WITHOUT AN ACTIVE TICKET", "", len(a.orphans), lay.width))
		for _, pr := range a.orphans {
			selected := row == a.cursor
			rowLine = append(rowLine, len(lines))
			lines = append(lines, a.orphanLine(pr, lay, selected))
			row++
		}
		lines = append(lines, "")
	}

	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, rowLine
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

// gutterSeg is the left margin, carrying the selection bar when selected.
func gutterSeg(selected bool) seg {
	if selected {
		return seg{text: "▌ ", st: lipgloss.NewStyle().Foreground(accent)}
	}
	return seg{text: "  "}
}

func (a *app) ticketLine(t Ticket, lay layout, selected bool) string {
	segs := []seg{
		gutterSeg(selected),
		{text: pad(typeCode(t.Type), lay.typeW+1), st: mutedStyle},
	}
	if lay.childW > 0 {
		// Right-align the count so the digits line up, and leave the cell empty
		// rather than printing 0 for the common childless case.
		count := ""
		if t.ChildCount > 0 {
			count = strconv.Itoa(t.ChildCount)
		}
		segs = append(segs, seg{
			text: padLeft(count, lay.childW) + " ",
			st:   lipgloss.NewStyle().Foreground(accent),
		})
	}
	segs = append(segs,
		seg{text: pad(trunc(t.Key, lay.keyW), lay.keyW), st: keyStyle, link: t.URL},
		seg{text: "  ", st: normalStyle},
		seg{text: pad(trunc(t.Summary, lay.summary), lay.summary), st: normalStyle},
		seg{text: "  ", st: normalStyle},
	)

	// The pull request cell, padded to its full width so whatever follows lines up.
	var prCell []seg
	if len(t.PRs) > 0 {
		prCell = append(prCell, seg{text: "→", st: lipgloss.NewStyle().Foreground(accent)}, seg{text: "  "})
		prCell = append(prCell, prSegs(t.PRs[0], lay.pr)...)
		if extra := len(t.PRs) - 1; extra > 0 {
			prCell = append(prCell, seg{text: fmt.Sprintf(" +%d", extra), st: faintStyle})
		}
	} else {
		prCell = append(prCell, seg{text: "·", st: faintStyle}, seg{text: "  "},
			seg{text: "no PR", st: faintStyle})
	}
	segs = append(segs, prCell...)

	if lay.symW > 0 {
		// Pad out the PR cell so the Symphony marker is a column at the right
		// edge rather than floating after badges of varying length.
		if fill := 3 + lay.pr - segsWidth(prCell); fill > 0 {
			segs = append(segs, seg{text: strings.Repeat(" ", fill)})
		}
		marker, style := symphonyMarker(t.Symphony)
		segs = append(segs, seg{text: " " + marker, st: style})
	}

	if selected {
		segs = highlight(segs, lay.width)
	}
	return segsRender(segs, a.hyperlinks)
}

// prContinuationLine renders a ticket's second and later pull requests, aligned
// under the first and highlighted with the ticket they belong to.
func (a *app) prContinuationLine(pr PullRequest, lay layout, selected bool) string {
	segs := []seg{{text: strings.Repeat(" ", lay.prColumn())}}
	segs = append(segs, prSegs(pr, lay.pr)...)
	if selected {
		segs = highlight(segs, lay.width)
	}
	return segsRender(segs, a.hyperlinks)
}

func (a *app) orphanLine(pr PullRequest, lay layout, selected bool) string {
	const refW = 24
	segs := []seg{gutterSeg(selected), {text: pad("⇢", 2), st: faintStyle}}
	prRef := prSegs(pr, refW)
	segs = append(segs, prRef...)
	if n := refW - segsWidth(prRef); n > 0 {
		segs = append(segs, seg{text: strings.Repeat(" ", n)})
	}

	// Leave room for the Symphony column so these rows stop at the same edge.
	titleW := lay.width - gutterCols - 2 - refW - 3 - lay.symBlock()
	segs = append(segs, seg{text: "  "}, seg{text: trunc(pr.Title, titleW), st: mutedStyle})

	if selected {
		segs = highlight(segs, lay.width)
	}
	return segsRender(segs, a.hyperlinks)
}

func (a *app) headerView(lay layout) string {
	const title = "◈ DEVDASH"

	tickets := 0
	for _, g := range a.groups {
		tickets += len(g.Tickets)
	}
	stats := fmt.Sprintf("  %d tickets · %d PRs", tickets, a.prCount())
	status := a.statusPlain()

	fixed := runewidth.StringWidth(title) + runewidth.StringWidth(stats) +
		runewidth.StringWidth(status) + 2

	// Name the repository the PRs come from, preferring the full link and
	// falling back to shorter forms when the terminal is too narrow for it.
	scope, scopeURL := "", ""
	if a.prScope != "" {
		for _, candidate := range []string{a.prScopeURL, a.prScope, shortRepo(a.prScope)} {
			if candidate == "" {
				continue
			}
			if fixed+runewidth.StringWidth(" in "+candidate)+1 <= lay.width {
				scope, scopeURL = candidate, a.prScopeURL
				break
			}
		}
	}

	scopeText := ""
	if scope != "" {
		scopeText = " in " + scope
	}

	gap := lay.width - fixed - runewidth.StringWidth(scopeText)
	if gap < 1 {
		gap = 1
	}

	rendered := titleStyle.Render(title) + mutedStyle.Render(stats)
	if scope != "" {
		link := keyStyle.Render(scope)
		if a.hyperlinks && scopeURL != "" {
			link = hyperlink(scopeURL, link)
		}
		rendered += mutedStyle.Render(" in ") + link
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
	{"⏎ ticket", 3},
	{"p PR", 5},
	{"c copy", 4},
	{"s status", 4},
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
		{"g / G", "jump to first / last row"},
		{"enter, o", "open the selected ticket in the browser"},
		{"p", "open the selected row's pull request"},
		{"c", "copy a shareable snippet of the row to the clipboard"},
		{"s", "change the selected ticket's status"},
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
		"  "+normalStyle.Render(pad("type", 16))+
			mutedStyle.Render("initials of the issue type's words: Sub-task → ST, New Feature → NF"),
		"  "+normalStyle.Render(pad("children", 16))+
			mutedStyle.Render("sub-tickets whose parent is this ticket, blank when it has none"))

	// Derived from the types actually loaded, so it describes this JIRA rather
	// than a fixed list.
	if legend := a.typeLegend(); len(legend) > 0 {
		out = append(out, "", groupHeader("TYPES IN VIEW", "", 0, lay.width))
		for _, r := range legend {
			out = append(out, "  "+normalStyle.Render(pad(r[0], 16))+mutedStyle.Render(r[1]))
		}
	}

	out = append(out, "", groupHeader("SYMPHONY", "", 0, lay.width))
	for _, s := range []struct{ state, what string }{
		{SymphonyRunning, "Symphony is working on this ticket"},
		{SymphonyBlocked, "paused waiting for operator input or approval"},
		{SymphonyRetrying, "waiting for the next retry window"},
	} {
		marker, style := symphonyMarker(s.state)
		out = append(out, "  "+style.Render(pad(marker, 16))+mutedStyle.Render(s.what))
	}

	// The PR state is carried by colour, so the samples are rendered in it.
	out = append(out, "", groupHeader("PR STATE", "", 0, lay.width))
	for _, s := range []struct {
		pr   PullRequest
		what string
	}{
		{PullRequest{State: "OPEN"}, "open"},
		{PullRequest{State: "OPEN", Draft: true}, "draft"},
		{PullRequest{State: "MERGED"}, "merged"},
		{PullRequest{State: "CLOSED"}, "closed without merging"},
	} {
		out = append(out, "  "+prStateStyle(s.pr).Render(pad("repo #123", 16))+mutedStyle.Render(s.what))
	}

	out = append(out, "", groupHeader("LEGEND", "", 0, lay.width))
	legend := [][2]string{
		{checkIcons().success + " " + checkIcons().failure + " " + checkIcons().pending,
			"checks passing · failing · running"},
		{prIcons().open + " " + prIcons().draft + " " + prIcons().merged + " " + prIcons().closed,
			"open · draft · merged · closed"},
		{"rev✓ rev± rev?", "approved (rev✓N = N approvals) · changes requested · awaiting"},
		{"archived", "the PR's repository is archived"},
		{"→  ·", "ticket has a pull request · has none"},
		{"+N", "ticket has N further pull requests, listed below"},
	}
	for _, r := range legend {
		out = append(out, "  "+normalStyle.Render(pad(r[0], 16))+mutedStyle.Render(r[1]))
	}

	out = append(out, "", faintStyle.Render("  Ticket keys and PR references are OSC 8 hyperlinks; ⌘-click or"),
		faintStyle.Render("  ctrl-click them in a terminal that supports links."))
	return out
}
