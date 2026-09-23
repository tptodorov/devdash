---
name: implement-github-issue
description: Implement a GitHub issue end-to-end, from reading it to a shipped PR, with no approval checkpoints. Use when asked to implement, fix, or work a GitHub issue given as a link or a bare number.
---

# Implement GitHub Issue

Take a GitHub issue from "open" to a validated, PR-ready change with no
back-and-forth. This workflow assumes it is already running in the right
branch and working copy — it never creates or switches either. It requires
only `gh` and `no-mistakes` on the `PATH`, and works for any coding agent
that can run shell commands.

## Parameters

- `issue` (required): the GitHub issue to implement, as either a full issue
  URL (`https://github.com/<owner>/<repo>/issues/<N>`) or a bare issue
  number. A bare number resolves against the repository in the current
  working copy.

## 1. Resolve the issue

If `issue` is a bare number, use the current repo (`gh repo view` infers it
from the git remote). If it's a full URL, extract `owner/repo` and the
number from it.

```sh
gh issue view <N> [--repo <owner>/<repo>] --json number,title,body,labels,url,comments
```

Keep the `url` field — it's the canonical link to the issue, and every later
reference to this issue (commit message, `no-mistakes` intent, final report)
should use that link rather than a bare `#<N>`, since `#<N>` shorthand only
resolves correctly within its own repo's context.

Fetch comments too, not just the issue body — clarifications, proposed
approaches, and scope changes often live in the discussion rather than the
original description.

If `gh` can't find the issue, stop and report the error — do not guess at
what the issue might have meant.

## 2. Understand and classify

Read the title, body, labels, and comments. Classify the issue as a **bug fix** or a
**feature**: check labels first (`bug`, `enhancement`, etc.), then fall back
to keywords in the title/body.

Form your own implementation approach and proceed — do not open an
interactive requirements conversation with the user. The only reason to stop
here is if the issue body is too sparse to act on at all (e.g. a title with
no actionable detail and no way to infer intent from the codebase); in that
case, report what's missing instead of guessing.

## 3. Implement

Follow this repo's `AGENTS.md`:

- **Bug fix issues:** first write a test that reproduces the bug and confirm
  it fails, then fix the code until it passes. Never skip straight to the
  fix.
- **Feature issues:** implement with tests alongside, following this repo's
  existing per-file `_test.go` convention (e.g. a change to `app.go` gets
  covering tests in `app_test.go` or the relevant `_test.go` file).

Self-check before moving on, the same way this repo's Stop hook does:

```sh
go build ./...
go test ./...
```

Fix anything that fails here yourself before handing off to `no-mistakes` —
don't rely on its review/test steps to catch build breaks you already know
about.

## 4. Validate, commit, and ship via no-mistakes

This step delegates to the already-configured `no-mistakes` pipeline
(review, test, lint, document, push, PR, CI) rather than reimplementing any
of that. Follow its own gate-driving protocol — its `SKILL.md` if your agent
loads skills, otherwise `no-mistakes axi run --help`; the points specific to
this workflow are:

1. **Ensure the repo is initialized:**
   ```sh
   no-mistakes axi
   ```
   If it reports the repo isn't initialized, run `no-mistakes init` once
   (non-interactive for a normal repo with an `origin` remote).

2. **Commit** the implementation on the current branch, with a message that
   references the issue by its full URL, so the PR body that `no-mistakes`
   generates carries a working link and closes the issue on merge, e.g.:
   ```
   fix: <summary>

   Fixes <issue-url>
   ```

3. **Drive the pipeline to a terminal outcome:**
   ```sh
   no-mistakes axi run --intent "<issue title>: <one-paragraph summary of what changed and why> — Fixes <issue-url>" --yes
   ```
   Pass `--yes`: this skill is meant to run fully unattended, so treat that
   as standing consent for `no-mistakes` to resolve gates — including
   `ask-user` findings — on its own. The one exception `--yes` does not
   cover is a `protected-path-refusal` gate, which always requires an
   explicit human response even under `--yes`. If one comes up, stop and
   relay it to the user verbatim rather than working around it.
   Loop on `axi respond` exactly as the no-mistakes skill describes until you
   reach an `outcome:`.

## 5. Report

- On `checks-passed` or `passed`: summarize what was implemented and give
  both the issue link and the PR link.
- On `failed` or `cancelled`: explain what blocked it and what you did (or
  still need to do) about it.
