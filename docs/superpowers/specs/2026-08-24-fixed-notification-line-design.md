# Fixed notification line

## Goal

Show transient command feedback and persistent source problems without moving the dashboard body.

## Design

The line immediately below the `DEVDASH` header is always reserved for one notification. When nothing needs attention, it remains blank.

Messages use this priority:

1. transient command feedback;
2. JIRA error;
3. GitHub error;
4. JIRA warning.

A command notification remains visible for the existing five seconds. When it expires, the highest-priority persistent problem reappears on the same line.

The notification is a bold, reverse-color badge with a severity icon. Its text is truncated to the terminal width. No new dependency, queue, rotation, or notification state is added.

## Rendering

The view always renders exactly one notification row between the header and body. Notification count no longer changes body height, so notifications cannot shift or hide dashboard rows.

## Verification

Focused view tests cover:

- the frame height staying constant as a command notification appears and expires;
- command feedback taking priority over persistent problems;
- the next persistent problem appearing when the command feedback expires;
- a blank reserved row when there is no notification;
- a visually distinct styled notification that fits the terminal width.
