package main

// demoData fills the dashboard with representative sample data so the interface
// can be seen without any credentials, and so documentation screenshots do not
// depend on somebody's real tickets.
//
// It exercises the cases worth showing: several statuses, a parent with children,
// every pull request state, a ticket with two PRs, a PR with no ticket, and a
// Symphony session.
func demoData() ([]Ticket, []PullRequest) {
	tickets := []Ticket{
		{
			Key: "PROJ-482", Summary: "Mark the CI wait helper as manual-only",
			Status: "Awaiting CR", Category: "In Progress", Type: "Task",
			URL: "https://your-org.atlassian.net/browse/PROJ-482",
		},
		{
			Key: "PROJ-475", Summary: "Add account identifiers to each configured database",
			Status: "Awaiting CR", Category: "In Progress", Type: "Sub-task",
			IsSubtask: true,
			URL:       "https://your-org.atlassian.net/browse/PROJ-475",
		},
		{
			Key: "PROJ-455", Summary: "Add a shared database registry",
			Status: "In Progress", Category: "In Progress", Type: "Task",
			ChildCount: 4, Symphony: SymphonyRunning,
			URL: "https://your-org.atlassian.net/browse/PROJ-455",
		},
		{
			Key: "PROJ-301", Summary: "The on-prem effort",
			Status: "In Progress", Category: "In Progress", Type: "Epic",
			ChildCount: 11,
			URL:        "https://your-org.atlassian.net/browse/PROJ-301",
		},
		{
			// Queued for Symphony but not picked up yet: Symphony polls, so this
			// is the ordinary state right after scheduling one.
			Key: "PROJ-460", Summary: "Ship the static data-plane runtime",
			Status: "To Do", Category: "To Do", Type: "Task",
			Symphony: SymphonyScheduled,
			URL:      "https://your-org.atlassian.net/browse/PROJ-460",
		},
		{
			Key: "TEAM-1204", Summary: "Unified Core Platform",
			Status: "TRIAGE", Category: "To Do", Type: "Initiative",
			ChildCount: 14,
			URL:        "https://your-org.atlassian.net/browse/TEAM-1204",
		},
		{
			Key: "PROJ-198", Summary: "Spike on package structure",
			Status: "Backlog", Category: "To Do", Type: "Story",
			URL: "https://your-org.atlassian.net/browse/PROJ-198",
		},
		{
			Key: "PROJ-204", Summary: "Improve configuration structure",
			Status: "Backlog", Category: "To Do", Type: "Story",
			URL: "https://your-org.atlassian.net/browse/PROJ-204",
		},
	}

	prs := []PullRequest{
		{
			Repo: "platform", Number: 1099, Title: "PROJ-482: mark the helper manual-only",
			URL: "https://github.com/acme/platform/pull/1099", Branch: "PROJ-482",
			State: "OPEN", Draft: true, CI: "SUCCESS", Review: "REVIEW_REQUIRED",
		},
		{
			Repo: "platform", Number: 1103, Title: "PROJ-475: add attribution identifiers",
			URL: "https://github.com/acme/platform/pull/1103", Branch: "PROJ-475-identifiers",
			State: "OPEN", CI: "SUCCESS", Approvals: 2,
		},
		{
			Repo: "platform", Number: 1088, Title: "PROJ-455: registry groundwork",
			URL: "https://github.com/acme/platform/pull/1088", Branch: "PROJ-455-groundwork",
			State: "MERGED", CI: "SUCCESS", Review: "APPROVED", Approvals: 1,
		},
		{
			Repo: "platform", Number: 1091, Title: "PROJ-455: wire the registry in",
			URL: "https://github.com/acme/platform/pull/1091", Branch: "PROJ-455-wiring",
			State: "OPEN", CI: "PENDING", Review: "CHANGES_REQUESTED",
		},
		{
			Repo: "sandbox", Number: 5, Title: "Monorepo template with clean architecture",
			URL: "https://github.com/acme/sandbox/pull/5", Branch: "template",
			State: "OPEN", CI: "FAILURE", Review: "REVIEW_REQUIRED",
		},
	}
	return tickets, prs
}

// loadDemo puts the sample data on the app and stops it reaching any API.
func (a *app) loadDemo() {
	a.demo = true
	a.jira, a.gh = nil, nil
	a.trackers, a.ghCfg = nil, nil
	a.trackerCfgErr, a.trackerErr, a.ghErr, a.trackerWarn = nil, nil, nil, nil
	a.prScope = "acme/platform"
	a.prScopeURL = "https://github.com/acme/platform"
	a.tickets, a.prs = demoData()
	a.lastFetch = a.now
	a.settle()
}
