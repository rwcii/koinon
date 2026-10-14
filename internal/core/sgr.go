package core

import "strings"

// sgrDim returns the dim state after one SGR sequence (ESC [ params m), starting from dim:
// 2 sets it, and an empty parameter, 0 and 22 clear it. The arguments of an extended color
// (38, 48 or 58, as 5;N or 2;R;G;B) are color data, never attributes (#371): a truecolor
// background such as 48;2;30;68;78 or the 256-color index in 38;5;2 leaves the state as it is.
func sgrDim(params string, dim bool) bool {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "", "0", "22":
			dim = false
		case "2":
			dim = true
		case "38", "48", "58":
			if i+1 < len(fields) {
				switch fields[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				}
			}
		}
	}
	return dim
}
