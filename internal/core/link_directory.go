package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/link"
)

// The receiver directory and addresses of paired computers (#670 chunk 02; LINK-670-WHAT
// items 2 and 3, LINK-670-ADDR). Each computer asks each paired computer for the sessions that
// can receive and keeps the last answer in memory, with its time, as a hint for peers and
// peer_status. The cache never authorizes a send.

const (
	linkDirectoryMaxEntries = 256
	linkDirectoryMaxBytes   = 48 << 10
)

var (
	ErrLinkComputerUnknown  = Refusal{"link_computer_unknown", "no paired computer has that name; see koinon link status"}
	ErrLinkMessagesPending  = Refusal{"link_messages_pending", "this Koinon version does not send messages to other computers yet; the computer is paired"}
	ErrLinkMaintainerRemote = Refusal{"link_maintainer_remote", "the maintainer sends only to sessions on this computer; use the other computer's dashboard"}
	ErrLinkAddressInvalid   = Refusal{"link_address_invalid", "use NAME@COMPUTER: NAME as on that computer, COMPUTER as this computer's name for it"}
)

// LinkEntry is one session that can receive on a paired computer. It carries no session ID,
// folder path, host or process data and no credential.
type LinkEntry struct {
	Name       string `json:"name"`
	Address    string `json:"address,omitempty"`
	Family     string `json:"family"`
	Role       string `json:"role,omitempty"`
	Repository string `json:"repository,omitempty"`
	Activity   string `json:"activity,omitempty"`
	ActivityAt int64  `json:"activity_at,omitempty"`
}

// linkDirectory is a computer's answer to the directory operation: its advertised name and
// its receivers, in family and name order, bounded in count and bytes.
type linkDirectory struct {
	Name      string      `json:"name"`
	Entries   []LinkEntry `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

// linkCache is the last directory of one pair, with the pin it came from and its time in
// milliseconds.
type linkCache struct {
	pin string
	gen uint64
	dir linkDirectory
	at  int64
}

// repositoryLabel is the base name of a repository, never its path.
func repositoryLabel(path string) string {
	if path == "" {
		return ""
	}
	p := filepath.Clean(path)
	if filepath.Base(p) == ".git" {
		p = filepath.Dir(p)
	}
	base := strings.TrimSuffix(filepath.Base(p), ".git")
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	return base
}

// storedActivity is a session's current activity observation from the daemon's own records,
// with its source time; it reads no provider.
func (s *Store) storedActivity(key Key) (string, int64) {
	s.observed.mu.Lock()
	defer s.observed.mu.Unlock()
	v, ok := s.observed.byKey[key]["activity"]
	if !ok || s.now().UnixMilli()-v.seen > observationFreshness["activity"].Milliseconds() {
		return "", 0
	}
	return v.State, v.At
}

// linkDirectoryOf builds this computer's directory: every active agent session that can
// receive by its peer name, sub-agents included, without the maintainer.
func (d *Daemon) linkDirectoryOf(ctx context.Context, name string) (linkDirectory, error) {
	items, truncated, err := d.store.List(ctx)
	if err != nil {
		return linkDirectory{}, err
	}
	entries := make([]LinkEntry, 0, len(items))
	for _, item := range items {
		if item.State != "active" || item.Family == maintainerKey.Family {
			continue
		}
		e := LinkEntry{Name: item.Name, Family: item.Family, Role: item.Role, Repository: repositoryLabel(item.Repository)}
		if item.HoldsAddress {
			e.Address = item.Address
		}
		e.Activity, e.ActivityAt = d.store.storedActivity(Key{item.Family, item.ID})
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b LinkEntry) int {
		if c := strings.Compare(a.Family, b.Family); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	dir := linkDirectory{Name: name, Truncated: truncated, Entries: []LinkEntry{}}
	size := 0
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return linkDirectory{}, err
		}
		if len(dir.Entries) == linkDirectoryMaxEntries || size+len(data)+1 > linkDirectoryMaxBytes {
			dir.Truncated = true
			break
		}
		size += len(data) + 1
		dir.Entries = append(dir.Entries, e)
	}
	return dir, nil
}

// validEntry keeps only well-formed entries of another computer's directory.
func validEntry(e LinkEntry) bool {
	okName := func(v string, required bool) bool {
		return (v != "" || !required) && len(v) <= link.MaxLocalNameBytes && utf8.ValidString(v) && !strings.ContainsAny(v, "@\x00\r\n")
	}
	return okName(e.Name, true) && okName(e.Address, false) && okName(e.Family, true) && okName(e.Role, false) &&
		okName(e.Repository, false) && okName(e.Activity, false) && !strings.ContainsAny(e.Repository, `/\`) && e.ActivityAt >= 0
}

// pairedPin finds the paired (not pending) pair with this pin and ID. The link lock is held.
func pairedPin(s link.State, pin, id string) (int, bool) {
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.Pin == pin && p.ID == id && p.State == link.Paired })
	return i, i >= 0
}

// answerDirectory is the directory operation: only a paired key gets it.
func (d *Daemon) answerDirectory(pin, id string) (linkDirectory, error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return linkDirectory{}, err
	}
	if _, ok := pairedPin(s, pin, id); !ok {
		return linkDirectory{}, ErrLinkNoPair
	}
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout/2)
	defer cancel()
	return d.linkDirectoryOf(ctx, s.Name)
}

// renamePairLocked sets the advertised name that a paired computer sent. Without an override,
// the effective name follows it, or the next free name. No other pair changes. The link lock
// is held.
func (d *Daemon) renamePairLocked(s *link.State, i int, name string) bool {
	if name == "" || s.Pairs[i].Advertised == name {
		return false
	}
	s.Pairs[i].Advertised = name
	if s.Pairs[i].Override == "" {
		s.Pairs[i].Effective = s.FreeName(name, s.Pairs[i].ID)
	}
	return true
}

// acceptNameUpdate is the name-update operation of a paired key.
func (d *Daemon) acceptNameUpdate(pin, id, name string, gen uint64) error {
	name = link.NormalizeName(name)
	if link.ValidateName(name) != nil {
		return ErrLinkNameInvalid
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return ErrLinkRemoteOff
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return err
	}
	i, ok := pairedPin(s, pin, id)
	if !ok {
		return ErrLinkNoPair
	}
	if d.renamePairLocked(&s, i, name) {
		return link.WriteState(d.root, s)
	}
	return nil
}

type linkNameUpdate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// pollDirectory asks a connected pair for its directory and keeps it, only in generation gen
// and only for the same pair ID and pin. Its advertised name updates the pair, so a missed
// name-update arrives at the next pass.
func (d *Daemon) pollDirectory(session linkSession, gen uint64, p link.Pair) {
	var dir linkDirectory
	if session.call("directory", linkPairID{ID: p.ID}, "directory-ok", &dir) != nil {
		return
	}
	entries := make([]LinkEntry, 0, min(len(dir.Entries), linkDirectoryMaxEntries))
	for _, e := range dir.Entries {
		if len(entries) == linkDirectoryMaxEntries {
			dir.Truncated = true
			break
		}
		if validEntry(e) {
			entries = append(entries, e)
		}
	}
	dir.Entries = entries
	name := link.NormalizeName(dir.Name)
	if link.ValidateName(name) != nil {
		name = ""
	}
	dir.Name = name
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return
	}
	i, ok := pairedPin(s, p.Pin, p.ID)
	if !ok {
		return
	}
	if d.renamePairLocked(&s, i, name) && link.WriteState(d.root, s) != nil {
		return
	}
	if d.link.directories == nil {
		d.link.directories = map[string]linkCache{}
	}
	d.link.directories[p.ID] = linkCache{pin: p.Pin, gen: gen, dir: dir, at: d.linkNow().UnixMilli()}
}

// dropDirectoryLocked forgets a pair's directory, at unpair. The link lock is held.
func (d *Daemon) dropDirectoryLocked(id string) {
	delete(d.link.directories, id)
}

// announceName sends this computer's new name to each paired computer in generation gen,
// which the setup took when it succeeded, best effort; the directory carries it too.
func (d *Daemon) announceName(gen uint64) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return
	}
	for _, p := range s.Pairs {
		if p.State != link.Paired {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*linkIOTimeout)
		if session, err := d.connectPair(ctx, gen, p); err == nil {
			session.call("name-update", linkNameUpdate{ID: p.ID, Name: s.Name}, "name-ok", nil)
			session.close()
		}
		cancel()
	}
}

// cacheStaleLocked says whether a pair's directory is not current: the link is closed, the
// directory is older than linkConnectedFor, or the pair is not observed as connected now. The
// link lock is held.
func (d *Daemon) cacheStaleLocked(p link.Pair, c linkCache, now int64) bool {
	if d.link.listener == nil || now-c.at > linkConnectedFor.Milliseconds() {
		return true
	}
	return d.link.observed[p.ID].State != "connected"
}

// remoteRows are the cached receivers of the paired computers as peer rows: name and address
// NAME@COMPUTER, with the time seen. A row is stale after linkConnectedFor without a new
// directory, or while the link is closed.
func (d *Daemon) remoteRows() []Peer {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if len(d.link.directories) == 0 {
		return nil
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return nil
	}
	now := d.linkNow().UnixMilli()
	var rows []Peer
	for _, p := range s.Pairs {
		c, ok := d.link.directories[p.ID]
		if !ok || c.pin != p.Pin || p.State != link.Paired {
			continue
		}
		stale := d.cacheStaleLocked(p, c, now)
		for _, e := range c.dir.Entries {
			rows = append(rows, remoteRow(p.Effective, e, c, stale))
		}
	}
	return rows
}

func remoteRow(computer string, e LinkEntry, c linkCache, stale bool) Peer {
	address := e.Name
	if e.Address != "" {
		address = e.Address
	}
	return Peer{Name: e.Name + "@" + computer, Address: address + "@" + computer, Family: e.Family, State: "active", Repository: e.Repository,
		Role: e.Role, Remote: true, Computer: computer, SeenAt: c.at, Stale: stale, DirectoryTruncated: c.dir.Truncated,
		ActivityState: e.Activity, ActivityAt: e.ActivityAt}
}

// remotePeerStatus answers peer_status for NAME@COMPUTER from the cached directory. It opens
// no connection.
func (d *Daemon) remotePeerStatus(to string) (PeerStatus, error) {
	name, computer, err := link.ParseAddress(to)
	if err != nil {
		return PeerStatus{}, ErrLinkAddressInvalid
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return PeerStatus{}, err
	}
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.Effective == computer })
	if i < 0 {
		return PeerStatus{}, ErrLinkComputerUnknown
	}
	p := s.Pairs[i]
	c, ok := d.link.directories[p.ID]
	if !ok || c.pin != p.Pin {
		return PeerStatus{}, ErrPeerNotFound
	}
	j := slices.IndexFunc(c.dir.Entries, func(e LinkEntry) bool { return e.Name == name || e.Address == name })
	if j < 0 {
		return PeerStatus{}, ErrPeerNotFound
	}
	e := c.dir.Entries[j]
	stale := d.cacheStaleLocked(p, c, d.linkNow().UnixMilli())
	unknown := PeerObservation{Reason: "remote_computer"}
	activity := PeerObservation{Known: e.Activity != "", Reason: "remote_computer", Source: "remote", At: e.ActivityAt, State: e.Activity,
		StaleAfterMS: linkConnectedFor.Milliseconds()}
	if activity.Known {
		activity.Reason = ""
	}
	return PeerStatus{Peer: remoteRow(p.Effective, e, c, stale), ObservedAt: c.at, Model: unknown, Context: unknown, Activity: activity}, nil
}

// remoteSend is a send to NAME@COMPUTER. The caller and the body are checked as for any send.
// An unknown COMPUTER is refused with no lookup and no connection. A known COMPUTER is refused
// with link_messages_pending: chunk 02 of #670 has no message queue; chunk 03 adds it. The
// maintainer sends only on this computer (LINK-670-WHAT item 5).
func (d *Daemon) remoteSend(ctx context.Context, caller *Key, to, body string) error {
	if body == "" || len(body) > maxBody || !utf8.ValidString(body) {
		return ErrInvalid
	}
	if caller == nil {
		return ErrLinkMaintainerRemote
	}
	if err := d.store.activeCaller(ctx, *caller, d.store.now().UnixMilli()); err != nil {
		return err
	}
	_, computer, err := link.ParseAddress(to)
	if err != nil {
		return ErrLinkAddressInvalid
	}
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.Effective == computer }) {
		return ErrLinkComputerUnknown
	}
	return ErrLinkMessagesPending
}
