package link

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strings"

	"github.com/rwcii/koinon/internal/remote"
)

// InvitationPrefix starts every invitation text, so a paste of other text fails clearly.
const InvitationPrefix = "koinon-link-1:"

// maxInvitationBytes bounds an invitation text.
const maxInvitationBytes = 2048

// SecretBytes is the size of an invitation's single-use secret: 256 random bits.
const SecretBytes = 32

// Invitation is what one computer gives the other through the maintainer (LINK-670-PAIR):
// its tailnet name and Tailscale addresses, its link port, its key pin, its advertised name
// and a single-use secret. The joining computer checks the pin before it sends the secret.
type Invitation struct {
	Version int      `json:"v"`
	DNSName string   `json:"dns"`
	IPs     []string `json:"ips"`
	Port    int      `json:"port"`
	Pin     string   `json:"pin"`
	Name    string   `json:"name"`
	Secret  string   `json:"secret"`
}

// NewSecret returns a new single-use secret, base64url without padding.
func NewSecret() (string, error) {
	b := make([]byte, SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SecretHash is what the inviting daemon keeps of a secret.
func SecretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Encode gives the invitation text.
func (i Invitation) Encode() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	return InvitationPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// ParseInvitation reads an invitation text; surrounding white space is ignored.
func ParseInvitation(text string) (Invitation, error) {
	invalid := errors.New("link_invitation_invalid: paste the whole invitation from the other computer's Add a computer step")
	text = strings.TrimSpace(text)
	if len(text) > maxInvitationBytes || !strings.HasPrefix(text, InvitationPrefix) {
		return Invitation{}, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(text, InvitationPrefix))
	if err != nil {
		return Invitation{}, invalid
	}
	var i Invitation
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&i) != nil || dec.Decode(new(any)) != io.EOF || i.Validate() != nil {
		return Invitation{}, invalid
	}
	return i, nil
}

// Validate checks every field of an invitation.
func (i Invitation) Validate() error {
	invalid := errors.New("link_invitation_invalid")
	if i.Version != 1 || remote.NormalizeDNSName(i.DNSName) != i.DNSName || i.DNSName == "" || !remote.ValidHTTPSPort(i.Port) || !ValidPin(i.Pin) || ValidateName(i.Name) != nil {
		return invalid
	}
	if ips, ok := ValidAddresses(i.IPs); !ok || len(ips) != len(i.IPs) {
		return invalid
	}
	if b, err := base64.RawURLEncoding.DecodeString(i.Secret); err != nil || len(b) != SecretBytes {
		return invalid
	}
	return nil
}

var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// ValidAddresses checks a list of 1 to 4 distinct Tailscale addresses in canonical form,
// IPv4 or IPv6; one family is enough.
func ValidAddresses(ips []string) ([]netip.Addr, bool) {
	if len(ips) == 0 || len(ips) > 4 {
		return nil, false
	}
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, text := range ips {
		addr, err := netip.ParseAddr(text)
		if err != nil || addr.String() != text || addr.Zone() != "" || seen[addr] {
			return nil, false
		}
		if !(addr.Is4() && tailnetV4.Contains(addr)) && !(addr.Is6() && !addr.Is4In6() && tailnetV6.Contains(addr)) {
			return nil, false
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out, true
}
