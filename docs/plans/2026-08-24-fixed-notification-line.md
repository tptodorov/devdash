# Fixed Notification Line Implementation Plan

**Goal:** Render command feedback and source problems in one reserved, visually distinct line without moving dashboard content.
**Scope:** Notification selection, rendering, and focused view tests.
**Non-goals:** Notification queues, rotation, configurable timing, or changes to the existing five-second flash lifetime.
**Risks:** ANSI styling and truncation must not overflow narrow terminals; existing unrelated dirty view changes must be preserved.

### Files

- Modify: `app.go`
- Create: `notification_test.go`

### Task 1: Specify fixed-line behavior

- Outcome: Focused tests describe notification priority, styling, width, and stable body placement.
- Steps:
  - Add tests that compare the first body row with and without a command notification.
  - Add table tests for command, JIRA error, GitHub error, JIRA warning, and blank states.
  - Confirm the tests fail because notifications are still appended as banners.
- Verification:
  - `go test ./... -run 'TestNotification|TestViewNotification'`
  - Expected: the new tests fail before implementation.
- Dependencies:
  - none

### Task 2: Render one reserved notification line

- Outcome: `View` always places one selected notification directly below the header.
- Steps:
  - Add a small notification renderer that applies the approved priority.
  - Render the selected message as a bold reverse-color badge with a severity icon and terminal-width truncation.
  - Remove variable banner accounting from body height and frame assembly.
  - Re-run the focused tests.
- Verification:
  - `go test ./... -run 'TestNotification|TestViewNotification'`
  - Expected: focused tests pass.
- Dependencies:
  - Task 1

### Task 3: Verify regressions

- Outcome: The complete repository remains green and the diff contains only the planned implementation plus pre-existing user edits.
- Steps:
  - Format changed Go files.
  - Run all tests.
  - Inspect the final diff and status without modifying unrelated files.
- Verification:
  - `gofmt -w app.go notification_test.go`
  - `go test ./...`
  - `git diff --check`
- Dependencies:
  - Task 2
