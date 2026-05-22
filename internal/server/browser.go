package server

import (
	"os/exec"
	"runtime"
)

// launchBrowser opens url in the OS default browser. Best-effort and
// non-blocking — Start returns without waiting for the browser process.
func launchBrowser(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	return exec.Command(name, args...).Start() //nolint:gosec // G204: url is the server's own bound loopback address, not user input
}

func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "cmd", []string{"/c", "start", url}
	default:
		return "xdg-open", []string{url}
	}
}
