package platform

// TailscaleCandidates lists where the tailscale CLI is installed when it is not on PATH:
// inside the Tailscale app first, then the Homebrew prefixes.
func TailscaleCandidates() []string {
	return []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale"}
}
