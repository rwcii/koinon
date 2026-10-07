package platform

func browserOpener(func(string) string) ([]string, bool) {
	return []string{"/usr/bin/open"}, true
}
