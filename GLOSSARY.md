# Developer Work Dashboard

devdash brings a developer's tracker tickets and GitHub pull requests together so the state of their work and requests for attention are visible in one place.

## Language

### Tickets

**Ticket**:
A Jira, Linear, or GitHub issue shown in the dashboard, usually assigned to the developer and unfinished.
_Avoid_: Issue

**Ticket key**:
The short identifier for a ticket, such as `PROJ-482`, or `#123` for a GitHub issue. A key is unique within its tracker but may also exist in another tracker.
_Avoid_: Issue ID

**Status**:
A ticket's named workflow state in its tracker, such as `Backlog` or `Awaiting CR`.
_Avoid_: State

**Status category**:
The shared lifecycle category of a status across trackers: `To Do`, `In Progress`, or `Done`.

**Child ticket**:
A ticket with another ticket as its parent.
_Avoid_: Sub-ticket

### Pull requests

**Associated pull request**:
A GitHub pull request shown with a ticket as work toward that ticket. One ticket can have several associated pull requests.
_Avoid_: Linked pull request

**Unmatched pull request**:
A pull request in the dashboard's current scope with no corresponding displayed ticket.
_Avoid_: Orphan PR

### Attention and automation

**Attention**:
A signal that a ticket or unmatched pull request needs the developer's action, such as a merge, a review response, a failed check, or a blocked automation run.

**Coding automation**:
A system that takes eligible tickets and runs an AI coding workflow. Symphony is one implementation.

**Automation-ready ticket**:
A ticket eligible for a coding automation to pick up, with no automation run yet.
_Avoid_: Scheduled ticket

**Automation run**:
A coding automation's attempt to handle a ticket, whether running, retrying, or blocked waiting for operator input.
_Avoid_: Symphony session
