package link

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"time"
)

// MaxNameBytes is the longest advertised name: the length of a DNS name (LINK-670-ADDR).
const MaxNameBytes = 253

// NormalizeName lower-cases an advertised name and removes one trailing root dot.
func NormalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// ValidateName checks a normalized advertised name: lower-case ASCII letters, digits, hyphens
// and dots, at most 253 bytes, each dot-separated label 1 to 63 bytes with alphanumeric ends.
func ValidateName(name string) error {
	if name == "" || len(name) > MaxNameBytes {
		return errors.New("link_name_invalid: a computer name has 1 to 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("link_name_invalid: each part between dots has 1 to 63 characters")
		}
		if !alnum(label[0]) || !alnum(label[len(label)-1]) {
			return errors.New("link_name_invalid: each part between dots starts and ends with a letter or digit")
		}
		for i := 0; i < len(label); i++ {
			if !alnum(label[i]) && label[i] != '-' {
				return errors.New("link_name_invalid: use lower-case letters, digits, hyphens and dots")
			}
		}
	}
	return nil
}

func alnum(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }

// hostname is os.Hostname, replaceable in tests.
var hostname = os.Hostname

// ShortName is the value that `hostname -s` gives on Linux and macOS, lower-cased: the host
// name up to its first dot. It returns "" when that value is not a valid name.
func ShortName() string {
	host, err := hostname()
	if err != nil {
		return ""
	}
	short, _, _ := strings.Cut(host, ".")
	short = NormalizeName(short)
	if ValidateName(short) != nil {
		return ""
	}
	return short
}

// lookupCNAME is the resolver's canonical-name lookup, replaceable in tests.
var lookupCNAME = func(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

// LongName is the full host name: the host name when it already has a dot, else the
// resolver's canonical name for it. It returns "" when neither gives a valid name with a dot;
// the name field then stays open for a typed value.
func LongName(ctx context.Context) string {
	host, err := hostname()
	if err != nil {
		return ""
	}
	name := NormalizeName(host)
	if !strings.Contains(name, ".") {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		canonical, err := lookupCNAME(ctx, host)
		if err != nil {
			return ""
		}
		name = NormalizeName(canonical)
	}
	if !strings.Contains(name, ".") || ValidateName(name) != nil {
		return ""
	}
	return name
}
