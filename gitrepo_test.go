package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGitHubURL(t *testing.T) {
	tests := []struct {
		raw       string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		// The scp-like form git writes for SSH remotes.
		{raw: "git@github.com:acme/platform.git", wantOwner: "acme", wantName: "platform"},
		{raw: "git@github.com:acme/platform", wantOwner: "acme", wantName: "platform"},
		// HTTPS, with and without the .git suffix or a trailing slash.
		{raw: "https://github.com/acme/platform.git", wantOwner: "acme", wantName: "platform"},
		{raw: "https://github.com/acme/platform", wantOwner: "acme", wantName: "platform"},
		{raw: "https://github.com/acme/platform/", wantOwner: "acme", wantName: "platform"},
		// Credentials embedded in the URL, and the ssh:// scheme.
		{raw: "https://user@github.com/acme/platform.git", wantOwner: "acme", wantName: "platform"},
		{raw: "ssh://git@github.com/acme/platform.git", wantOwner: "acme", wantName: "platform"},
		{raw: "ssh://git@github.com:22/acme/platform.git", wantOwner: "acme", wantName: "platform"},
		{raw: "git://github.com/acme/platform.git", wantOwner: "acme", wantName: "platform"},
		// A repository name containing a dot must not lose part of itself.
		{raw: "git@github.com:owner/repo.with.dots.git", wantOwner: "owner", wantName: "repo.with.dots"},
		// Whitespace as git may leave it.
		{raw: "  git@github.com:acme/platform.git  ", wantOwner: "acme", wantName: "platform"},
		// Anything not on github.com cannot be searched by this tool.
		{raw: "git@gitlab.com:owner/repo.git", wantErr: true},
		{raw: "https://git.example.com/owner/repo.git", wantErr: true},
		{raw: "https://github.com/owner", wantErr: true},
		{raw: "not a url", wantErr: true},
		{raw: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			owner, name, err := parseGitHubURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseGitHubURL(%q) = %q/%q, want an error", tc.raw, owner, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGitHubURL(%q) error = %v", tc.raw, err)
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Errorf("parseGitHubURL(%q) = %q/%q, want %q/%q",
					tc.raw, owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func writeRepo(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLocalRepoPrefersOrigin(t *testing.T) {
	// Mirrors the real shape of this repository's config, including the extra
	// remotes its worktree tooling leaves behind.
	root := writeRepo(t, `
[core]
	repositoryformatversion = 0
[remote "paseo-pr-821"]
	url = https://github.com/someone/fork.git
	fetch = +refs/heads/*:refs/remotes/paseo-pr-821/*
[remote "origin"]
	url = git@github.com:acme/platform.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "main"]
	remote = origin
`)

	owner, name, err := localRepo(root)
	if err != nil {
		t.Fatalf("localRepo() error = %v", err)
	}
	if owner != "acme" || name != "platform" {
		t.Errorf("localRepo() = %q/%q, want acme/platform", owner, name)
	}
}

func TestLocalRepoFallsBackWhenNoOrigin(t *testing.T) {
	root := writeRepo(t, `
[remote "upstream"]
	url = https://github.com/acme/platform.git
`)
	owner, name, err := localRepo(root)
	if err != nil {
		t.Fatalf("localRepo() error = %v", err)
	}
	if owner != "acme" || name != "platform" {
		t.Errorf("localRepo() = %q/%q, want acme/platform", owner, name)
	}
}

func TestLocalRepoFromSubdirectory(t *testing.T) {
	root := writeRepo(t, "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	owner, name, err := localRepo(deep)
	if err != nil {
		t.Fatalf("localRepo() error = %v", err)
	}
	if owner != "o" || name != "r" {
		t.Errorf("localRepo() = %q/%q, want o/r", owner, name)
	}
}

// A worktree's .git is a file pointing at a git directory whose remotes live in
// the main repository's config, reached through commondir.
func TestLocalRepoInWorktree(t *testing.T) {
	main := writeRepo(t, "[remote \"origin\"]\n\turl = git@github.com:acme/platform.git\n")
	mainGit := filepath.Join(main, ".git")

	wtGit := filepath.Join(mainGit, "worktrees", "feature")
	if err := os.MkdirAll(wtGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := filepath.Join(t.TempDir(), "feature")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	owner, name, err := localRepo(worktree)
	if err != nil {
		t.Fatalf("localRepo() error = %v", err)
	}
	if owner != "acme" || name != "platform" {
		t.Errorf("localRepo() = %q/%q, want acme/platform", owner, name)
	}
}

func TestLocalRepoErrors(t *testing.T) {
	t.Run("outside a repository", func(t *testing.T) {
		if _, _, err := localRepo(t.TempDir()); err == nil {
			t.Error("expected an error outside a git repository")
		}
	})

	t.Run("repository with no remotes", func(t *testing.T) {
		root := writeRepo(t, "[core]\n\tbare = false\n")
		if _, _, err := localRepo(root); err == nil {
			t.Error("expected an error when no remote is configured")
		}
	})
}

// Comments and section-like text must not be mistaken for remotes.
func TestParseRemotesIgnoresNoise(t *testing.T) {
	root := writeRepo(t, `
# [remote "commented"]
#	url = git@github.com:nope/nope.git
[core]
	url = git@github.com:wrong/section.git
[remote "origin"]
	; a comment
	pushurl = git@github.com:other/pushtarget.git
	url = git@github.com:right/repo.git
`)

	remotes, err := parseRemotes(filepath.Join(root, ".git", "config"))
	if err != nil {
		t.Fatalf("parseRemotes() error = %v", err)
	}
	if len(remotes) != 1 {
		t.Fatalf("remotes = %v, want just origin", remotes)
	}
	if got := remotes["origin"]; got != "git@github.com:right/repo.git" {
		t.Errorf("origin = %q, want the url key, not pushurl", got)
	}
}
