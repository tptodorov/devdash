package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localRepo reports the GitHub owner and repository recorded in the git
// repository containing dir. It reads .git/config directly rather than shelling
// out, so the tool depends on no external binaries.
func localRepo(dir string) (owner, name string, err error) {
	configPath, err := gitConfigPath(dir)
	if err != nil {
		return "", "", err
	}

	remotes, err := parseRemotes(configPath)
	if err != nil {
		return "", "", err
	}
	if len(remotes) == 0 {
		return "", "", fmt.Errorf("no git remotes configured")
	}

	// Prefer origin, then upstream, then whatever is there, so a repository with
	// only a differently-named remote still resolves.
	for _, preferred := range []string{"origin", "upstream"} {
		if u, ok := remotes[preferred]; ok {
			return parseGitHubURL(u)
		}
	}
	for _, u := range remotes {
		return parseGitHubURL(u)
	}
	return "", "", fmt.Errorf("no usable git remote")
}

// gitConfigPath walks up from dir to find the repository and returns the path of
// the config file that holds its remotes. Worktrees keep their remotes in the
// main repository's config, which commondir points at.
func gitConfigPath(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		switch {
		case err == nil && info.IsDir():
			return gitConfigIn(candidate)
		case err == nil:
			// A .git file points at the real git directory, as in a worktree.
			data, readErr := os.ReadFile(candidate)
			if readErr != nil {
				return "", readErr
			}
			target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
			if target == "" {
				return "", fmt.Errorf("%s does not name a git directory", candidate)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return gitConfigIn(target)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a git repository")
		}
		dir = parent
	}
}

// gitConfigIn resolves a git directory to the config file that owns its remotes,
// following commondir out of a worktree into the main repository.
func gitConfigIn(gitDir string) (string, error) {
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			gitDir = filepath.Clean(common)
		}
	}
	return filepath.Join(gitDir, "config"), nil
}

// parseRemotes pulls the remote URLs out of a git config file. The format is
// simple enough that a full INI parser would be overkill.
func parseRemotes(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	remotes := map[string]string{}
	section := ""

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = ""
			header := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if fields := strings.Fields(header); len(fields) == 2 && fields[0] == "remote" {
				section = strings.Trim(fields[1], `"`)
			}
			continue
		}

		if section == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) != "url" {
			continue
		}
		if v := strings.TrimSpace(value); v != "" {
			remotes[section] = v
		}
	}
	return remotes, scanner.Err()
}

// parseGitHubURL extracts owner and repository from any of the URL forms git
// records for a GitHub remote.
func parseGitHubURL(raw string) (owner, name string, err error) {
	s := strings.TrimSpace(raw)

	// scp-like form: git@github.com:owner/repo.git
	if !strings.Contains(s, "://") {
		host, path, found := strings.Cut(s, ":")
		if !found {
			return "", "", fmt.Errorf("cannot read remote %q", raw)
		}
		if _, h, ok := strings.Cut(host, "@"); ok {
			host = h
		}
		if !isGitHubHost(host) {
			return "", "", fmt.Errorf("remote %q is not on github.com", raw)
		}
		return splitOwnerRepo(path, raw)
	}

	// URL form: https://github.com/owner/repo.git, ssh://git@github.com/...
	rest := s[strings.Index(s, "://")+3:]
	hostPart, path, found := strings.Cut(rest, "/")
	if !found {
		return "", "", fmt.Errorf("cannot read remote %q", raw)
	}
	if _, h, ok := strings.Cut(hostPart, "@"); ok {
		hostPart = h
	}
	if h, _, ok := strings.Cut(hostPart, ":"); ok {
		hostPart = h // strip any port
	}
	if !isGitHubHost(hostPart) {
		return "", "", fmt.Errorf("remote %q is not on github.com", raw)
	}
	return splitOwnerRepo(path, raw)
}

func isGitHubHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "github.com" || host == "www.github.com"
}

func splitOwnerRepo(path, raw string) (owner, name string, err error) {
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")

	owner, name, found := strings.Cut(path, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("cannot read owner and repository from %q", raw)
	}
	return owner, name, nil
}

// DetectRepo resolves the repository to scope pull requests to. The name comes
// from the local git config, then goes through the API so a stale remote left by
// a rename still yields the name GitHub search will match.
func DetectRepo(ctx context.Context, gh *githubClient, dir string) (string, error) {
	owner, name, err := localRepo(dir)
	if err != nil {
		return "", err
	}
	if gh == nil {
		return owner + "/" + name, nil
	}

	canonical, err := gh.CanonicalRepo(ctx, owner, name)
	if err != nil {
		// Scoping to an unverified name risks searching a name that matches
		// nothing, which looks like having no open PRs. Showing everything is
		// the safer failure.
		return "", fmt.Errorf("could not confirm %s/%s: %w", owner, name, err)
	}
	return canonical, nil
}
