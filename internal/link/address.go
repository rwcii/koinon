package link

import (
	"errors"
	"strings"
)

// Address limits (LINK-670-ADDR): NAME is what a local send accepts, COMPUTER is an
// advertised name, and the full address is NAME@COMPUTER.
const (
	MaxLocalNameBytes = 256
	MaxAddressBytes   = MaxLocalNameBytes + 1 + MaxNameBytes
)

// ErrAddressInvalid is a NAME@COMPUTER address that breaks its grammar.
var ErrAddressInvalid = errors.New("link_address_invalid: use NAME@COMPUTER: NAME as on this computer, COMPUTER as the paired computer's name")

// IsRemoteAddress reports whether a recipient names another computer: it has an @. Local
// names have no @.
func IsRemoteAddress(to string) bool { return strings.Contains(to, "@") }

// ParseAddress splits NAME@COMPUTER at the last @. NAME has 1 to 256 bytes and no further @,
// so a message never passes through a second computer; COMPUTER has the advertised-name
// grammar; the whole address has at most 510 bytes.
func ParseAddress(to string) (name, computer string, err error) {
	i := strings.LastIndexByte(to, '@')
	if i < 0 || len(to) > MaxAddressBytes {
		return "", "", ErrAddressInvalid
	}
	name, computer = to[:i], to[i+1:]
	if name == "" || len(name) > MaxLocalNameBytes || strings.Contains(name, "@") || strings.ContainsAny(name, "\x00\r\n") ||
		ValidateName(computer) != nil {
		return "", "", ErrAddressInvalid
	}
	return name, computer, nil
}
