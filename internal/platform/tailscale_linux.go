package platform

// TailscaleCandidates lists where the tailscale CLI is installed when it is not on PATH.
func TailscaleCandidates() []string {
	return []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale"}
}
