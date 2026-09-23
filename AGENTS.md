# Agent Instructions

## Local skills

This repo has agent skills under `.claude/skills/`, of two kinds:

- **Package-managed** — installed by the [`skills`](https://github.com/vercel-labs/skills)
  CLI and tracked in `skills-lock.json` (source, path, content hash per skill).
  Update or reinstall these with the `skills` CLI; don't hand-edit them.
- **Hand-authored** — written directly in this repo, not tracked in
  `skills-lock.json`. Edit these like any other file.

Installed (package-managed):

- **use-modern-go** (from `JetBrains/go-modern-guidelines`) — apply the Modern
  Go Guidelines whenever writing, modifying, fixing, or refactoring Go code.

Installed (hand-authored):

- **implement-github-issue** — take a GitHub issue (link or bare number) end
  to end to a shipped PR, with no approval checkpoints.

### Updating skills

```sh
skills update            # update all project skills to latest
skills update use-modern-go   # update one skill
```

### Restoring skills on a fresh clone/worktree

```sh
skills experimental_install   # installs from skills-lock.json
```

### Adding a new skill

```sh
skills add <owner/repo> --skill <skill-name> --agent claude-code -y
```

This writes the skill into `.claude/skills/` and records it in
`skills-lock.json`. Commit both.
