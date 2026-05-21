package server

import (
	"os/exec"
	"runtime"
)

// launchBrowser opens url in the OS default browser. Best-effort and
// non-blocking — Start returns without waiting for the browser process.
func launchBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start() //nolint:gosec // G204: url is the server's own bound loopback address, not user input
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start() //nolint:gosec // G204: url is the server's own bound loopback address, not user input
	default:
		return exec.Command("xdg-open", url).Start() //nolint:gosec // G204: url is the server's own bound loopback address, not user input
	}
}
