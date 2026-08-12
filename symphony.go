package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Symphony session states, in the order they appear in the API payload.
const (
	SymphonyRunning  = "running"
	SymphonyBlocked  = "blocked"
	SymphonyRetrying = "retrying"
)

const (
	workflowFile        = "WORKFLOW.md"
	symphonyDefaultHost = "127.0.0.1"
	symphonyStatePath   = "/api/v1/state"
)

// symphonyEndpoint finds the Symphony instance for the working tree containing
// dir, by reading the server block of WORKFLOW.md's YAML front matter:
//
//	---
//	server:
//	  port: 10000
//	---
//
// The port is taken from the file rather than from a running process, because the
// PID file Symphony leaves behind goes stale while the port stays authoritative.
func symphonyEndpoint(dir string) (string, error) {
	path, err := findWorkflowFile(dir)
	if err != nil {
		return "", err
	}

	host, port, err := symphonyServer(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:%d", host, port), nil
}

// findWorkflowFile walks up from dir looking for WORKFLOW.md, so the tool works
// from anywhere inside the tree.
func findWorkflowFile(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, workflowFile)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found", workflowFile)
		}
		dir = parent
	}
}

// symphonyServer reads host and port out of the server block. The front matter is
// simple enough that scanning it beats taking on a YAML dependency, but the scan
// has to be section-aware: WORKFLOW.md also carries polling.interval_ms and
// hooks.timeout_ms, and a naive search for a port would be free to wander.
func symphonyServer(path string) (host string, port int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	host = symphonyDefaultHost
	section := ""
	inFrontMatter := false
	seenDelimiter := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)

		if trimmed == "---" {
			if !seenDelimiter {
				seenDelimiter, inFrontMatter = true, true
				continue
			}
			break // end of front matter
		}
		if !inFrontMatter || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// A key at column zero opens a new section.
		if raw == trimmed {
			section = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if section != "server" {
			continue
		}

		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)

		switch strings.TrimSpace(key) {
		case "port":
			// Only the port directly under server:, not one nested deeper.
			if n, convErr := strconv.Atoi(value); convErr == nil && indentOf(raw) <= 2 {
				port = n
			}
		case "host":
			if value != "" && indentOf(raw) <= 2 {
				host = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	if port == 0 {
		return "", 0, fmt.Errorf("no server.port in %s", filepath.Base(path))
	}
	return host, port, nil
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

type symphonySession struct {
	IssueIdentifier string `json:"issue_identifier"`
	LastMessage     string `json:"last_message"`
}

type symphonyStateResponse struct {
	Running  []symphonySession `json:"running"`
	Blocked  []symphonySession `json:"blocked"`
	Retrying []symphonySession `json:"retrying"`
}

// SymphonyState reports which tickets Symphony currently has in hand, keyed by
// upper-cased issue key. Blocked outranks retrying outranks running, so a ticket
// that needs an operator is never hidden behind a less urgent state.
func SymphonyState(ctx context.Context, hc *http.Client, baseURL string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+symphonyStatePath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := readAllLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("symphony returned %d: %s", resp.StatusCode, firstLine(body))
	}

	var parsed symphonyStateResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decoding symphony state: %w", err)
	}

	out := map[string]string{}
	// Least urgent first, so a more urgent state overwrites it.
	for _, group := range []struct {
		sessions []symphonySession
		state    string
	}{
		{parsed.Running, SymphonyRunning},
		{parsed.Retrying, SymphonyRetrying},
		{parsed.Blocked, SymphonyBlocked},
	} {
		for _, s := range group.sessions {
			if key := strings.ToUpper(strings.TrimSpace(s.IssueIdentifier)); key != "" {
				out[key] = group.state
			}
		}
	}
	return out, nil
}

// applySymphony marks the tickets Symphony is working on.
func applySymphony(tickets []Ticket, state map[string]string) {
	for i := range tickets {
		tickets[i].Symphony = state[strings.ToUpper(tickets[i].Key)]
	}
}
