package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
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

// symphonyConfig is the part of WORKFLOW.md's front matter that decides which
// tickets Symphony will pick up, and where its server listens.
type symphonyConfig struct {
	Tracker struct {
		Provider struct {
			ProjectKey string `yaml:"project_key"`
		} `yaml:"provider"`
		RequiredLabels []string `yaml:"required_labels"`
		ActiveStates   []string `yaml:"active_states"`
		TerminalStates []string `yaml:"terminal_states"`
	} `yaml:"tracker"`
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"server"`
}

// frontMatter returns the YAML block delimited by --- at the top of a file.
func frontMatter(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var block []string
	inside, seen := false, false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			if !seen {
				seen, inside = true, true
				continue
			}
			break
		}
		if inside {
			block = append(block, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !seen {
		return nil, fmt.Errorf("%s has no YAML front matter", filepath.Base(path))
	}
	return []byte(strings.Join(block, "\n")), nil
}

// readSymphonyConfig parses the front matter of the WORKFLOW.md governing dir.
func readSymphonyConfig(dir string) (symphonyConfig, error) {
	var cfg symphonyConfig

	path, err := findWorkflowFile(dir)
	if err != nil {
		return cfg, err
	}
	raw, err := frontMatter(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s front matter: %w", filepath.Base(path), err)
	}
	return cfg, nil
}

// symphonyServer reports where the Symphony server for this tree listens. The
// port comes from the file rather than from the PID file Symphony leaves behind,
// which goes stale while the port stays authoritative.
func symphonyServer(path string) (host string, port int, err error) {
	raw, err := frontMatter(path)
	if err != nil {
		return "", 0, err
	}
	var cfg symphonyConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return "", 0, fmt.Errorf("parsing %s front matter: %w", filepath.Base(path), err)
	}
	if cfg.Server.Port == 0 {
		return "", 0, fmt.Errorf("no server.port in %s", filepath.Base(path))
	}
	host = cfg.Server.Host
	if host == "" {
		host = symphonyDefaultHost
	}
	return host, cfg.Server.Port, nil
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
