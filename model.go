package main

import (
	"regexp"
	"sort"
	"strings"
)

// Ticket is an issue assigned to the current user, together with any pull
// requests that reference it.
type Ticket struct {
	Key      string
	Summary  string
	Status   string
	Category string // status category: "In Progress", "To Do", "Done"
	Type     string // the project's own issue type name, whatever it is
	URL      string
	Labels   []string
	// IsSubtask comes from the tracker's own sub-task/sub-issue flag rather
	// than the type's name, so it holds for any project's naming.
	IsSubtask  bool
	ChildCount int // issues whose parent is this ticket, any assignee
	// Symphony is the state of the Symphony session working this ticket:
	// running, blocked, retrying, or empty when Symphony is not on it.
	Symphony string
	PRs      []PullRequest
	// Source names the Tracker this ticket came from, e.g. "JIRA" or
	// "Linear". Write actions (status change, Symphony scheduling) are JIRA
	// workflows with no equivalent wired up for other trackers, so they are
	// gated on this field rather than on which trackers happen to be
	// configured.
	Source string
}

// childCandidates lists the tickets worth asking JIRA about children for. JIRA
// forbids sub-tasks of sub-tasks, so those can be skipped; everything else,
// including epics and initiatives, may have children worth counting.
func childCandidates(tickets []Ticket) []string {
	var out []string
	for _, t := range tickets {
		if t.IsSubtask {
			continue
		}
		if ticketKeyRE.MatchString(t.Key) {
			out = append(out, t.Key)
		}
	}
	return out
}

// applyChildCounts records how many children each ticket has. Only tickets
// from source are touched, since counts were only ever queried for that
// tracker; a ticket from another tracker that happens to share a key is left
// alone rather than getting an unrelated count.
func applyChildCounts(tickets []Ticket, counts map[string]int, source string) {
	for i := range tickets {
		if tickets[i].Source != source {
			continue
		}
		tickets[i].ChildCount = counts[strings.ToUpper(tickets[i].Key)]
	}
}

// PullRequest is an open pull request authored by the current user.
type PullRequest struct {
	Repo     string
	Number   int
	Title    string
	URL      string
	Branch   string
	Draft    bool
	State    string // OPEN, CLOSED, MERGED
	Archived bool   // the PR's repository is archived
	Review   string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	// Approvals counts approving reviews. GitHub leaves Review empty when the
	// base branch requires none, so an approved PR can arrive with no decision
	// at all; this is what makes that approval visible.
	Approvals int
	CI        string // SUCCESS, FAILURE, PENDING, ERROR, ""
	Ticket    string // ticket key parsed from the title or branch, "" if none
}

// dropArchived removes pull requests whose repository is archived. GitHub keeps
// serving them in search results, but a PR in an archived repo can no longer be
// merged, so it is noise on a dashboard of live work.
func dropArchived(prs []PullRequest, include bool) []PullRequest {
	if include {
		return prs
	}
	out := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if !pr.Archived {
			out = append(out, pr)
		}
	}
	return out
}

// group is a set of tickets sharing a JIRA status, rendered under one header.
type group struct {
	Status   string
	Category string
	Tickets  []Ticket
}

// ticketKeyRE matches JIRA keys such as PROJ-17506 or TEAM-205732.
var ticketKeyRE = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9})-(\d+)\b`)

// ticketKeyFor extracts the ticket key a pull request addresses, preferring the
// title (where the convention puts it) and falling back to the branch name.
func ticketKeyFor(pr PullRequest) string {
	if m := ticketKeyRE.FindString(pr.Title); m != "" {
		return m
	}
	return ticketKeyRE.FindString(pr.Branch)
}

// statusRank orders known workflow statuses so the most urgent appear first.
// Statuses absent from the map fall back to defaultStatusRank.
var statusRank = map[string]int{
	"blocked": 0, "impeded": 0, "on hold": 5,
	"awaiting cr": 10, "in review": 10, "code review": 10, "review": 10, "peer review": 10,
	"in progress": 20, "in development": 20, "in dev": 20, "doing": 20,
	"selected for development": 30, "ready for dev": 30,
	"to do": 40, "open": 40, "new": 40,
	"triage":  50,
	"backlog": 60,
}

const defaultStatusRank = 35

var categoryRank = map[string]int{"In Progress": 0, "To Do": 1, "Done": 2}

func rankFor(status, category string) (int, int) {
	r, ok := statusRank[strings.ToLower(strings.TrimSpace(status))]
	if !ok {
		r = defaultStatusRank
	}
	c, ok := categoryRank[category]
	if !ok {
		c = 1
	}
	return c, r
}

// build correlates tickets with pull requests and buckets the tickets by
// status. Pull requests that do not map onto an active ticket are returned
// separately so they stay visible instead of being silently dropped.
func build(tickets []Ticket, prs []PullRequest) ([]group, []PullRequest) {
	// A key is only guaranteed unique within its own tracker, so two trackers
	// issuing the same key (e.g. a JIRA project and a Linear team sharing a
	// short code) must not have one silently overwrite the other here; every
	// ticket matching a key gets the pull request.
	byKey := make(map[string][]int, len(tickets))
	for i, t := range tickets {
		key := strings.ToUpper(t.Key)
		byKey[key] = append(byKey[key], i)
	}

	var orphans []PullRequest
	for _, pr := range prs {
		pr.Ticket = ticketKeyFor(pr)
		if idxs, ok := byKey[strings.ToUpper(pr.Ticket)]; ok {
			for _, i := range idxs {
				tickets[i].PRs = append(tickets[i].PRs, pr)
			}
			continue
		}
		orphans = append(orphans, pr)
	}

	for i := range tickets {
		sort.SliceStable(tickets[i].PRs, func(a, b int) bool {
			return tickets[i].PRs[a].Number < tickets[i].PRs[b].Number
		})
	}

	byStatus := map[string]*group{}
	var order []string
	for _, t := range tickets {
		g, ok := byStatus[t.Status]
		if !ok {
			g = &group{Status: t.Status, Category: t.Category}
			byStatus[t.Status] = g
			order = append(order, t.Status)
		}
		g.Tickets = append(g.Tickets, t)
	}

	groups := make([]group, 0, len(order))
	for _, s := range order {
		groups = append(groups, *byStatus[s])
	}
	sort.SliceStable(groups, func(a, b int) bool {
		ca, ra := rankFor(groups[a].Status, groups[a].Category)
		cb, rb := rankFor(groups[b].Status, groups[b].Category)
		if ca != cb {
			return ca < cb
		}
		if ra != rb {
			return ra < rb
		}
		return groups[a].Status < groups[b].Status
	})

	// A merged pull request is useful as context for a ticket that is still open,
	// which is why merged PRs are fetched at all. One that matches no active
	// ticket is simply finished work, and listing it would bury the open PRs that
	// still need attention.
	kept := orphans[:0]
	for _, pr := range orphans {
		if pr.State != "MERGED" {
			kept = append(kept, pr)
		}
	}
	orphans = kept

	sort.SliceStable(orphans, func(a, b int) bool {
		if orphans[a].Repo != orphans[b].Repo {
			return orphans[a].Repo < orphans[b].Repo
		}
		return orphans[a].Number < orphans[b].Number
	})

	return groups, orphans
}
