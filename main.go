// Command devdash shows the JIRA tickets assigned to you alongside the pull
// requests that address them, correlated on one page.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	defaultPRQuery = "author:@me is:pr is:open"
	// mergedWindow bounds how far back merged pull requests are fetched.
	mergedWindow = 30 * 24 * time.Hour
	// minRefresh keeps an over-eager interval from hammering either API.
	minRefresh = 2 * time.Second
	// symphonyTimeout is generous relative to the usual ~100ms: Symphony snapshots
	// its orchestrator to answer, which was measured at 6.8s under load, and a
	// timeout would drop the markers for that refresh.
	symphonyTimeout = 8 * time.Second
)

func main() {
	period := flag.Duration("refresh", envDuration("DEVDASH_REFRESH", 10*time.Second),
		"auto-refresh interval, e.g. 10s or 2m; 0 disables auto-refresh")
	jql := flag.String("jql", envOr("DEVDASH_JQL", DefaultJQL),
		"JQL query selecting the tickets to show")
	prQuery := flag.String("pr-query", os.Getenv("DEVDASH_PR_QUERY"),
		"GitHub search query selecting the pull requests to show; overrides repo scoping")
	allRepos := flag.Bool("all-repos", false,
		"show pull requests from every repository, not just the one in this directory")
	noLinks := flag.Bool("no-links", false,
		"render plain text instead of OSC 8 terminal hyperlinks")
	once := flag.Bool("once", false,
		"print a single snapshot and exit instead of running the interactive UI")
	includeArchived := flag.Bool("include-archived", false,
		"keep pull requests whose repository is archived (hidden by default)")

	flag.Usage = func() { writeHelp(os.Stderr) }

	// "devdash help" is a subcommand, so it is handled before parsing. The flags
	// above are already registered, so the help text can list their real
	// defaults.
	if len(os.Args) > 1 && helpTopics[os.Args[1]] {
		writeHelp(os.Stdout)
		return
	}
	flag.Parse()

	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "devdash: unexpected argument %q; try \"devdash help\"\n", flag.Arg(0))
		os.Exit(2)
	}

	interval := *period
	if interval > 0 && interval < minRefresh {
		interval = minRefresh
	}

	a := newApp(*jql, "", interval, !*noLinks, *includeArchived)

	// Resolving the repository costs one round trip, so do it once at startup
	// rather than on every refresh.
	repo := ""
	if *prQuery == "" && !*allRepos {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cwd, _ := os.Getwd()
		repo, _ = DetectRepo(ctx, a.gh, cwd) // not in a repo: fall back to all
		cancel()
	}
	a.ghQuery, a.prScope = prSearchQuery(*prQuery, repo, *allRepos)
	a.explicitQuery = *prQuery != ""
	if a.prScope != "" {
		a.prScopeURL = "https://github.com/" + a.prScope
	}

	if *once {
		if err := a.snapshot(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "devdash:", err)
			os.Exit(1)
		}
		return
	}

	if _, err := tea.NewProgram(a, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "devdash:", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
