package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestLiveTransitionRoundTrip exercises the real JIRA transition endpoints
// against a real issue. It mutates that issue, so it only runs when explicitly
// pointed at a ticket:
//
//	DEVDASH_LIVE_TICKET=PROJ-17552 go test -run TestLiveTransitionRoundTrip -v
//
// The issue is moved to another status and then moved back, so it ends in the
// status it started in.
func TestLiveTransitionRoundTrip(t *testing.T) {
	key := os.Getenv("DEVDASH_LIVE_TICKET")
	if key == "" {
		t.Skip("set DEVDASH_LIVE_TICKET to run this against real JIRA")
	}

	c, err := newJIRAClient(&http.Client{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("jira client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	original, err := liveStatus(ctx, c, key)
	if err != nil {
		t.Fatalf("reading current status: %v", err)
	}
	t.Logf("%s starts in %q", key, original)

	items, err := c.Transitions(ctx, key)
	if err != nil {
		t.Fatalf("Transitions(): %v", err)
	}

	// Pick a reachable destination that is not the current status and needs no
	// extra input, then confirm we can get back to where we started.
	var out, back *Transition
	for i := range items {
		tr := &items[i]
		if len(tr.Required) > 0 {
			continue
		}
		if tr.To == original && back == nil {
			back = tr
		}
		if tr.To != original && out == nil && tr.To != "" {
			out = tr
		}
	}
	if out == nil {
		t.Fatalf("no field-free transition away from %q", original)
	}
	if back == nil {
		t.Fatalf("no field-free transition back to %q; refusing to move %s one way", original, key)
	}
	t.Logf("will move %s -> %q (transition %q id=%s) and back via id=%s",
		key, out.To, out.Name, out.ID, back.ID)

	// Always attempt to restore the original status, even if an assertion fails.
	restored := false
	defer func() {
		if restored {
			return
		}
		rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rcancel()
		if err := c.ApplyTransition(rctx, key, back.ID, nil); err != nil {
			t.Errorf("FAILED TO RESTORE %s to %q: %v", key, original, err)
			return
		}
		t.Logf("restored %s to %q in cleanup", key, original)
	}()

	if err := c.ApplyTransition(ctx, key, out.ID, nil); err != nil {
		t.Fatalf("ApplyTransition() to %q: %v", out.To, err)
	}

	got, err := liveStatus(ctx, c, key)
	if err != nil {
		t.Fatalf("re-reading status: %v", err)
	}
	if got != out.To {
		t.Fatalf("status = %q after transition, want %q", got, out.To)
	}
	t.Logf("confirmed %s is now %q", key, got)

	// The tickets query must also reflect it, since that is what the UI reads.
	tickets, err := c.Tickets(ctx, "key = "+key)
	if err != nil {
		t.Fatalf("Tickets(): %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("got %d tickets for %s, want 1", len(tickets), key)
	}
	if tickets[0].Status != out.To {
		t.Errorf("search reports %q, want %q", tickets[0].Status, out.To)
	}

	if err := c.ApplyTransition(ctx, key, back.ID, nil); err != nil {
		t.Fatalf("ApplyTransition() back to %q: %v", original, err)
	}
	restored = true

	final, err := liveStatus(ctx, c, key)
	if err != nil {
		t.Fatalf("reading restored status: %v", err)
	}
	if final != original {
		t.Errorf("status = %q after restore, want %q", final, original)
	}
	t.Logf("restored %s to %q", key, final)
}

func liveStatus(ctx context.Context, c *jiraClient, key string) (string, error) {
	q := url.Values{}
	q.Set("fields", "status")

	body, status, err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key), q)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", errFromStatus(status, body)
	}

	var parsed struct {
		Fields struct {
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	return parsed.Fields.Status.Name, nil
}

func errFromStatus(status int, body []byte) error {
	return &liveError{status: status, body: firstLine(body)}
}

type liveError struct {
	status int
	body   string
}

func (e *liveError) Error() string {
	return "jira returned " + itoa(e.status) + ": " + e.body
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
