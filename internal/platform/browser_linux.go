package platform

func browserOpener(getenv func(string) string) ([]string, bool) {
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return nil, false
	}
	return []string{"xdg-open"}, true
}
