package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	// accent marks the cursor and nothing else. It used to carry five meanings at
	// once — the title, the child count, the arrow before a pull request, a
	// running Symphony session — which left it meaning none of them. Now a
	// periwinkle cell on screen always says "this is where you are".
	accent = lipgloss.Color("111")

	fgNormal = lipgloss.AdaptiveColor{Light: "236", Dark: "252"}
	fgMuted  = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	fgFaint  = lipgloss.AdaptiveColor{Light: "250", Dark: "239"}

	// The state of work: good, bad, still waiting.
	colorOK   = lipgloss.Color("77")
	colorBad  = lipgloss.Color("203")
	colorWarn = lipgloss.Color("221")

	colorMerged = lipgloss.Color("141") // a merged pull request, and nothing else
	// colorSym was magenta 213, which read as the same purple as a merged pull
	// request when both landed on one row — and a ticket Symphony is working is
	// exactly the kind that has one. Cyan shares a row with nothing.
	colorSym = lipgloss.Color("87") // a Symphony session, and nothing else
	// Review statuses used to share violet with a merged pull request, so one hue
	// meant two unrelated things on the same screen. They get their own.
	colorReview = lipgloss.Color("117")

	// selBg paints the selected row end to end, so the highlight covers the
	// ticket, the pull request and the space between them. selColBg is a shade
	// brighter, marking the column left/right is currently on.
	selBg = lipgloss.AdaptiveColor{Light: "254", Dark: "238"}
	// A contrasting hue, not a brighter grey: two greys a few steps apart in
	// the 256-colour palette are not distinguishable on most themes.
	selColBg = lipgloss.AdaptiveColor{Light: "153", Dark: "24"}

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(fgNormal)
	normalStyle   = lipgloss.NewStyle().Foreground(fgNormal)
	mutedStyle    = lipgloss.NewStyle().Foreground(fgMuted)
	faintStyle    = lipgloss.NewStyle().Foreground(fgFaint)
	keyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("152"))
	keySelStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	summarySelSty = lipgloss.NewStyle().Bold(true).Foreground(fgNormal)
	okStyle       = lipgloss.NewStyle().Foreground(colorOK)
	badStyle      = lipgloss.NewStyle().Foreground(colorBad)
	warnStyle     = lipgloss.NewStyle().Foreground(colorWarn)
	errStyle      = lipgloss.NewStyle().Foreground(colorBad).Bold(true)
	countStyle    = lipgloss.NewStyle().Foreground(fgFaint)
	mergedStyle   = lipgloss.NewStyle().Foreground(colorMerged)

	symStyle     = lipgloss.NewStyle().Foreground(colorSym).Bold(true)
	symWarnStyle = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	// alertStyle reverses as well as reddens. Reverse video is a shape, not a
	// hue, so the one state that needs a human still reads where colour does
	// not: a piped -once snapshot, NO_COLOR, or red-green colour blindness.
	alertStyle = lipgloss.NewStyle().Foreground(colorBad).Bold(true).Reverse(true)
)

// The indicator alphabet. Every glyph here is plain Unicode and one display
// column wide, which is what lets the whole design work without a Nerd Font.
const (
	iconPR      = "●" // a pull request
	iconPRDraft = "○" // ...that has not been offered for review yet

	iconCheckPass = "✓"
	iconCheckFail = "✗"
	iconCheckRun  = "◷"

	iconApproved = "✓"
	iconChanges  = "↩"

	// iconSymphony serves every session state. The note means Symphony has this
	// ticket; the colour says what it is doing with it.
	iconSymphony = "♪"

	iconAlert = "!"

	// iconChild marks a ticket with children, iconSubtask one that is a child.
	// The two are mutually exclusive, so they share a single column.
	iconChild   = "+"
	iconSubtask = "↳"
)

// symphonyMarker is the glyph and colour for a Symphony session state. The glyph
// never changes: it is the colour that says whether Symphony is queued on this
// ticket, working it, waiting to retry, or stuck and wanting a human.
func symphonyMarker(state string) (string, lipgloss.Style) {
	switch state {
	case SymphonyScheduled:
		return iconSymphony, faintStyle
	case SymphonyRunning:
		return iconSymphony, symStyle
	case SymphonyRetrying:
		return iconSymphony, symWarnStyle
	case SymphonyBlocked:
		return iconSymphony, alertStyle
	default:
		return " ", faintStyle
	}
}

// prStateIcon is the glyph and colour for what the pull request is. Colour
// carries the state; the glyph's shape carries whether it is a draft, because a
// draft is not a fourth state but a flag on an open pull request — and a draft
// that was closed is closed.
func prStateIcon(pr PullRequest) (string, lipgloss.Style) {
	if pr.Draft {
		return iconPRDraft, prStateStyle(pr)
	}
	return iconPR, prStateStyle(pr)
}

// prCheckIcon is the glyph and colour for the CI rollup of the head commit. An
// empty string means nothing is reported, so nothing is drawn.
func prCheckIcon(pr PullRequest) (string, lipgloss.Style) {
	switch pr.CI {
	case "SUCCESS":
		// Passing is what a pull request is supposed to do, so it is drawn
		// recessively. The only saturated ink in this slot should be a failure.
		return iconCheckPass, mutedStyle
	case "FAILURE", "ERROR":
		return iconCheckFail, badStyle
	case "PENDING", "EXPECTED":
		return iconCheckRun, warnStyle
	default:
		return "", faintStyle
	}
}

// prStateStyle colours a pull request reference by its state: normal for open,
// violet for merged, red for closed, and faint for a draft still open. Closed
// and merged outrank draft, because a draft that was closed is closed.
func prStateStyle(pr PullRequest) lipgloss.Style {
	switch pr.State {
	case "MERGED":
		return mergedStyle
	case "CLOSED":
		return badStyle
	}
	if pr.Draft {
		return faintStyle
	}
	return normalStyle
}

// statusColor maps a JIRA status onto a colour, keying off well-known status
// names first and falling back to the status category.
func statusColor(status, category string) lipgloss.TerminalColor {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked", "impeded", "on hold":
		return colorBad
	case "awaiting cr", "in review", "code review", "review", "peer review":
		return colorReview
	case "in progress", "in development", "in dev", "doing":
		return lipgloss.Color("214")
	case "to do", "open", "new", "selected for development", "ready for dev":
		return lipgloss.Color("75")
	case "triage":
		return lipgloss.Color("109")
	case "backlog":
		return lipgloss.Color("245")
	}
	switch category {
	case "In Progress":
		return lipgloss.Color("214")
	case "Done":
		return colorOK
	default:
		return lipgloss.Color("75")
	}
}

// seg is a run of text with a single style. Segments let a composite cell be
// measured in display columns before any escape sequences are added.
type seg struct {
	text string
	st   lipgloss.Style
	link string // optional URL to wrap this segment in an OSC 8 hyperlink
	// stop marks which left/right column this segment belongs to, stored one
	// higher than the column index so that the zero value means "no column".
	// Anything not explicitly tagged is therefore never painted as active.
	stop int
}

// noStop is the zero value: a segment left/right never lands on.
const noStop = 0

// stopTag encodes a column index for seg.stop, mapping "none" to the zero value.
func stopTag(col int) int {
	if col < 0 {
		return noStop
	}
	return col + 1
}

func segsWidth(ss []seg) int {
	w := 0
	for _, s := range ss {
		w += runewidth.StringWidth(s.text)
	}
	return w
}

func segsRender(ss []seg, hyperlinks bool) string {
	var b strings.Builder
	for _, s := range ss {
		rendered := s.st.Render(s.text)
		if hyperlinks && s.link != "" {
			rendered = hyperlink(s.link, rendered)
		}
		b.WriteString(rendered)
	}
	return b.String()
}

// segsPad renders the segments and pads the result to w display columns.
func segsPad(ss []seg, w int, hyperlinks bool) string {
	out := segsRender(ss, hyperlinks)
	if n := w - segsWidth(ss); n > 0 {
		out += strings.Repeat(" ", n)
	}
	return out
}

// highlight marks a row as selected: every segment gets the selection
// background and bold text, and the row is padded out to width. Doing it here
// means selection styling is decided in one place, and the whole row — ticket,
// connector and pull request alike — is emphasised rather than just the ticket.
func highlight(segs []seg, width int, activeStop int) []seg {
	out := make([]seg, 0, len(segs)+1)
	for _, s := range segs {
		bg := selBg
		if activeStop >= 0 && s.stop == stopTag(activeStop) {
			bg = selColBg
		}
		s.st = s.st.Background(bg).Bold(true)
		out = append(out, s)
	}
	if fill := width - segsWidth(segs); fill > 0 {
		out = append(out, seg{
			text: strings.Repeat(" ", fill),
			st:   lipgloss.NewStyle().Background(selBg),
		})
	}
	return out
}

// hyperlink wraps text in an OSC 8 escape so terminals that support it render
// a clickable link. Terminals that do not simply show the text.
func hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
