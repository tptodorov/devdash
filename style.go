package main

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	accent      = lipgloss.Color("111")
	fgNormal    = lipgloss.AdaptiveColor{Light: "236", Dark: "252"}
	fgMuted     = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	fgFaint     = lipgloss.AdaptiveColor{Light: "250", Dark: "239"}
	colorOK     = lipgloss.Color("77")
	colorBad    = lipgloss.Color("203")
	colorWarn   = lipgloss.Color("221")
	colorMerged = lipgloss.Color("141")

	// selBg paints the selected row end to end, so the highlight covers the
	// ticket, the pull request and the space between them.
	selBg = lipgloss.AdaptiveColor{Light: "254", Dark: "238"}

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
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
)

// Symphony markers. A running session is informational, but a blocked one is
// waiting on you, so it gets its own glyph rather than only a colour.
const (
	symphonyIconRunning  = "♪"
	symphonyIconBlocked  = "!"
	symphonyIconRetrying = "↻"
)

// symphonyMarker is the glyph and colour for a Symphony session state.
func symphonyMarker(state string) (string, lipgloss.Style) {
	switch state {
	case SymphonyRunning:
		return symphonyIconRunning, lipgloss.NewStyle().Foreground(accent)
	case SymphonyBlocked:
		return symphonyIconBlocked, errStyle
	case SymphonyRetrying:
		return symphonyIconRetrying, warnStyle
	default:
		return " ", faintStyle
	}
}

// Pull request icons, matching workmux (github.com/raine/workmux) so the two
// tools read the same way side by side. Nerd Font glyphs are used by default,
// with the plain-Unicode set workmux falls back to when nerd fonts are off.
//
// The codepoints are workmux's own, including the older Octicon positions for
// draft and closed.
var (
	useNerdFont = true

	nerdPRIcons = prIconSet{
		// workmux uses U+F177 and U+F406 here, but current Nerd Fonts have
		// reassigned both: F177 is fa-arrow_left_long and F406 is
		// oct-accessibility. These are the positions the draft and closed
		// Octicons actually live at now.
		draft:  "\uf4dd", // nf-oct-git_pull_request_draft
		open:   "\uf407", // nf-oct-git_pull_request
		merged: "\uf419", // nf-oct-git_merge
		closed: "\uf4dc", // nf-oct-git_pull_request_closed
	}
	fallbackPRIcons = prIconSet{draft: "○", open: "●", merged: "◆", closed: "×"}

	nerdCheckIcons = checkIconSet{
		success: "\U000f0134", // nf-md-checkbox_marked_circle_outline
		failure: "\U000f0159", // nf-md-close_circle
		// workmux writes U+F0520 here and calls it timer_sand, but that is
		// md-timetable; timer_sand is one position lower.
		pending: "\U000f051f", // nf-md-timer_sand
	}
	fallbackCheckIcons = checkIconSet{success: "✓", failure: "×", pending: "◷"}
)

type prIconSet struct{ draft, open, merged, closed string }
type checkIconSet struct{ success, failure, pending string }

func prIcons() prIconSet {
	if useNerdFont {
		return nerdPRIcons
	}
	return fallbackPRIcons
}

func checkIcons() checkIconSet {
	if useNerdFont {
		return nerdCheckIcons
	}
	return fallbackCheckIcons
}

// prStateIcon is the glyph and colour for what the pull request is. Closed and
// merged outrank draft, because a draft that was closed is closed.
func prStateIcon(pr PullRequest) (string, lipgloss.Style) {
	icons := prIcons()
	switch {
	case pr.State == "MERGED":
		return icons.merged, mergedStyle
	case pr.State == "CLOSED":
		return icons.closed, badStyle
	case pr.Draft:
		return icons.draft, faintStyle
	default:
		return icons.open, normalStyle
	}
}

// prCheckIcon is the glyph and colour for the CI rollup of the head commit. An
// empty string means nothing is reported, so nothing is drawn.
func prCheckIcon(pr PullRequest) (string, lipgloss.Style) {
	icons := checkIcons()
	switch pr.CI {
	case "SUCCESS":
		return icons.success, okStyle
	case "FAILURE", "ERROR":
		return icons.failure, badStyle
	case "PENDING", "EXPECTED":
		return icons.pending, warnStyle
	default:
		return "", faintStyle
	}
}

// prStateStyle colours a pull request reference by its state: normal for open,
// grey for a draft, red for closed, violet for merged. Closed and merged outrank
// draft, because a draft that was closed is closed.
func prStateStyle(pr PullRequest) lipgloss.Style {
	switch {
	case pr.State == "MERGED":
		return mergedStyle
	case pr.State == "CLOSED":
		return badStyle
	case pr.Draft:
		return faintStyle
	default:
		return normalStyle
	}
}

// statusColor maps a JIRA status onto a colour, keying off well-known status
// names first and falling back to the status category.
func statusColor(status, category string) lipgloss.TerminalColor {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked", "impeded", "on hold":
		return colorBad
	case "awaiting cr", "in review", "code review", "review", "peer review":
		return lipgloss.Color("141")
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

// maxTypeCode caps the derived abbreviation so one verbose issue type cannot
// widen the column for every row.
const maxTypeCode = 4

// typeCode abbreviates any JIRA issue type by taking the initial of each word
// and capitalising it: Task becomes T, Sub-task ST, New Feature NF, Change
// Request CR. Nothing is hard-coded, so it works on any project's workflow
// without being taught its types.
func typeCode(issueType string) string {
	words := strings.FieldsFunc(issueType, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	code := make([]rune, 0, len(words))
	for _, w := range words {
		code = append(code, unicode.ToUpper([]rune(w)[0]))
		if len(code) == maxTypeCode {
			break
		}
	}
	if len(code) == 0 {
		return "·"
	}
	return string(code)
}

// seg is a run of text with a single style. Segments let a composite cell be
// measured in display columns before any escape sequences are added.
type seg struct {
	text string
	st   lipgloss.Style
	link string // optional URL to wrap this segment in an OSC 8 hyperlink
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
func highlight(segs []seg, width int) []seg {
	out := make([]seg, 0, len(segs)+1)
	for _, s := range segs {
		s.st = s.st.Background(selBg).Bold(true)
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
