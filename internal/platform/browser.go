package platform

// BrowserOpener names the command that opens a URL in the user's browser, or reports
// that no browser is available, for example in an SSH session or, on Linux, without a
// graphical display. The URL is the command's last argument.
func BrowserOpener(getenv func(string) string) ([]string, bool) {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil, false
	}
	return browserOpener(getenv)
}
