# devdash

Your assigned JIRA and Linear tickets and the pull requests addressing them, on
one page, in your terminal.

[![CI](https://github.com/tptodorov/devdash/actions/workflows/ci.yml/badge.svg)](https://github.com/tptodorov/devdash/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![devdash](docs/screenshot.png)

## Try it in one command

No install, no credentials, nothing to configure — this renders the screenshot
above with sample data:

```bash
go run github.com/tptodorov/devdash@latest -demo
```

Once you have the environment variables from [Configure](#configure), the
same command shows your real tickets:

```bash
go run github.com/tptodorov/devdash@latest
```

`go run` compiles and runs it in one step, so it is the quickest way to try devdash
without adding a binary to your `PATH`. If you decide to keep it, see
[Install](#install).

## Why

The state of your own work is spread across three places: your issue tracker (JIRA,
Linear, or both) says what is assigned to you, GitHub says which pull requests
exist, and neither knows about the other. Reconciling them is a tab-switching
exercise you repeat all day.

devdash puts them on one line each. It also closes three gaps that cost real time:

- **A merged PR vanishes from `is:open` searches**, so a ticket still in review
  looks like it has no PR at all. devdash fetches recently merged PRs too.
- **Approvals can be invisible.** GitHub reports no review decision when the base
  branch requires none, so an approved PR looks unreviewed. devdash counts
  approving reviews as well as reading the decision.
- **Changing a ticket's status meant leaving the terminal.** Press `s` (JIRA
  tickets; Linear is read-only for now).

## Requirements

- **Go 1.25+** to install
- **A JIRA API token**, **a Linear API key**, or both — whichever trackers
  have credentials set are shown together. Create a JIRA token at
  [id.atlassian.com](https://id.atlassian.com/manage-profile/security/api-tokens),
  a Linear key under linear.app > Settings > Security & access
- **A GitHub token** with `repo` scope

Every glyph devdash draws is plain Unicode, one column wide, so no special font
is needed.

## Install

```bash
go install github.com/tptodorov/devdash@latest
```

That puts `devdash` in `$(go env GOPATH)/bin`. Make sure that is on your `PATH`,
or set `GOBIN` first:

```bash
GOBIN="$HOME/.local/bin" go install github.com/tptodorov/devdash@latest
```

## Configure

Set up JIRA, Linear, or both — devdash activates whichever tracker has its
keys set, and shows both together if both are set. Add them to your shell
profile:

```bash
export JIRA_URL='https://your-org.atlassian.net'
export JIRA_USERNAME='you@your-org.com'      # your Atlassian account email
export JIRA_API_TOKEN='...'                  # from id.atlassian.com

export LINEAR_API_KEY='...'                  # linear.app > Settings > Security & access

export GITHUB_TOKEN='...'                    # a token with repo scope
```

If you already use the GitHub CLI, the token is one command away:

```bash
export GITHUB_TOKEN=$(gh auth token)
```

Check what devdash can see:

```bash
devdash help          # the REQUIREMENTS section reports each variable as set or missing
```

## Quick start

```bash
cd ~/code/your-repo
devdash                 # or: go run github.com/tptodorov/devdash@latest
```

Run from inside a repository and the pull requests are narrowed to it; the header
says which one. Run from anywhere else and every repo you have PRs in is included.

```bash
devdash -demo           # sample data, no credentials needed — see it before you set it up
devdash --help          # everything below, plus every API call the tool makes
devdash -once           # print one snapshot and exit, for piping or a cron job
devdash -refresh 30s    # slow the auto-refresh down
devdash -all-repos      # ignore the current repository, show everything
```

## Keys

| Key | Action |
| --- | ------ |
| `↑`/`k`, `↓`/`j` | move between rows |
| `←`/`h`, `→`/`l` | move between the columns of the selected row |
| `g` / `G` | jump to the first / last row |
| `enter` | open whatever the selected column points at |
| `o` | open the ticket, whichever column is selected |
| `p` | open the selected row's pull request |
| `c` | copy a shareable snippet: title, ticket link, every PR link |
| `s` | change the selected ticket's status |
| `S` | schedule the ticket for Symphony, or take it back unless an agent is running |
| `r` | refresh now |
| `a` | pause or resume the automatic refresh |
| `?` | keys, columns, and every indicator explained |
| `q`, `esc`, `ctrl+c` | quit |

Ticket keys and PR references are OSC 8 hyperlinks — ⌘-click them in iTerm2,
Ghostty, WezTerm, Kitty or any terminal that supports links.

## Column navigation

Left and right walk the columns of the selected row, which is highlighted in a
contrasting colour, in the order the columns are drawn. Enter opens whatever the
active one points at:

| Column | `enter` opens |
| ------ | ------------- |
| Symphony | the Symphony dashboard |
| relation | a JIRA search for its sub-tickets (JIRA only) |
| ticket | the ticket, in JIRA or Linear |
| pull request | that pull request — each PR on the row is its own column |

```
 ↩ ♪ +4 PROJ-455   Add a shared database registry  ● platform #1088  ✓  ✓2
   │ │  │                                          │
   │ │  │                                          └ pull request
   │ │  └ ticket
   │ └ relation
   └ Symphony
```

Columns appear only when they do on screen, so left and right never land
somewhere that would do nothing: a ticket with no relation, no PR and no Symphony
session has a single column. `o` still opens the ticket from anywhere on the row.
A sub-task's `↳` is not a stop either — the ticket does not carry its parent's
key, so there is nothing to open.

## A ticket with several pull requests

The first pull request shares the ticket's line. Every further one gets a line of
its own, marked `↳` immediately left of the pull request cell, so a line carrying
no ticket still reads as belonging to the ticket above it — and every reference
lands in the same column, which is what lets the states be read straight down.
Each keeps its own attention marker, so a pull request wanting something under one
that does not still announces itself:

```
 ↩ ♪ +4 PROJ-455   Add a shared database registry  ● platform #1088  ✓  ✓2
 ↩                                               ↳ ● platform #1091  ◷  ↩
                                                 ↳ ● platform #1104  ✓
```

They are all one row: `↑`/`↓` steps over the group, `←`/`→` walks into each PR in
turn, selecting the ticket highlights every line, and `c` copies all three links.

## Reading a row

```
 ✓    ↳ PROJ-475   Add account identifiers         ● platform #1103  ✓  ✓2
 │    │ │          │                               │ │               │
 │    │ │          │                               │ │               └ checks, then review
 │    │ │          │                               │ └ repo and number, linked
 │    │ │          │                               └ state: ● open, ○ draft; colour says merged or closed
 │    │ │          └ summary
 │    │ └ ticket key, linked to JIRA or Linear
 │    └ relation: +N sub-tickets, or ↳ a sub-task
 └ attention
```

Every column is sized to its contents. The relation, key and pull request
reference columns fit the widest value on screen, so they line up down the page
and cost nothing they do not use, and any column with nothing in it disappears. The PR title is capped at 30 columns, since it
only describes a pull request the row already names.

The summary is the column that flexes: it takes whatever the terminal leaves, up
to the longest summary loaded, so a wide terminal spends its room on the ticket
descriptions rather than on padding. Squeeze the window and the summary gives way
first, then the title collapses, then the reference. Anything longer than its
column is cut with an `…` — the ticket itself is one `enter` away.

The PR title drops the ticket key it conventionally opens with, since the row
already carries it.

**The leftmost column is the one to read first.** It answers a single question —
is there something here for me — so the edge of the screen can be scanned on its
own. A ticket takes the most urgent state of any of its pull requests, and the
header counts them: `3 need you`.

| Marker | Meaning |
| --- | --- |
| `✓` | approved and green: merge it |
| `↩` | changes requested: respond to the review |
| `!` | the build failed, or Symphony is blocked waiting on you |
| blank | this row wants nothing from you |

**Relation** is `+N` when a ticket has N sub-tickets, or `↳` when it is itself a
sub-task. The two cannot both apply — JIRA forbids sub-tasks of sub-tasks — so one
column carries both.

**Pull requests** are one glyph coloured by state, then two fixed slots for checks
and review. Holding the slots in place is what lets them be read as columns down
the page:

| | Glyph | Meaning |
| --- | --- | --- |
| state | `●` | open |
| | `●` violet | merged |
| | `●` red | closed without merging |
| | `○` | a draft — hollow, because a draft is a flag on an open PR, not a state of its own |
| checks | `✓` | passing — drawn faintly, because passing is expected |
| | `✗` | failing |
| | `◷` | still running |
| review | `✓` | approved; `✓3` means three approving reviews |
| | `↩` | changes requested |

Each slot is blank when there is nothing to say. A pull request merely awaiting
review is behaving normally, so it draws nothing: only a deviation earns ink. The
reference beside the glyph is coloured to match, so a merged PR is a run of violet
rather than a single tinted dot.

Tickets are grouped by status, most urgent first, and each group is coloured.

## Changing a ticket's status

Press `s` on a JIRA ticket (Linear tickets are read-only for now). The choices
come from JIRA's transitions API for that specific issue, so they are exactly
what your workflow permits from its current state.

```
╭─ PROJ-482 ────────────────────────────────────────────╮
│ Mark the CI wait helper as manual-only                │
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

`1`–`9` jump straight to a choice. Two details that matter:

- **The destination status is shown, not the transition's name.** They differ more
  often than you would expect — a transition called *Done* can move an issue to
  *Awaiting Verification*. Where they differ, the transition's own name appears as
  `via "Done"`.
- **`+1 step`** marks a transition that needs a value first, such as a resolution
  when closing. You get a second prompt listing the allowed values.

Auto-refresh pauses while the panel is open, so rows cannot move under you.

## Sharing

Press `c` to copy the selected row as text you can paste to whoever should look at
it:

```
PROJ-482 — Mark the CI wait helper as manual-only
https://your-org.atlassian.net/browse/PROJ-482
https://github.com/acme/platform/pull/1099
```

Bare URLs, so they unfurl in Slack. A ticket with several PRs contributes all of
their links. Nothing that goes stale is included — no status, no CI state.

## Symphony (optional)

Tickets that a local [Symphony](https://github.com/openai/symphony) has in hand,
or has been given, are marked in a column at the left-hand edge, beside the
attention marker. The note is the constant — it means Symphony has this ticket —
and the colour says what Symphony is doing with it:

| Marker | Meaning |
| --- | --- |
| `♪` grey | scheduled, waiting for Symphony to pick it up |
| `♪` cyan | Symphony is working on the ticket |
| `♪` yellow | waiting for the next retry window |
| `♪` red | paused waiting for operator input or approval |

`S` toggles, on a JIRA ticket (Linear has no Symphony equivalent, and pressing
`S` on a Linear ticket flashes as much). On a ticket Symphony would pick up it
strips the required labels again, which is enough to release the ticket:
Symphony re-reads the labels before it retries or reconsiders a blocked issue,
so stuck work can be taken back, fixed, and scheduled afresh. Only a ticket an
agent is actively running is refused, since removing a label cannot interrupt
a turn already in progress — stop that session in Symphony instead. Unrelated
labels and the ticket's status are left alone.

The instance is located from the `server` block of `WORKFLOW.md`'s front matter,
searching upward from the working directory:

```yaml
---
server:
  port: 10000
---
```

Discovery and the query are repeated on every refresh, since Symphony starts and
stops independently. If it is not running the column disappears entirely and
nothing is reported — that is the ordinary case.

## Reference

| Flag | What it does | Environment | Default |
| ---- | ------------ | ----------- | ------- |
| `-demo` | show sample data instead of contacting any tracker or GitHub; needs no credentials | — | real data |
| `-once` | print one snapshot and exit instead of running the interactive UI | — | interactive |
| `-refresh` | auto-refresh interval, e.g. `30s` or `2m`; `0` disables it | `DEVDASH_REFRESH` | `10s`, minimum `2s` |
| `-all-repos` | show pull requests from every repository, not just this directory's | — | scoped to this repo |
| `-jql` | JQL selecting which JIRA tickets to show | `DEVDASH_JQL` | assigned to you, not Done |
| `-linear-query` | Linear `IssueFilter` (JSON) selecting which Linear tickets to show | `DEVDASH_LINEAR_QUERY` | assigned to you, not done |
| `-pr-query` | GitHub search selecting which pull requests to show; overrides repo scoping | `DEVDASH_PR_QUERY` | open and recently merged, scoped to this repo |
| `-include-archived` | keep pull requests whose repository is archived | — | archived hidden |
| `-no-links` | render plain text instead of OSC 8 terminal hyperlinks | — | hyperlinks on |

There is also a `help` subcommand, which reports whether each required
environment variable is set and lists every API call the tool makes:

```bash
devdash help
```

Scope it to a project or an organisation by overriding the queries:

```bash
devdash -jql 'assignee = currentUser() AND project = PROJ AND statusCategory != Done ORDER BY updated DESC'
devdash -linear-query '{"team":{"key":{"eq":"ENG"}},"state":{"type":{"nin":["completed","canceled"]}}}'
devdash -pr-query 'author:@me is:pr is:open org:your-org'
```

An explicit `-pr-query` is run exactly as written, with no repository scoping and
no state filtering — ask for closed PRs and you get them.

## How it works

**Correlation.** A pull request attaches to a ticket when its key (JIRA's or
Linear's — both look like `PROJ-123`) appears in its **title**, falling back to
its **branch name**. PRs matching no active ticket are
listed under their own heading rather than hidden, because they still need
something from you.

**Which PRs are fetched.** Open ones, plus any merged in the last 30 days —
so a ticket still in review keeps showing the PR that did the work. Closed without
merging is excluded as abandoned. A *merged* PR matching no active ticket is
dropped rather than listed; it is finished work, and there can be hundreds.

**Repository scope.** The owner and name are read from `.git/config` directly — no
`git` subprocess — then confirmed against the API. That second step matters: a
renamed repository keeps its old URL in the remote, and GitHub's search does not
follow renames, so searching the stale name silently matches nothing.

**Archived repositories** are skipped. GitHub keeps returning their PRs forever,
but they cannot be merged. `-include-archived` brings them back.

**No external binaries** are needed for data. JIRA, Linear and GitHub are all
called over HTTPS. `pbcopy`/`xclip` and `open`/`xdg-open` are used only for the
clipboard and the browser, and only when you press `c` or `enter`.

Nothing is written anywhere except the clipboard, and JIRA when you press `s`.

## Troubleshooting

**"0 tickets" or an authentication error.** Run `devdash help` and check the
REQUIREMENTS table — it reports each variable as set or missing. Note that JIRA
answers an unauthenticated search with `200` and an empty list, so devdash verifies
your identity separately rather than reporting an empty backlog; an expired token
shows as an explicit error.

**An indicator is a blank box.** Your terminal font is missing one of the plain
Unicode glyphs devdash uses. Any font with reasonable symbol coverage will do; no
patched or Nerd Font is required.

**A ticket shows `no PR` when you know there is one.** The key must appear in the
PR's title or branch name. Check the spelling, and note that scoping to the current
repository hides PRs in other repositories — try `-all-repos`.

**No colour when piping.** Expected: colour is dropped when the output is not a
terminal. `-once -no-links` gives clean text for scripts.

## Contributing

Issues and pull requests are welcome.

```bash
git clone https://github.com/tptodorov/devdash
cd devdash
go test ./...
go build -o devdash .
./devdash -demo          # no credentials needed
```

CI runs on every push and pull request: `gofmt`, `go vet`, `go test -race -cover`,
and a cross-build for macOS and Linux on amd64 and arm64. Running `gofmt -l .` and
`go test ./...` locally is enough to predict it.

The README screenshot is generated from demo data, so it never contains anyone's
real tickets. To regenerate it after a change to the interface:

```bash
pip install Pillow
go build -o devdash .
python3 scripts/screenshot.py ./devdash docs/screenshot.png
```

`TestDemoDataCoversEveryState` keeps the sample data covering everything the
documentation claims to show.

To see what `S` would do to every one of your tickets without changing anything:

```bash
DEVDASH_DRYRUN_DIR=/path/to/repo go test -run TestDryRunSchedulePlans -v
```

One test suite talks to real JIRA and is skipped unless you point it at a ticket
you do not mind moving. It transitions the ticket and moves it back:

```bash
DEVDASH_LIVE_TICKET=PROJ-482 go test -run TestLiveTransitionRoundTrip -v
```

## Licence

[MIT](LICENSE)
