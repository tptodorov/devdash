package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestPRSearchQuery(t *testing.T) {
	tests := []struct {
		name      string
		explicit  string
		repo      string
		allRepos  bool
		wantQuery string
		wantScope string
	}{
		{
			name:      "inside a repo narrows the search and reports the scope",
			repo:      "acme/platform",
			wantQuery: defaultPRQuery + " repo:acme/platform",
			wantScope: "acme/platform",
		},
		{
			name:      "outside a repo falls back to every repository",
			repo:      "",
			wantQuery: defaultPRQuery,
			wantScope: "",
		},
		{
			name:      "all-repos overrides detection",
			repo:      "acme/platform",
			allRepos:  true,
			wantQuery: defaultPRQuery,
			wantScope: "",
		},
		{
			// An explicit query is the user's own; narrowing it would silently
			// change what they asked for.
			name:      "explicit query wins over detection",
			explicit:  "author:@me is:pr is:open org:someorg",
			repo:      "acme/platform",
			wantQuery: "author:@me is:pr is:open org:someorg",
			wantScope: "",
		},
		{
			name:      "explicit query wins over all-repos too",
			explicit:  "author:@me is:pr is:open repo:a/b",
			repo:      "acme/platform",
			allRepos:  true,
			wantQuery: "author:@me is:pr is:open repo:a/b",
			wantScope: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			query, scope := prSearchQuery(tc.explicit, tc.repo, tc.allRepos)
			if query != tc.wantQuery {
				t.Errorf("query = %q, want %q", query, tc.wantQuery)
			}
			if scope != tc.wantScope {
				t.Errorf("scope = %q, want %q", scope, tc.wantScope)
			}
		})
	}
}

// A narrowed list must never look like the whole picture, so the scope has to
// reach the header — as the repository's link where there is room for it.
func TestHeaderShowsRepoScope(t *testing.T) {
	newApp := func(width int) *app {
		a := &app{
			width:   width,
			tickets: []Ticket{{Key: "PROJ-1", Status: "To Do", Category: "To Do", Type: "Task"}},
			prs:     []PullRequest{{Repo: "platform", Number: 1}},
		}
		a.settle()
		return a
	}

	t.Run("unscoped names no repository", func(t *testing.T) {
		a := newApp(118)
		plain := ansi.Strip(a.headerView(a.layout()))
		if !strings.Contains(plain, "1 PRs") || strings.Contains(plain, "platform") {
			t.Errorf("header = %q, want no repository named", plain)
		}
	})

	// Widest form first, then progressively shorter ones as the terminal narrows.
	// The full URL is deliberately not among them: the name is a hyperlink either
	// way, so spelling out github.com/ spent the header on nothing.
	tests := []struct {
		name  string
		width int
		want  string
	}{
		{name: "wide shows owner/name", width: 118, want: "acme/platform"},
		{name: "medium still shows owner/name", width: 70, want: "acme/platform"},
		{name: "narrow falls back to the bare name", width: 64, want: "platform"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(tc.width)
			a.prScope = "acme/platform"
			a.prScopeURL = "https://github.com/acme/platform"

			got := a.headerView(a.layout())
			// Styling splits the gap and the name into separate spans, so compare
			// the text the user actually sees.
			if plain := ansi.Strip(got); !strings.Contains(plain, "  "+tc.want+"  ") {
				t.Errorf("header at %d cols = %q, want it to contain %q", tc.width, plain, tc.want)
			}
			// Whichever form is chosen, the line must still fit.
			if w := lipgloss.Width(got); w > a.layout().width {
				t.Errorf("header at %d cols renders %d wide", tc.width, w)
			}
		})
	}
}

// The repository name is a hyperlink, and turning links off must leave plain text.
func TestHeaderRepoLink(t *testing.T) {
	a := &app{
		width:      118,
		hyperlinks: true,
		prScope:    "acme/platform",
		prScopeURL: "https://github.com/acme/platform",
	}
	a.settle()

	if got := a.headerView(a.layout()); !strings.Contains(got, "\x1b]8;;https://github.com/acme/platform") {
		t.Errorf("header = %q, want an OSC 8 hyperlink", got)
	}

	a.hyperlinks = false
	got := a.headerView(a.layout())
	if strings.Contains(got, "\x1b]8;;") {
		t.Errorf("header = %q, want no hyperlink escapes with links disabled", got)
	}
	if !strings.Contains(ansi.Strip(got), "acme/platform") {
		t.Errorf("header = %q, want the repository still named as text", got)
	}
}

// The footer is a fixed set of reminders, so a narrow terminal must shed them
// rather than let the line run past the edge.
func TestFooterNeverOverflows(t *testing.T) {
	for _, width := range []int{20, 40, 60, 68, 79, 80, 100, 160} {
		a := &app{width: width}
		got := lipgloss.Width(a.footerView())
		if got > width {
			t.Errorf("footer at %d cols renders %d wide: %q", width, got, a.footerView())
		}
		// Whatever survives, quitting must stay discoverable.
		if width >= 20 && !strings.Contains(a.footerView(), "q quit") {
			t.Errorf("footer at %d cols dropped \"q quit\": %q", width, a.footerView())
		}
	}
}

func TestShortRepo(t *testing.T) {
	tests := map[string]string{
		"acme/platform": "platform",
		"platform":      "platform",
		"a/b/c":         "c",
		"":              "",
	}
	for in, want := range tests {
		if got := shortRepo(in); got != want {
			t.Errorf("shortRepo(%q) = %q, want %q", in, got, want)
		}
	}
}
