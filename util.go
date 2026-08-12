package main

import (
	"bytes"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mattn/go-runewidth"
)

const maxResponseBytes = 8 << 20

func readAllLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxResponseBytes))
}

// firstLine returns a short, single-line excerpt suitable for an error banner.
func firstLine(b []byte) string {
	s := string(bytes.TrimSpace(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return runewidth.Truncate(s, 200, "…")
}

// trunc shortens s to fit width w, marking elision with an ellipsis.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

// pad right-pads s with spaces so it occupies exactly w display columns.
func pad(s string, w int) string {
	n := w - runewidth.StringWidth(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

// padLeft left-pads s with spaces so it occupies exactly w display columns,
// right-aligning the content.
func padLeft(s string, w int) string {
	n := w - runewidth.StringWidth(s)
	if n <= 0 {
		return s
	}
	return strings.Repeat(" ", n) + s
}

// shortRepo drops the owner from owner/name, which is what a person calls the
// repository they are standing in.
func shortRepo(nameWithOwner string) string {
	if i := strings.LastIndexByte(nameWithOwner, '/'); i >= 0 {
		return nameWithOwner[i+1:]
	}
	return nameWithOwner
}

// openURL hands a URL to the platform's browser opener.
func openURL(url string) error {
	if url == "" {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
