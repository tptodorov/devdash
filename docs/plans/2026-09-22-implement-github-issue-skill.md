# Implement-GitHub-Issue Skill Implementation Plan

**Goal:** A project skill, `/implement-github-issue <issue-link-or-number>`, that takes devdash from a GitHub issue to a merged-ready PR with no approval checkpoints.
**Scope:** One new skill file (`.claude/skills/implement-github-issue/SKILL.md`) and an `AGENTS.md` update noting it.
**Non-goals:** No branch/worktree creation (assumes it's already running in the right one). No interactive requirements-gathering with the user mid-run. No bespoke commit/push/PR logic — that's delegated to `no-mistakes`.
**Risks:** The skill's instructions must stay in sync with how `no-mistakes`'s own skill actually drives gates; if that skill's protocol changes, ours could give stale guidance. Low risk otherwise — it's a prompt file, not code.

### Files

- Create: `.claude/skills/implement-github-issue/SKILL.md`
- Modify: `AGENTS.md` (Local skills section)

### Task 1: Write the skill file

- Outcome: `.claude/skills/implement-github-issue/SKILL.md` exists with frontmatter (`name`, `description`, `user-invocable: true`) and a body covering, in order: resolving the issue argument (number or URL, via `gh issue view`), classifying bug vs. feature, implementing (TDD-first for bugs per this repo's `AGENTS.md`, tests-alongside for features, self-checking with `go build ./...` / `go test ./...`), shipping via `no-mistakes` (init-if-needed, commit referencing the issue, `no-mistakes axi run --intent "..." --yes` driven to a terminal outcome, with the `protected-path-refusal` exception), and reporting the outcome to the user.
- Steps:
  - Write the frontmatter, matching the style of `.claude/skills/use-modern-go/SKILL.md` and `~/.claude/skills/no-mistakes/SKILL.md`.
  - Write each section as concrete, imperative instructions with exact commands (`gh issue view`, `go build ./...`, `go test ./...`, `no-mistakes axi`, `no-mistakes init`, `no-mistakes axi run --intent "..." --yes`), not paraphrases.
- Verification:
  - Read the file back and confirm every decision from the design discussion is represented: autonomous end-to-end, no branch creation, TDD for bugs, `no-mistakes` for shipping, `--yes` with the `protected-path-refusal` carve-out.
- Dependencies: none

### Task 2: Document it in AGENTS.md

- Outcome: `AGENTS.md`'s "Local skills" section distinguishes hand-authored project skills (like `implement-github-issue`) from skills installed via the `skills` CLI and tracked in `skills-lock.json` (like `use-modern-go`), and lists `implement-github-issue` with a one-line description and its invocation form.
- Steps:
  - Add a short "Hand-authored" subsection (or equivalent) alongside the existing package-managed-skills content.
- Verification:
  - Read the file back; confirm it stays concise and doesn't duplicate the SKILL.md's own instructions.
- Dependencies: Task 1

### Execution

Two files, no code, no tests to fail-then-pass — dispatching per-task subagents would be pure ceremony here. I'll write both directly in this session.
