package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// helpTopics are the argv values that mean "explain yourself".
var helpTopics = map[string]bool{
	"help": true, "-h": true, "-help": true, "--help": true,
}

func section(w io.Writer, name string) {
	fmt.Fprintf(w, "\n%s\n", titleStyle.Render(name))
}

func row(w io.Writer, left, right string) {
	fmt.Fprintf(w, "  %s%s\n", normalStyle.Render(pad(left, 22)), mutedStyle.Render(right))
}

// prose writes an indented paragraph line, leaving blank lines truly blank
// rather than two stray spaces.
func prose(w io.Writer, line string) {
	if line == "" {
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintf(w, "  %s\n", mutedStyle.Render(line))
}

// writeHelp prints everything needed to run devdash, what it will do, and every
// call it makes on your behalf.
func writeHelp(w io.Writer) {
	fmt.Fprintf(w, "%s — %s\n",
		titleStyle.Render("devdash"),
		mutedStyle.Render("your active JIRA tickets and the pull requests addressing them, on one page."))

	section(w, "WHAT IT DOES")
	for _, line := range []string{
		"Fetches every JIRA issue assigned to you that has not reached the Done",
		"status category, and every open pull request you authored, then correlates",
		"them. A PR attaches to a ticket when a JIRA key appears in its title,",
		"falling back to its branch name.",
		"",
		"Tickets are grouped by status, most urgent first, and colour coded. Pull",
		"requests matching no active ticket are listed under their own heading",
		"rather than hidden. PRs in archived repositories are dropped.",
		"",
		"The view refreshes on its own, and ticket keys and PR references are",
		"clickable links in terminals that support OSC 8 hyperlinks.",
	} {
		prose(w, line)
	}

	section(w, "REQUIREMENTS")
	fmt.Fprintf(w, "  %s\n", faintStyle.Render(
		pad("environment variable", 23)+pad("purpose", 42)+"status"))
	for _, req := range []struct{ name, purpose string }{
		{"JIRA_URL", "e.g. https://your-org.atlassian.net"},
		{"JIRA_USERNAME", "your Atlassian account email"},
		{"JIRA_API_TOKEN", "id.atlassian.com > Security > API tokens"},
		{"GITHUB_TOKEN", "token with repo scope (or GH_TOKEN)"},
	} {
		fmt.Fprintf(w, "  %s%s%s\n",
			normalStyle.Render(pad(req.name, 23)),
			mutedStyle.Render(pad(trunc(req.purpose, 40), 42)),
			envStatus(req.name))
	}
	fmt.Fprintf(w, "\n  %s\n", faintStyle.Render("Both APIs are called directly over HTTPS. If you use the GitHub CLI:"))
	fmt.Fprintf(w, "  %s\n", mutedStyle.Render("export GITHUB_TOKEN=$(gh auth token)"))

	section(w, "USAGE")
	row(w, "devdash", "the interactive dashboard")
	row(w, "devdash help", "this text")
	row(w, "devdash -once", "print one snapshot and exit, e.g. to pipe somewhere")

	section(w, "FLAGS")
	flag.CommandLine.SetOutput(w)
	flag.PrintDefaults()

	section(w, "KEYS")
	for _, k := range [][2]string{
		{"↑/k, ↓/j", "move between rows"},
		{"←/h, →/l", "move between the columns of the selected row"},
		{"g / G", "jump to the first / last row"},
		{"enter", "open whatever the selected column points at"},
		{"o", "open the ticket, whichever column is selected"},
		{"p", "open the selected row's pull request"},
		{"c", "copy a shareable snippet: title, ticket link, every PR link"},
		{"s", "change the selected ticket's status"},
		{"S", "schedule the ticket for Symphony, or take it back unless an agent is running"},
		{"r", "refresh now"},
		{"a", "pause or resume the automatic refresh"},
		{"?", "keys, columns and every indicator explained"},
		{"q, esc, ctrl+c", "quit"},
	} {
		row(w, k[0], k[1])
	}

	section(w, "COLUMN NAVIGATION")
	prose(w, "Left and right walk the columns of the selected row, in the order they are")
	prose(w, "drawn. The active one is highlighted in a contrasting colour; enter opens")
	prose(w, "what it points at:")
	prose(w, "")
	row(w, "Symphony", "the Symphony dashboard")
	row(w, "relation", "a JIRA search for the ticket's sub-tickets")
	row(w, "ticket", "the ticket in JIRA")
	row(w, "pull request", "that pull request; each one on the row is its own column")
	prose(w, "")
	prose(w, "Columns appear only when they do on screen, so left and right never land")
	prose(w, "somewhere that would do nothing. A sub-task's "+iconSubtask+" is not a stop: the ticket")
	prose(w, "does not carry its parent's key, so there is nothing to open.")

	section(w, "COLUMNS")
	row(w, "attention", "leftmost: the one thing this row wants from you, if anything")
	row(w, "symphony", "what an agent is doing with the ticket, when one has it")
	row(w, "relation", "+N sub-tickets, or "+iconSubtask+" when the ticket is itself a sub-task")
	row(w, "ticket", "key and summary, hyperlinked to JIRA")
	row(w, "pull request", "repo and number, coloured by state, then checks and review")
	row(w, "PR title", "up to 30 columns, with the ticket key it repeats dropped")
	prose(w, "")
	prose(w, "Every column is as wide as its widest value and no wider, and a column with")
	prose(w, "nothing in it disappears. The summary is the one that flexes: it takes")
	prose(w, "whatever the terminal leaves, up to the longest summary loaded. A ticket's")
	prose(w, "first PR shares its line; each further one gets a line of its own, marked")
	prose(w, iconSubtask+" beneath it. A ticket with no pull request draws nothing there.")

	section(w, "ATTENTION")
	prose(w, "The leftmost column answers one question — is there something here for me —")
	prose(w, "so the edge of the screen can be read on its own. A ticket takes the most")
	prose(w, "urgent state of any of its pull requests:")
	prose(w, "")
	for _, s := range []struct {
		a    attention
		what string
	}{
		{attnMerge, "approved and green: merge it"},
		{attnChanges, "changes requested: respond to the review"},
		{attnAlert, "the build failed, or Symphony is blocked waiting on you"},
	} {
		marker, style := s.a.marker()
		fmt.Fprintf(w, "  %s%s\n", style.Render(pad(marker, 22)), mutedStyle.Render(s.what))
	}

	section(w, "SYMPHONY")
	prose(w, "Tickets Symphony has in hand, or has been given, are marked at the left-hand")
	prose(w, "edge, beside the attention marker. The note is the constant — it means")
	prose(w, "Symphony has this ticket — and the colour says what it is doing with it.")
	prose(w, "Scheduled is grey because nothing is happening yet: Symphony polls, so a")
	prose(w, "freshly scheduled ticket waits up to one interval.")
	prose(w, "")
	for _, s := range []struct{ state, what string }{
		{SymphonyScheduled, "grey: scheduled, waiting for Symphony to pick it up"},
		{SymphonyRunning, "magenta: Symphony is working on it"},
		{SymphonyRetrying, "yellow: waiting for the next retry window"},
		{SymphonyBlocked, "red: paused waiting for operator input or approval"},
	} {
		marker, style := symphonyMarker(s.state)
		fmt.Fprintf(w, "  %s%s\n", style.Render(pad(marker, 22)), mutedStyle.Render(s.what))
	}
	prose(w, "")
	prose(w, "S toggles the selected ticket in and out of Symphony's queue. The conditions")
	prose(w, "come from the tracker block of WORKFLOW.md, so nothing here is assumed: the")
	prose(w, "ticket must belong to project_key, carry every required_label, and sit in one")
	prose(w, "of active_states.")
	prose(w, "")
	prose(w, "On a ticket Symphony would not pick up, S adds whatever is missing. On one it")
	prose(w, "already would, S removes the required labels again, leaving the status alone.")
	prose(w, "")
	prose(w, "Stuck work can be taken back: a blocked or retrying ticket comes out of the")
	prose(w, "queue so it can be fixed and scheduled afresh, since Symphony re-reads the")
	prose(w, "labels before it acts on either and drops its claim when they are gone.")
	prose(w, "")
	prose(w, "Refused rather than done: a ticket in a terminal_state, one from another")
	prose(w, "project, and unscheduling one an agent is actively running — no label change")
	prose(w, "interrupts a turn already in progress, so stop that session in Symphony.")
	prose(w, "")
	prose(w, "The instance is found from the server block of WORKFLOW.md's front matter,")
	prose(w, "rediscovered and queried on every refresh, since Symphony starts and stops")
	prose(w, "independently of this tool. If it is not running, nothing is shown and no")
	prose(w, "error is reported — that is the ordinary case.")

	section(w, "CHECKS AND REVIEW")
	prose(w, "Two fixed slots follow every pull request reference, checks then review, so")
	prose(w, "they can be read as columns down the page. Each is blank when there is")
	prose(w, "nothing to say: a pull request merely awaiting review is behaving normally,")
	prose(w, "and only a deviation from that is worth the ink.")
	prose(w, "")
	// The two slots share the ✓ glyph and are told apart by position and colour,
	// so the samples name their slot rather than standing on the glyph alone.
	row(w, "checks "+iconCheckPass, "passing — drawn faintly, because passing is expected")
	row(w, "checks "+iconCheckFail, "failing")
	row(w, "checks "+iconCheckRun, "still running")
	row(w, "review "+iconApproved, "approved; "+iconApproved+"3 means three approving reviews")
	row(w, "review "+iconChanges, "changes requested")
	prose(w, "")
	prose(w, "The archived note follows both slots, so it cannot shift them out of line,")
	prose(w, "and it is dropped on a terminal with no room for it:")
	prose(w, "")
	row(w, "archived", "the PR's repository is archived; only with -include-archived")

	section(w, "PULL REQUEST STATE")
	prose(w, "Colour carries the state, and a hollow glyph means the pull request is still")
	prose(w, "a draft — a draft is not a fourth state but a flag on an open one, and a")
	prose(w, "draft that was closed is closed:")
	prose(w, "")
	for _, s := range []struct {
		pr   PullRequest
		what string
	}{
		{PullRequest{State: "OPEN"}, "open"},
		{PullRequest{State: "MERGED"}, "violet: merged"},
		{PullRequest{State: "CLOSED"}, "red: closed without merging"},
		{PullRequest{State: "OPEN", Draft: true}, "hollow: a draft, not offered for review yet"},
	} {
		icon, style := prStateIcon(s.pr)
		fmt.Fprintf(w, "  %s%s\n",
			style.Render(pad(icon+" repo #123", 22)), mutedStyle.Render(s.what))
	}
	prose(w, "")
	prose(w, "Every glyph devdash draws is plain Unicode one column wide, so no Nerd Font")
	prose(w, "is needed.")
	prose(w, "")
	prose(w, "Open pull requests are fetched, plus any merged in the last 30 days, so a")
	prose(w, "ticket still open keeps showing the PR that did the work. Closed without")
	prose(w, "merging is excluded as abandoned. A merged PR matching no active ticket is")
	prose(w, "dropped rather than listed — it is finished work. An explicit -pr-query is")
	prose(w, "run exactly as written, with no filtering.")

	section(w, "WHAT IT CALLS")
	fmt.Fprintf(w, "  %s\n", faintStyle.Render("JIRA, at $JIRA_URL"))
	for _, c := range [][2]string{
		{"GET  /rest/api/3/search/jql", "the ticket list, and the child counts"},
		{"GET  /rest/api/3/myself", "confirms auth when the list comes back empty"},
		{"GET  /rest/api/3/issue/{key}/transitions", "the statuses you may move to"},
		{"POST /rest/api/3/issue/{key}/transitions", "applies a status change (only on s)"},
	} {
		fmt.Fprintf(w, "    %s%s\n", normalStyle.Render(pad(c[0], 42)), mutedStyle.Render(c[1]))
	}

	fmt.Fprintf(w, "\n  %s\n", faintStyle.Render("GitHub, at api.github.com"))
	for _, c := range [][2]string{
		{"POST /graphql", "searches your open pull requests"},
		{"GET  /repos/{owner}/{name}", "confirms the repo name, following renames"},
	} {
		fmt.Fprintf(w, "    %s%s\n", normalStyle.Render(pad(c[0], 42)), mutedStyle.Render(c[1]))
	}

	fmt.Fprintf(w, "\n  %s\n", faintStyle.Render("Symphony, at the port in WORKFLOW.md"))
	fmt.Fprintf(w, "    %s%s\n",
		normalStyle.Render(pad("GET  /api/v1/state", 42)),
		mutedStyle.Render("which tickets it is working on"))

	fmt.Fprintf(w, "\n  %s\n", faintStyle.Render("On this machine"))
	for _, c := range [][2]string{
		{".git/config", "read directly to find the repo; no git subprocess"},
		{"pbcopy, wl-copy, xclip, xsel, clip", "the clipboard, on c"},
		{"open, xdg-open, rundll32", "your browser, on enter or p"},
	} {
		fmt.Fprintf(w, "    %s%s\n", normalStyle.Render(pad(c[0], 42)), mutedStyle.Render(c[1]))
	}
	fmt.Fprintf(w, "\n  %s\n",
		faintStyle.Render("Nothing is written anywhere except the clipboard, and JIRA when you press s."))

	section(w, "SCOPE")
	for _, line := range []string{
		"Run inside a git repository and the pull requests are narrowed to it;",
		"the header says so. Outside one, or with -all-repos, every repository is",
		"included. An explicit -pr-query always wins over both.",
	} {
		prose(w, line)
	}
	fmt.Fprintln(w)
}

// envStatus reports whether a required variable is present, without echoing any
// secret. GITHUB_TOKEN is satisfied by GH_TOKEN too.
func envStatus(name string) string {
	value := os.Getenv(name)
	if value == "" && name == "GITHUB_TOKEN" {
		if os.Getenv("GH_TOKEN") != "" {
			return okStyle.Render("set (GH_TOKEN)")
		}
	}
	if strings.TrimSpace(value) == "" {
		return badStyle.Render("missing")
	}
	return okStyle.Render("set")
}
