package link

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/remote"
)

// StateFile is the private link state of the state directory.
const StateFile = "link.json"

// Limits of the link state.
const (
	MaxPairs   = 64
	MaxInvites = 16
)

// Pair states and roles.
const (
	Pending = "pending"
	Paired  = "paired"
	Inviter = "inviter"
	Joiner  = "joiner"
)

// DefaultPort is the Serve TCP port of the link when the maintainer names none.
const DefaultPort = 47672

// State is this computer's link setup and its pairs. Name is "" until the first Add a
// computer sets it. Open invitations keep only a hash of their secret.
type State struct {
	Version int      `json:"version"`
	Name    string   `json:"name"`
	Port    int      `json:"port"`
	Pairs   []Pair   `json:"pairs"`
	Invites []Invite `json:"invites"`
}

// Pair is one other computer. ID is new for each pairing, so a new pairing of the same
// computer is a new generation and old work is never revived. Advertised is the name that
// the other computer sends; Override is the maintainer's local name, or "" ; Effective is the
// unique local name that addresses use.
type Pair struct {
	ID         string   `json:"id"`
	Advertised string   `json:"advertised"`
	Override   string   `json:"override,omitempty"`
	Effective  string   `json:"effective"`
	Pin        string   `json:"pin"`
	DNSName    string   `json:"dns"`
	IPs        []string `json:"ips"`
	Port       int      `json:"port"`
	State      string   `json:"state"`
	Role       string   `json:"role"`
	Created    int64    `json:"created"`
	Expires    int64    `json:"expires,omitempty"`
}

// Invite is an open invitation of this computer.
type Invite struct {
	SecretHash string `json:"secret_hash"`
	Created    int64  `json:"created"`
	Expires    int64  `json:"expires"`
}

// NewID returns a new random pair ID.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var errState = errors.New("link_state_invalid: inspect link.json in the private state directory")

// ReadState reads the link state; a missing file is the empty state.
func ReadState(root string) (State, error) {
	f, err := platform.OpenPrivate(filepath.Join(root, StateFile), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: 1}, nil
	}
	if err != nil {
		return State{}, err
	}
	defer f.Close()
	var s State
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(&s) != nil || dec.Decode(new(any)) != io.EOF || s.Validate() != nil {
		return State{}, errState
	}
	return s, nil
}

// WriteState replaces the link state atomically, never through an unsafe inode.
func WriteState(root string, s State) error {
	if err := s.Validate(); err != nil {
		return err
	}
	path := filepath.Join(root, StateFile)
	if f, err := platform.OpenPrivate(path, os.O_RDONLY); err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return platform.WriteAtomic(path, append(data, '\n'), 0600)
}

// Validate checks the whole state: names, pins, addresses and the uniqueness of IDs, pins
// and effective names.
func (s State) Validate() error {
	if s.Version != 1 || s.Name != "" && ValidateName(s.Name) != nil || s.Port != 0 && !remote.ValidHTTPSPort(s.Port) || len(s.Pairs) > MaxPairs || len(s.Invites) > MaxInvites {
		return errState
	}
	ids, pins, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range s.Pairs {
		if len(p.ID) != 32 || ids[p.ID] || !ValidPin(p.Pin) || pins[p.Pin] || ValidateName(p.Advertised) != nil || ValidateName(p.Effective) != nil || names[p.Effective] {
			return errState
		}
		if p.Override != "" && ValidateName(p.Override) != nil {
			return errState
		}
		if remote.NormalizeDNSName(p.DNSName) != p.DNSName || p.DNSName == "" || !remote.ValidHTTPSPort(p.Port) {
			return errState
		}
		if _, ok := ValidAddresses(p.IPs); !ok {
			return errState
		}
		if p.State != Pending && p.State != Paired || p.Role != Inviter && p.Role != Joiner || p.State == Pending && p.Expires == 0 {
			return errState
		}
		ids[p.ID], pins[p.Pin], names[p.Effective] = true, true, true
	}
	for _, i := range s.Invites {
		if len(i.SecretHash) != 64 || i.Expires <= i.Created {
			return errState
		}
	}
	return nil
}

// FreeName returns name, or name-2, name-3 and so on: the first that no other pair uses as
// its effective name. except is the ID of the pair that is renamed, or "".
func (s State) FreeName(name, except string) string {
	used := map[string]bool{}
	for _, p := range s.Pairs {
		if p.ID != except {
			used[p.Effective] = true
		}
	}
	if !used[name] {
		return name
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		base := name
		if len(base)+len(suffix) > MaxNameBytes {
			base = base[:MaxNameBytes-len(suffix)]
		}
		// The suffix joins the last label, which keeps its 63-byte limit.
		if last := len(base) - 1 - strings.LastIndexByte(base, '.'); last+len(suffix) > maxLabelBytes {
			base = base[:len(base)-(last+len(suffix)-maxLabelBytes)]
		}
		// A cut can end a label with a hyphen or a dot; the suffix starts with a hyphen, so
		// remove such characters from the end of the base.
		for len(base) > 0 && (base[len(base)-1] == '-' || base[len(base)-1] == '.') {
			base = base[:len(base)-1]
		}
		candidate := base + suffix
		if !used[candidate] && ValidateName(candidate) == nil {
			return candidate
		}
	}
}

// PairByPin returns the pair with this pin.
func (s State) PairByPin(pin string) (Pair, bool) {
	for _, p := range s.Pairs {
		if p.Pin == pin {
			return p, true
		}
	}
	return Pair{}, false
}
