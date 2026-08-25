package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var reverseSGR = regexp.MustCompile("\\x1b\\[(?:[0-9]+;)*7(?:;[0-9]+)*m")

func TestNotificationLinePriority(t *testing.T) {
	jiraErr := errors.New("JIRA unavailable")
	ghErr := errors.New("GitHub unavailable")
	jiraWarn := errors.New("child counts unavailable")

	tests := []struct {
		name string
		app  app
		want string
	}{
		{
			name: "command feedback wins",
			app:  app{flash: "copied PROJ-1", jiraErr: jiraErr, ghErr: ghErr, jiraWarn: jiraWarn},
			want: "✓ copied PROJ-1",
		},
		{
			name: "JIRA error precedes GitHub error",
			app:  app{jiraErr: jiraErr, ghErr: ghErr, jiraWarn: jiraWarn},
			want: "! jira: JIRA unavailable",
		},
		{
			name: "GitHub error precedes JIRA warning",
			app:  app{ghErr: ghErr, jiraWarn: jiraWarn},
			want: "! github: GitHub unavailable",
		},
		{
			name: "JIRA warning is the fallback",
			app:  app{jiraWarn: jiraWarn},
			want: "~ jira: child counts unavailable",
		},
		{name: "nothing to report leaves the row blank", app: app{}, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.app.width = 60
			lay := tc.app.layout()
			got := tc.app.notificationView(lay)
			plain := ansi.Strip(got)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("notification = %q, want a blank row", got)
				}
				return
			}
			if !strings.Contains(plain, tc.want) {
				t.Errorf("notification = %q, want it to contain %q", plain, tc.want)
			}
			if !reverseSGR.MatchString(got) {
				t.Errorf("notification is not reverse styled: %q", got)
			}
			if width := lipgloss.Width(got); width > lay.width {
				t.Errorf("notification is %d columns wide, have %d", width, lay.width)
			}
		})
	}

	t.Run("long feedback is truncated", func(t *testing.T) {
		a := app{width: 60, flash: strings.Repeat("long feedback ", 10)}
		got := a.notificationView(a.layout())
		if width := lipgloss.Width(got); width > a.layout().width {
			t.Errorf("notification is %d columns wide, have %d", width, a.layout().width)
		}
		if plain := ansi.Strip(got); !strings.HasSuffix(plain, "…") {
			t.Errorf("notification = %q, want truncated text", plain)
		}
	})
}

func TestViewNotificationUsesReservedRow(t *testing.T) {
	a := newAppForTest()
	a.loadDemo()

	quiet := strings.Split(a.View(), "\n")
	if quiet[1] != "" {
		t.Fatalf("reserved notification row = %q, want blank", quiet[1])
	}
	wantBodyRow := lineContaining(t, quiet, "AWAITING CR")

	a.setFlash("copied PROJ-482")
	active := strings.Split(a.View(), "\n")
	if plain := ansi.Strip(active[1]); !strings.Contains(plain, "copied PROJ-482") {
		t.Fatalf("reserved notification row = %q, want command feedback", plain)
	}
	if got := lineContaining(t, active, "AWAITING CR"); got != wantBodyRow {
		t.Errorf("first body row moved from line %d to %d", wantBodyRow, got)
	}
}

func TestExpiredCommandNotificationRestoresPersistentProblem(t *testing.T) {
	now := time.Now()
	a := &app{
		width:      60,
		flash:      "copied PROJ-1",
		flashUntil: now.Add(-time.Second),
		jiraErr:    errors.New("JIRA unavailable"),
	}

	a.Update(tickMsg(now))
	plain := ansi.Strip(a.notificationView(a.layout()))
	if strings.Contains(plain, "copied PROJ-1") || !strings.Contains(plain, "JIRA unavailable") {
		t.Errorf("notification after expiry = %q, want the persistent JIRA error", plain)
	}
}

func lineContaining(t *testing.T, lines []string, want string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), want) {
			return i
		}
	}
	t.Fatalf("frame has no line containing %q", want)
	return -1
}
