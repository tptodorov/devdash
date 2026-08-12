package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// clipboardCommand returns the platform's clipboard writer, or nil when none of
// the usual tools are installed.
func clipboardCommand() *exec.Cmd {
	candidates := map[string][][]string{
		"darwin":  {{"pbcopy"}},
		"windows": {{"clip"}},
	}[runtime.GOOS]

	if candidates == nil {
		// Wayland first, then the X11 tools.
		candidates = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	}

	for _, argv := range candidates {
		if path, err := exec.LookPath(argv[0]); err == nil {
			return exec.Command(path, argv[1:]...)
		}
	}
	return nil
}

// copyToClipboard puts text on the system clipboard, falling back to the OSC 52
// terminal escape when no clipboard tool is available — which is also what makes
// this work over SSH, where the local clipboard is out of reach.
func copyToClipboard(text string) error {
	if cmd := clipboardCommand(); cmd != nil {
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return osc52Copy(text)
}

// osc52Copy asks the terminal itself to set the clipboard. Support varies by
// terminal, so a nil error here means the request was sent, not that it landed.
func osc52Copy(text string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if _, err := fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", encoded); err != nil {
		return fmt.Errorf("no clipboard tool found and the terminal refused the copy: %w", err)
	}
	return nil
}
