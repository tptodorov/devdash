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
		{"g / G", "jump to the first / last row"},
		{"enter, o", "open the selected ticket in a browser"},
		{"p", "open the selected row's pull request"},
		{"c", "copy a shareable snippet: title, ticket link, every PR link"},
		{"s", "change the selected ticket's status"},
		{"r", "refresh now"},
		{"a", "pause or resume the automatic refresh"},
		{"?", "keys, columns and the issue types currently on screen"},
		{"q, esc, ctrl+c", "quit"},
	} {
		row(w, k[0], k[1])
	}

	section(w, "COLUMNS")
	row(w, "type", "initials of the issue type's words: Sub-task ST, New Feature NF")
	row(w, "children", "sub-tickets whose parent is this ticket, blank when none")
	row(w, "ticket", "key and summary, hyperlinked to JIRA")
	row(w, "pull request", "repo and number, coloured by state, with ci and review badges")

	section(w, "SYMPHONY")
	prose(w, "If a local Symphony instance is running, tickets it has in hand are marked")
	prose(w, "in a column of their own, which disappears when it has no sessions:")
	prose(w, "")
	for _, s := range []struct{ state, what string }{
		{SymphonyRunning, "Symphony is working on it"},
		{SymphonyBlocked, "paused waiting for operator input or approval"},
		{SymphonyRetrying, "waiting for the next retry window"},
	} {
		marker, style := symphonyMarker(s.state)
		fmt.Fprintf(w, "  %s%s\n", style.Render(pad(marker, 22)), mutedStyle.Render(s.what))
	}
	prose(w, "")
	prose(w, "The instance is found from the server block of WORKFLOW.md's front matter,")
	prose(w, "rediscovered and queried on every refresh, since Symphony starts and stops")
	prose(w, "independently of this tool. If it is not running, nothing is shown and no")
	prose(w, "error is reported — that is the ordinary case.")

	section(w, "PULL REQUEST BADGES")
	row(w, "ci✓", "checks passing")
	row(w, "ci✗", "checks failing")
	row(w, "ci◌", "checks still running")
	row(w, "rev✓", "approved; rev✓3 means three approving reviews")
	row(w, "rev±", "changes requested")
	row(w, "rev?", "awaiting review")
	row(w, "archived", "the PR's repository is archived; only with -include-archived")

	section(w, "PULL REQUEST STATE")
	prose(w, "The state is carried by the colour of the reference itself:")
	prose(w, "")
	for _, s := range []struct {
		pr   PullRequest
		what string
	}{
		{PullRequest{State: "OPEN"}, "open"},
		{PullRequest{State: "OPEN", Draft: true}, "draft"},
		{PullRequest{State: "MERGED"}, "merged"},
		{PullRequest{State: "CLOSED"}, "closed without merging"},
	} {
		fmt.Fprintf(w, "  %s%s\n",
			prStateStyle(s.pr).Render(pad("repo #123", 22)), mutedStyle.Render(s.what))
	}
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
