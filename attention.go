package main

import "github.com/charmbracelet/lipgloss"

// attention is the single most urgent thing a row wants from you. It exists so
// the leftmost column of the dashboard can be read on its own, as a strip of
// "what needs me", instead of the answer being spread across a review badge, a
// check glyph and a Symphony marker at three different ends of the row.
//
// The values are ordered by urgency so that rolling a ticket up from its pull
// requests is a max, and the ticket ends up marked with the worst of them.
type attention int

const (
	attnNone attention = iota
	// attnMerge: approved and green. Nothing is wrong; there is just a merge
	// button waiting for you.
	attnMerge
	// attnChanges: a reviewer sent it back.
	attnChanges
	// attnAlert: the build is broken, or Symphony is stuck and wants an operator.
	attnAlert
)

// marker is the glyph and style for the attention column. attnNone draws a
// space: a row that wants nothing from you should say nothing.
func (a attention) marker() (string, lipgloss.Style) {
	switch a {
	case attnMerge:
		return iconApproved, okStyle
	case attnChanges:
		return iconChanges, badStyle
	case attnAlert:
		return iconAlert, alertStyle
	default:
		return " ", faintStyle
	}
}

// attentionForPR is what one pull request wants. A pull request that is no
// longer open wants nothing: merged and closed ones are on screen as context for
// a ticket that is still open, not as work.
func attentionForPR(pr PullRequest) attention {
	if pr.State != "OPEN" {
		return attnNone
	}
	switch pr.CI {
	case "FAILURE", "ERROR":
		return attnAlert
	}
	if pr.Review == "CHANGES_REQUESTED" {
		return attnChanges
	}
	// A draft has not been offered for review, so an approval sitting on one is
	// not a cue to merge it.
	if pr.Draft {
		return attnNone
	}
	// GitHub leaves Review empty when the base branch requires no review, so an
	// approved pull request can arrive with no decision at all. Counting the
	// approving reviews as well is what makes that approval visible.
	if pr.CI == "SUCCESS" && (pr.Review == "APPROVED" || pr.Approvals > 0) {
		return attnMerge
	}
	return attnNone
}

// attentionFor is what a ticket wants: the worst of its pull requests, and a
// blocked Symphony session regardless of what its pull requests say.
func attentionFor(t Ticket) attention {
	a := attnNone
	if t.Symphony == SymphonyBlocked {
		a = attnAlert
	}
	for _, pr := range t.PRs {
		a = max(a, attentionForPR(pr))
	}
	return a
}

// needsYou counts the rows wanting something, for the header tally. It counts
// rows rather than reasons, because it answers "how many things should I look
// at", and one ticket with three broken pull requests is one thing to look at.
func needsYou(groups []group, orphans []PullRequest) int {
	n := 0
	for _, g := range groups {
		for _, t := range g.Tickets {
			if attentionFor(t) != attnNone {
				n++
			}
		}
	}
	for _, pr := range orphans {
		if attentionForPR(pr) != attnNone {
			n++
		}
	}
	return n
}
