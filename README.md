# devdash

Your active JIRA tickets and the pull requests addressing them, correlated on
one page.

```
◈ DEVDASH  10 tickets · 3 PRs in https://github.com/acme/platform    ⟳ 10s · 16:57:59

▌ AWAITING CR ──────────────────────────────────────────────────────────────── 3
▌ ST    PROJ-17506   Migrate RAM live data plane stores to persisted…  →  platform #1105 ci◌ rev✓
  T     PROJ-17552   Mark the CI wait helper as manual-only         →  platform #1099 ci✓ rev?
  ST    PROJ-17538   Add account identifiers configured…    →  platform #1103 ci✓ rev✓

▌ IN PROGRESS ──────────────────────────────────────────────────────────────── 2
  T   4 PROJ-17453   Add a shared database registry…     ·  no PR
  E  11 PROJ-16617   the on-prem effort                                 ·  no PR

▌ PRS WITHOUT AN ACTIVE TICKET ─────────────────────────────────────────────── 1
  ⇢ sandbox #5 ci✗      feat: Monorepo template with clean architecture…
```

Tickets are grouped by status, most urgent first, and each group is colour
coded. Ticket keys and PR references are OSC 8 hyperlinks — ⌘-click them in
iTerm2, Ghostty, WezTerm, Kitty or any terminal that supports links.

## The type and children columns

The first column abbreviates the issue type; the second counts its sub-tickets.

```
  T     PROJ-17552    Mark the CI wait helper as manual-only
  ST    PROJ-17538    Add account identifiers…
  E  11 PROJ-16617    the on-prem effort
  T   4 PROJ-17453    Add a shared database registry
  I  14 TEAM-205732   Unified Core Platform
```

**Type.** The initial of each word in the type's name, capitalised: `Task` → `T`,
`Sub-task` → `ST`, `New Feature` → `NF`, `Change Request` → `CR`, `Data Analytics
Task` → `DAT`. Words split on any non-alphanumeric character, so hyphens, spaces,
slashes and underscores all count. Nothing is hard-coded, so it works on any
project's workflow without being taught its types — verified against all 143
issue types in this JIRA instance. Codes are capped at 4 characters.

Because the rule is purely derived, two types in one view can share a code
(`Story` and `Security` both give `S`). The `?` help lists **TYPES IN VIEW**,
generated from the tickets actually loaded, so any collision that really occurs
is spelled out rather than hidden.

**Children.** The number of issues whose parent is this ticket, any assignee,
shown in the accent colour. Blank when there are none, and the column disappears
entirely when nothing in view has children.

Counts come from the child side, with a `parent in (…)` query. An issue's own
`subtasks` field cannot answer this — it stays empty for epic and initiative
children, reporting `0` for an epic that actually has 11. That costs one extra
request per refresh (~0.6s here), skipping sub-tasks since JIRA forbids nesting
them. If it fails the tickets still render, with a `~ jira: child counts
unavailable` warning.

All three of these columns — type, children and the ticket key — size themselves
to the data, so a long project key such as `LONGPROJ-308` widens the key column
instead of pushing the summary out of alignment.

## Requirements

- `JIRA_URL`, `JIRA_USERNAME` and `JIRA_API_TOKEN` in the environment
- `GITHUB_TOKEN` (or `GH_TOKEN`) holding a token with `repo` scope

Both APIs are called directly over HTTPS; `devdash` shells out to no external
binaries for data. If you use the GitHub CLI, a token is one command away:

```bash
export GITHUB_TOKEN=$(gh auth token)
```

## Usage

```bash
devdash help                # requirements, keys, and every call it makes
devdash                     # interactive, auto-refreshing every 10s
devdash -refresh 30s        # slower refresh
devdash -refresh 0          # no auto-refresh; press r to refresh manually
devdash -once               # print one snapshot and exit
devdash -once -no-links     # snapshot without hyperlink escapes, good for piping
```

### Keys

| Key              | Action                          |
| ---------------- | ------------------------------- |
| `↑`/`k`, `↓`/`j` | move between rows               |
| `g` / `G`        | jump to first / last row         |
| `enter`, `o`     | open the selected ticket        |
| `p`              | open the selected row's PR      |
| `c`              | copy a shareable snippet        |
| `s`              | change the ticket's status      |
| `r`              | refresh now                     |
| `a`              | toggle automatic refresh        |
| `?`              | help and legend                 |
| `q`              | quit                            |

### Flags and environment

| Flag         | Environment          | Default                        |
| ------------ | -------------------- | ------------------------------ |
| `-refresh`   | `DEVDASH_REFRESH`  | `10s` (`0` disables; min `2s`) |
| `-jql`       | `DEVDASH_JQL`      | assigned to you, not Done      |
| `-pr-query`  | `DEVDASH_PR_QUERY` | `author:@me is:pr is:open`, scoped to this repo |
| `-all-repos` | —                    | scoped to the current repo     |
| `-no-links`  | —                    | hyperlinks on                  |
| `-once`      | —                    | interactive                    |
| `-include-archived` | —             | archived-repo PRs hidden       |

Scope it to one project or repo by overriding the queries:

```bash
devdash -jql 'assignee = currentUser() AND project = MOD AND statusCategory != Done ORDER BY updated DESC'
devdash -pr-query 'author:@me is:pr is:open org:acme'
```

## Pull request badges

Each pull request shows GitHub's own status, shortened:

| Badge | Meaning |
| ----- | ------- |
| `ci✓` | checks passing |
| `ci✗` | checks failing |
| `ci◌` | checks still running |
| `rev✓` | approved; `rev✓3` means three approving reviews |
| `rev±` | changes requested |
| `rev?` | awaiting review |
| `archived` | the PR's repository is archived (only with `-include-archived`) |

The approval badge comes from GitHub's review decision **and** the count of
approving reviews. Both are needed: GitHub reports no decision at all when the
base branch requires no review, so a genuinely approved PR would otherwise show
nothing.

The PR's own state is carried by the colour of its reference:

| Colour | State |
| ------ | ----- |
| normal text | open |
| grey | draft |
| violet | merged |
| red | closed without merging |

Merged has its own colour rather than sharing red with closed: a merged PR is a
success and should not read as an alarm. Closed and merged outrank draft, so a
draft that was closed shows as closed.

### Which pull requests are fetched

Open pull requests, plus any merged in the last 30 days. **Closed without
merging is excluded** — that is abandoned work, not something a dashboard of live
work should carry.

Merged PRs are fetched so a ticket that is still open keeps showing the PR that
did the work: `PROJ-17506` was `In Review` while its `#1105` had already merged,
and without this it showed `no PR`.

A merged PR matching no active ticket is dropped rather than listed. It is
finished work, and there are hundreds of them — listing them would bury the open
PRs that still need attention. Open PRs with no ticket are always listed, since
they still need something from you.

GitHub has no qualifier for "open or merged": `is:open` and `is:merged` are
disjoint, and `-is:unmerged` excludes open entirely. So two searches are used,
travelling in a single GraphQL request.

An explicit `-pr-query` is honoured exactly as written, with no second search and
no state filtering — ask for closed PRs and you get them.

## Symphony

If a local [Symphony](https://github.com/openai/symphony) instance is running,
the tickets it currently has in hand are marked in a column of their own:

```
  ST    PROJ-17538   Add account identifiers configured…  →  platform #1103 ci✓ rev✓   ♪
  T     PROJ-17552   Mark the CI wait helper as manual-only       →  platform #1099 ci✓ rev?
```

The marker sits in its own column at the right-hand edge, after the pull request.

| Marker | Meaning |
| ------ | ------- |
| `♪` | Symphony is working on the ticket |
| `!` | paused waiting for operator input or approval |
| `↻` | waiting for the next retry window |

The column disappears entirely when Symphony has no sessions, so it costs nothing
when you are not using it. Blocked has its own glyph rather than only a colour,
because it is the state that needs *you*.

### Discovery

The instance is located from the `server` block of `WORKFLOW.md`'s YAML front
matter, searching upward from the working directory:

```yaml
---
server:
  port: 10000
---
```

`host` is honoured if present, defaulting to `127.0.0.1`. The port is read from
the file rather than from `.symphony/symphony.pid`, because that PID file goes
stale while the port stays authoritative — in this repo it pointed at a dead
process while Symphony was alive on the configured port.

Discovery and the API call are repeated on **every refresh**, since Symphony is
started and stopped independently of this tool and its port travels with the
working tree. It queries `GET /api/v1/state`.

If Symphony is not running, or there is no `WORKFLOW.md`, nothing is shown and no
error is reported — that is the ordinary case, and a banner would cry wolf on most
refreshes.

## Sharing a ticket or PR

Press `c` to copy the selected row as text you can paste to whoever should look
at it: what it is, then every link they need.

```
PROJ-17552 — Mark the CI wait helper as manual-only
https://your-org.atlassian.net/browse/PROJ-17552
https://github.com/acme/platform/pull/1099
```

There is no preamble, so it reads fine whether you are asking for a review or
just pointing at something. The links are bare, which is what makes them unfurl
when pasted into Slack.

What lands on the clipboard follows the row:

- a ticket with pull requests gets the ticket link and **every** PR link, not
  just the first
- a ticket with no PR gets only the ticket link, with no empty `PR:` line
- a pull request with no ticket is labelled by repo and number, with no JIRA link

Nothing that goes stale is included — no status, no CI state.

Copying uses `pbcopy` on macOS, `wl-copy`/`xclip`/`xsel` on Linux and `clip` on
Windows, falling back to the OSC 52 terminal escape when none is present, which
is also what makes it work over SSH.

## Changing status

Press `s` on a ticket to move it. The choices come from JIRA's transitions API
for that specific issue, so they are exactly what your workflow permits from its
current state — and they differ per project (MOD offers 10, RED 14).

```
╭─ PROJ-17552 ───────────────────────────────────────────╮
│ Mark the CI wait helper as manual-only             │
│ now: Awaiting CR                                      │
│                                                       │
│ ▸ 1 To Do                                             │
│   2 In Progress                                       │
│   3 Awaiting Verification  via "Done"                 │
│   4 Closed  +1 step                                   │
│   8 Awaiting CR  (current)                            │
│                                                       │
│ ↑↓ choose   ⏎ select   esc cancel   o open            │
╰───────────────────────────────────────────────────────╯
```

Navigate with `↑↓`/`jk`, `⏎` to select, `1`–`9` to jump straight to a choice,
`esc` to cancel, `o` to open the ticket in a browser. Auto-refresh pauses while
the panel is open so rows cannot move under you.

Three details worth knowing:

- **The destination status is shown, not the transition's name.** They are not
  always the same: in MOD, the transition called *Done* actually moves an issue
  to *Awaiting Verification*, and in RED *COMMITTED* moves to *ROLL OUT*. Where
  they differ the transition's own name is shown as `via "Done"`.
- **`+1 step`** marks a transition that needs a value first. Closing a MOD issue
  requires a resolution, so you get a second prompt listing the 12 allowed
  resolutions.
- **`(current)`** marks the transition that would land the issue back in the
  status it is already in.

If a transition requires a field this tool cannot offer a list for — a free-text
comment, say — it says so and points you at `o` to do it in the browser, rather
than sending a request JIRA will reject.

## How tickets and PRs are matched

A PR is attached to a ticket when a JIRA key (`PROJ-17506`, `TEAM-205732`) appears
in its **title**, falling back to its **branch name**. PRs whose key matches no
active ticket — or that have no key at all — are listed under *PRS WITHOUT AN
ACTIVE TICKET* rather than hidden.

## Repository scope

Run from inside a repository and the PR list is narrowed to it. The header names
that repository, hyperlinked, so a narrowed list is never mistaken for the whole
picture. The link shortens to `owner/name` and then `name` on narrower
terminals:

```
◈ DEVDASH  10 tickets · 3 PRs in https://github.com/acme/platform
```

Outside a repository — or with `-all-repos` — every repo you have open PRs in is
included and the header just reads `4 PRs`. An explicit `-pr-query` always wins
over scoping, since narrowing a query you wrote yourself would silently change
what you asked for.

The owner and name are read straight out of `.git/config` — no `git` subprocess —
preferring `origin`, then `upstream`, then any remote, and following a worktree's
`commondir` to the main repository's config.

That name is then confirmed against the API, because a renamed repository keeps
its old URL in the remote and GitHub's search does **not** follow renames:
searching `repo:acme/oldname` matches nothing even though that remote
resolves fine. The REST endpoint answers `301` for a rename and Go follows it, so
a stale remote still produces the name search will match. If that confirmation
fails, scoping is skipped and every repository is shown — showing too much is the
safer failure. Resolution happens once at startup, not per refresh.

## Archived repositories

Pull requests in archived repositories are hidden. GitHub keeps returning them
from search indefinitely, but they cannot be merged, so they are noise on a
devdash of live work — and worse, one can attach itself to a live ticket and
make a stale PR look actionable.

Pass `-include-archived` to bring them back; they are then tagged `archived`.

## Notes

- The two sources are fetched independently: if JIRA fails, your PRs still
  render, and vice versa. Failures show as a banner and the last good data is
  kept.
- JIRA answers an unauthenticated search with `200` and an empty list, so an
  expired token would otherwise look like an empty backlog. When the result is
  empty the tool verifies identity via `/myself` and reports auth failures
  explicitly.
- The refresh interval is measured from the end of the previous fetch, so the
  real cadence is the interval plus however long a fetch takes.
