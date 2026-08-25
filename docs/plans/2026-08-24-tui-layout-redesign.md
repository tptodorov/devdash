# TUI Layout Redesign Implementation Plan

**Goal:** Rebuild the dashboard row so what needs the user is readable at the left edge, and so every colour carries exactly one meaning.
**Scope:** Row layout, the colour palette, the attention and relation columns, Symphony placement, orphan alignment, and the help and README text that documents them.
**Non-goals:** No new API fields (no PR age, no ticket age), no change to fetching, correlation, grouping order, or the key bindings themselves. The attention-first "focus view" (mock C) and the narrow-terminal stacked layout (mock B) are deliberately out of scope.
**Risks:** Snapshot goldens assert exact column positions and will all need regolding; reordering the left/right column stops changes `←→` traversal and will break selection tests; removing `-no-nerd-font` is user-facing; the new glyphs must all measure one display column under `runewidth`.

The agreed design is rendered by `docs/mock/main.go` (`go run ./docs/mock`, `-legend` for the palette alone). That program is the reference for this work and should be deleted once the layout lands.

### Files

- Modify: `style.go`, `view.go`, `app.go`, `main.go`, `help.go`, `README.md`
- Create: `attention.go`, `attention_test.go`
- Regold: `snapshot_test.go`, `view` and `selection` test expectations
- Delete on completion: `docs/mock/`

### The target grid

```
col 0    selection bar
col 1    attention: what this row wants from you
col 2    (space)
col 3    Symphony: a note, coloured by session state
col 4    (space)
col 5-7  relation: +N children, or ↳ subtask, right-aligned
col 8    (space)
col 9+   key, summary (flexes), then the PR cell at the right edge
```

At 100 columns the summary gets 54, up from 49, which is enough that the
longest demo summary stops truncating.

### Task 1: Give every colour one meaning

- Outcome: `accent` is used only for selection; violet means only a merged PR; Symphony has its own hue.
- Steps:
  - In `style.go:12-42`, add `colSym = lipgloss.Color("213")` and `colReview = lipgloss.Color("117")`.
  - Remove `accent` from `titleStyle`, and later (Tasks 3 and 5) from the relation column and the Symphony marker, leaving `gutterSeg` (`view.go:392`) as its only user.
  - In `statusColor` (`style.go:167`), move the review statuses — `awaiting cr`, `in review`, `code review`, `review`, `peer review` — from `141` to `colReview`, so `colorMerged` is unambiguous.
  - Make `prCheckIcon`'s success case use `mutedStyle` rather than `okStyle`: a passing build is the expected case and should not compete with a failure for attention.
- Verification:
  - `go test ./... -run 'TestStatusColor|TestPRCheckIcon|TestPRStateStyle'`
  - `grep -n accent style.go view.go` — expect hits only in `gutterSeg` and the selection styles.
- Dependencies: none

### Task 2: Derive the attention marker

- Outcome: a pure function maps a ticket to the single most urgent thing it wants, with tests covering the precedence.
- Steps:
  - Create `attention.go` with an `attention` type and `attentionFor(t Ticket) attention`, returning, highest first: `attnAlert` (any PR's CI failed, or `t.Symphony == SymphonyBlocked`), `attnChanges` (any PR has `Review == "CHANGES_REQUESTED"`), `attnMerge` (a PR is open, not draft, `CI == "SUCCESS"`, and approved or `Approvals > 0`), else `attnNone`.
  - Add `attentionForPR(pr PullRequest)` for continuation lines, so a bad PR under a good one still marks itself.
  - Add a `needsYou(groups []group, orphans []PullRequest) int` tally for the header.
  - Write `attention_test.go` as a table test: each precedence pair, a merged PR contributing nothing, a draft never reaching `attnMerge`, and the blocked-Symphony case.
- Verification:
  - `go test ./... -run TestAttention`
  - Expected: the table passes; no other package changes yet.
- Dependencies: none

### Task 3: Replace the type and children columns with one relation column

- Outcome: `+N` for children, `↳` for a subtask, blank otherwise, in three right-aligned columns.
- Steps:
  - Delete `typeCode` and `maxTypeCode` (`style.go:194-216`) and `(*app).typeLegend` (`view.go:272-300`).
  - Drop `typeW` from `widths` and `layout` and from `measure`/`computeLayout` (`view.go:44-186`); replace `childW` with a fixed `relW = 3`.
  - In `ticketLine` (`view.go:413-436`), emit the relation cell from `t.ChildCount` and `t.IsSubtask` in `mutedStyle`, right-aligned, before the key.
  - Rename `stopChildren` to `stopRelation` (`app.go:18`). Keep the children search URL for the `+N` case; for a subtask, leave the stop off unless the parent key is available.
- Verification:
  - `go test ./... -run 'TestLayout|TestTicketLine|TestMeasure'`
  - `grep -rn typeCode\\\|typeLegend *.go` — expect no hits outside tests being deleted.
- Dependencies: Task 1

### Task 4: Rebuild the PR cell

- Outcome: one glyph coloured by state, hollow when draft, then two fixed one-glyph badge slots.
- Steps:
  - In `style.go`, collapse `prStateIcon` to a single glyph: `●`, or `○` when `pr.Draft`, coloured by `prStateStyle`. Keep `prStateStyle`'s existing precedence — closed and merged outrank draft — and return `faintStyle` only for an *open* draft.
  - In `prSegs` (`view.go:189-231`), replace the `rev✓`/`rev±`/`rev?` text badges with two fixed slots: checks (`✓` muted, `✗` bad, `◷` warn, blank) then review (`✓`, `✓N` ok, `↩` bad, blank for awaiting). Awaiting review draws nothing — only a deviation earns ink.
  - Keep `prRefWidth` measuring the rendered segments so the column can never be a column short of its content.
- Verification:
  - `go test ./... -run 'TestPRSegs|TestPRRefWidth|TestPRCell'`
  - `go run ./docs/mock` and compare the PR cell against the reference render.
- Dependencies: Task 1

### Task 5: Move Symphony to the left lane and reorder the column stops

- Outcome: the note sits at column 3, coloured by state, and `←→` walks the row left to right.
- Steps:
  - In `symphonyMarker` (`style.go:54-67`) return `♪` for every state, styled: `faintStyle` scheduled, `colSym` bold running, `colWarn` bold retrying, and red bold reverse for blocked. Delete `symphonyIconBlocked` and `symphonyIconRetrying`.
  - In `ticketLine`, emit the marker at column 3 and delete the right-edge Symphony block, including `layout.symBlock` and `symW` (`view.go:51,85-90`) and the padding at `view.go:450-458`.
  - In `settle` (`app.go:729-750`), reorder the appends to match the new visual order: Symphony, relation, ticket, then the PRs.
- Verification:
  - `go test ./... -run 'TestSymphony|TestStops|TestSelection|TestColumnWalk'`
  - Manually: `←→` across a Symphony row visits columns in screen order.
- Dependencies: Tasks 1, 3

### Task 6: Remove the row chrome

- Outcome: absence is silent, and the sections carry their own separation.
- Steps:
  - Delete the `·  no PR` filler (`view.go:445-447`), leaving the PR cell blank.
  - Delete `prLead` and its 3-column cell (`view.go:37,441`); keep `prBranch` for continuation lines only, and adjust `layout.prBlock`/`prColumn` accordingly.
  - Drop the blank line appended after each group (`view.go:322`), relying on the coloured bar and rule.
- Verification:
  - `go test ./... -run 'TestBuildBody|TestGroupHeader'`
  - Expected: the demo body loses one line per group and gains 3 columns of summary.
- Dependencies: Task 3

### Task 7: Put orphan pull requests on the shared grid

- Outcome: the PR-without-a-ticket section aligns with every row above it.
- Steps:
  - Rewrite `orphanLine` (`view.go:482-499`) to drop the local `refW = 24` and the `⇢` glyph: put `pr.Title` in the summary column in `mutedStyle`, and the reference in the shared PR column via `prCellSegs`.
  - Set the attention marker from `attentionForPR`.
- Verification:
  - `go test ./... -run 'TestOrphanLine|TestSnapshot'`
  - Expected: the orphan PR reference starts at the same screen column as every other PR reference.
- Dependencies: Tasks 2, 4, 6

### Task 8: Update the header, the help, and the docs; delete the Nerd Font machinery

- Outcome: the chrome reflects the new design, and nothing depends on a Nerd Font.
- Steps:
  - In `headerView` (`view.go:501-548`), prefer `shortRepo(a.prScope)` over the full URL, and add the `needsYou` tally in `colBad` bold when non-zero.
  - In `helpView` (`view.go:594-678`), delete the "TYPES IN VIEW" section, and rewrite the Symphony, PR-state, and legend blocks to the new indicators. Mirror the sections in `docs/mock/main.go`'s `legend()`.
  - Every glyph in the new design is plain Unicode, so delete `useNerdFont`, `nerdPRIcons`, `fallbackPRIcons`, `nerdCheckIcons`, `fallbackCheckIcons`, `prIcons`, `checkIcons` (`style.go:69-115`) and the `-no-nerd-font` flag in `main.go`.
  - Update `README.md`: drop the Nerd Font requirement and the `-no-nerd-font` line, refresh the Keys table, and regenerate `docs/screenshot.png`.
- Verification:
  - `go test ./... -run 'TestHelp|TestHeader'`
  - `grep -rn 'nerd\|NerdFont' *.go README.md` — expect no hits.
- Dependencies: Tasks 3, 4, 5

### Task 9: Regold and verify

- Outcome: the repository is green and the diff contains only the planned work.
- Steps:
  - Regold `snapshot_test.go` and any width-sensitive expectations in the view and selection tests, reading each diff rather than accepting wholesale — these goldens are the only guard on column alignment.
  - Confirm the layout still degrades sanely at 80 and 60 columns, the `computeLayout` floor.
  - `gofmt -w` the changed files, run everything, inspect the diff.
  - Delete `docs/mock/` and this plan's reference to it.
- Verification:
  - `gofmt -l .` — expect no output
  - `go vet ./... && go test ./...`
  - `git diff --check`
- Dependencies: Tasks 1-8
