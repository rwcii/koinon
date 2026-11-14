package core

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/remote"
)

// Introductions (LINK-GROUP-JOIN, #683). When a computer pairs through this computer's
// invitation, this computer introduces it to each of its other paired computers, with one new
// pair ID for each new pair:
//
//  1. The confirm reply of the join (confirm-ok) lists the other computers' names, tailnet
//     names, addresses, link ports and pins. The new computer stores these pairs and
//     acknowledges them with introduced; it accepts no other key from an introduction.
//  2. Then each other computer gets the new computer's details with introduce, from this
//     paired key only, and stores the pair with the same ID.
//
// The introductions wait in link.json, so a computer that is off gets its introduction when
// it answers again. A new computer that did not get the confirm reply asks again with
// pair-confirm at its monitor passes during introWindow; an introduction that it did not
// acknowledge in that time is dropped.

// introWindow is how long after a join the new computer asks for its introductions, and how
// long this computer keeps an introduction that the new computer did not acknowledge.
const introWindow = time.Hour

// linkIntroduce is one introduced computer and the ID of its new pair.
type linkIntroduce struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	DNS  string   `json:"dns"`
	IPs  []string `json:"ips"`
	Port int      `json:"port"`
	Pin  string   `json:"pin"`
}

// linkIntroduceOK says whether the receiver added the pair; false means that it was already
// paired with that key.
type linkIntroduceOK struct {
	Added bool `json:"added"`
}

// linkConfirmOK is the body of confirm-ok: the computers that this computer introduces the
// new computer to. A joiner before LINK-GROUP-JOIN does not read it.
type linkConfirmOK struct {
	Introductions []linkIntroduce `json:"introductions,omitempty"`
}

// linkIntroduced is the new computer's acknowledgement: the pair ID of its join, and the
// IDs of the introduced pairs that it holds. The others are dropped.
type linkIntroduced struct {
	ID  string   `json:"id"`
	IDs []string `json:"ids"`
}

// addIntrosLocked adds one introduction for each other paired computer, after pair i became
// paired through this computer's invitation. The link lock is held; the caller writes.
func addIntrosLocked(s *link.State, i int, now int64) error {
	for _, p := range s.Pairs {
		if p.ID == s.Pairs[i].ID || p.State != link.Paired || len(s.Intros) >= link.MaxIntros {
			continue
		}
		id, err := link.NewID()
		if err != nil {
			return err
		}
		s.Intros = append(s.Intros, link.Intro{ID: id, New: s.Pairs[i].ID, Old: p.ID, Created: now})
	}
	return nil
}

// introsFor returns the introductions that the new computer of pair id has not acknowledged,
// each with the other computer's details.
func introsFor(s link.State, id string) []linkIntroduce {
	var out []linkIntroduce
	for _, in := range s.Intros {
		if in.New != id || in.NewSent {
			continue
		}
		if j := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == in.Old && p.State == link.Paired }); j >= 0 {
			p := s.Pairs[j]
			out = append(out, linkIntroduce{ID: in.ID, Name: p.Advertised, DNS: p.DNSName, IPs: p.IPs, Port: p.Port, Pin: p.Pin})
		}
	}
	return out
}

// dropIntros removes the introductions of pair id, after an unpair or a cancel.
func dropIntros(s *link.State, id string) {
	s.Intros = slices.DeleteFunc(s.Intros, func(i link.Intro) bool { return i.New == id || i.Old == id })
}

// validIntroduce checks an introduced computer's fields; sender is the pin of the computer
// that introduces it.
func validIntroduce(in linkIntroduce, sender, own string) (string, bool) {
	name := link.NormalizeName(in.Name)
	if len(in.ID) != 32 || link.ValidateName(name) != nil || remote.NormalizeDNSName(in.DNS) != in.DNS || in.DNS == "" || !remote.ValidHTTPSPort(in.Port) ||
		!link.ValidPin(in.Pin) || in.Pin == sender || in.Pin == own {
		return "", false
	}
	if ips, ok := link.ValidAddresses(in.IPs); !ok || len(ips) != len(in.IPs) {
		return "", false
	}
	return name, true
}

// addIntroducedLocked adds the introduced pair in, from the paired computer via. It answers
// whether this computer now holds the pair with that ID: true when it added it, or held it
// already; false when another pair has that key or that ID. The link lock is held; the caller
// writes.
func addIntroducedLocked(s *link.State, via link.Pair, in linkIntroduce, own string, now int64) (added, held bool, err error) {
	name, ok := validIntroduce(in, via.Pin, own)
	if !ok {
		return false, false, ErrInvalid
	}
	if p, ok := s.PairByPin(in.Pin); ok {
		return false, p.ID == in.ID, nil
	}
	if slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.ID == in.ID }) {
		return false, false, nil
	}
	if len(s.Pairs) >= link.MaxPairs {
		return false, false, ErrLinkLimit
	}
	s.Pairs = append(s.Pairs, link.Pair{ID: in.ID, Advertised: name, Effective: s.FreeName(name, ""), Pin: in.Pin, DNSName: in.DNS, IPs: in.IPs,
		Port: in.Port, State: link.Paired, Role: link.Joiner, Created: now, Via: via.ID})
	return true, true, nil
}

// acceptIntroduce adds the pair that a paired computer introduces (introduce), in generation
// gen.
func (d *Daemon) acceptIntroduce(pin string, in linkIntroduce, gen uint64) (linkIntroduceOK, error) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return linkIntroduceOK{}, ErrLinkRemoteOff
	}
	id, err := d.linkIdentityLocked()
	if err != nil {
		return linkIntroduceOK{}, err
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return linkIntroduceOK{}, err
	}
	via := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.Pin == pin && p.State == link.Paired })
	if via < 0 {
		return linkIntroduceOK{}, ErrLinkNoPair
	}
	added, _, err := addIntroducedLocked(&s, s.Pairs[via], in, id.Pin, d.linkNow().Unix())
	if err != nil {
		return linkIntroduceOK{}, err
	}
	if added {
		if err := link.WriteState(d.root, s); err != nil {
			return linkIntroduceOK{}, err
		}
	}
	return linkIntroduceOK{Added: added}, nil
}

// storeIntroductions stores the introductions that the computer of pair viaID listed in its
// confirm reply, in generation gen, and acknowledges them on session. It returns the names
// of the computers whose pairs this computer holds now.
func (d *Daemon) storeIntroductions(session linkSession, gen uint64, viaID string, list []linkIntroduce) []string {
	if len(list) == 0 {
		return nil
	}
	if len(list) > link.MaxPairs {
		list = list[:link.MaxPairs]
	}
	d.link.mu.Lock()
	if d.link.gen != gen {
		d.link.mu.Unlock()
		return nil
	}
	id, err := d.linkIdentityLocked()
	if err != nil {
		d.link.mu.Unlock()
		return nil
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return nil
	}
	v := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == viaID && p.State == link.Paired })
	if v < 0 {
		d.link.mu.Unlock()
		return nil
	}
	via := s.Pairs[v]
	now := d.linkNow().Unix()
	ids, names, changed := []string{}, []string{}, false
	for _, in := range list {
		added, held, err := addIntroducedLocked(&s, via, in, id.Pin, now)
		if err != nil || !held {
			continue
		}
		changed = changed || added
		ids = append(ids, in.ID)
		names = append(names, s.Pairs[slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == in.ID })].Effective)
	}
	if changed && link.WriteState(d.root, s) != nil {
		d.link.mu.Unlock()
		return nil
	}
	d.link.mu.Unlock()
	// A lost acknowledgement is sent again at the next pass, from the same pairs.
	session.call("introduced", linkIntroduced{ID: viaID, IDs: ids}, "introduced-ok", nil)
	return names
}

// acceptIntroduced records the new computer's acknowledgement, in generation gen: each listed
// introduction goes to the other computer next, and each other one is dropped.
func (d *Daemon) acceptIntroduced(pin string, ack linkIntroduced, gen uint64) error {
	if len(ack.ID) != 32 || len(ack.IDs) > link.MaxIntros {
		return ErrInvalid
	}
	d.link.mu.Lock()
	if d.link.gen != gen {
		d.link.mu.Unlock()
		return ErrLinkRemoteOff
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return err
	}
	if !slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.ID == ack.ID && p.Pin == pin && p.State == link.Paired }) {
		d.link.mu.Unlock()
		return ErrLinkNoPair
	}
	before := len(s.Intros)
	changed := false
	s.Intros = slices.DeleteFunc(s.Intros, func(in link.Intro) bool {
		return in.New == ack.ID && !in.NewSent && !slices.Contains(ack.IDs, in.ID)
	})
	for i := range s.Intros {
		if s.Intros[i].New == ack.ID && !s.Intros[i].NewSent {
			s.Intros[i].NewSent, changed = true, true
		}
	}
	if changed || len(s.Intros) != before {
		err = link.WriteState(d.root, s)
	}
	d.link.mu.Unlock()
	if err == nil {
		d.linkIntroKick()
	}
	return err
}

// linkIntroKick starts the delivery of the acknowledged introductions, or asks the running
// delivery for one more round.
func (d *Daemon) linkIntroKick() {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.listener == nil || d.link.life == nil {
		return
	}
	if d.link.introducing {
		d.link.introAgain = true
		return
	}
	d.link.introducing = true
	gen := d.link.gen
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			d.deliverIntros(gen)
			d.link.mu.Lock()
			if d.link.introAgain && d.link.gen == gen {
				d.link.introAgain = false
				d.link.mu.Unlock()
				continue
			}
			d.link.introducing, d.link.introAgain = false, false
			d.link.mu.Unlock()
			return
		}
	}()
}

// deliverIntros gives each acknowledged introduction of generation gen to the other
// computer. One that the other computer stored, or refused, is removed; one that it did not
// answer stays for a later pass.
func (d *Daemon) deliverIntros(gen uint64) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return
	}
	for _, in := range s.Intros {
		if !in.NewSent {
			continue
		}
		if _, err := d.linkLifeOf(gen); err != nil {
			return
		}
		fresh, old, ok := d.introPairs(gen, in.ID)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*linkIOTimeout)
		session, err := d.connectPair(ctx, gen, old)
		if err != nil {
			cancel()
			continue
		}
		var reply linkIntroduceOK
		err = session.call("introduce", linkIntroduce{ID: in.ID, Name: fresh.Advertised, DNS: fresh.DNSName, IPs: fresh.IPs, Port: fresh.Port, Pin: fresh.Pin}, "introduce-ok", &reply)
		session.close()
		cancel()
		var refusal Refusal
		if err == nil || errors.As(err, &refusal) && refusal.Code != ErrLinkRemoteOff.Code {
			d.removeIntro(gen, in.ID)
		}
	}
}

// introPairs reads introduction id's two pairs now, in generation gen. An introduction whose
// pair is gone is removed.
func (d *Daemon) introPairs(gen uint64, id string) (link.Pair, link.Pair, bool) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return link.Pair{}, link.Pair{}, false
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return link.Pair{}, link.Pair{}, false
	}
	i := slices.IndexFunc(s.Intros, func(in link.Intro) bool { return in.ID == id })
	if i < 0 {
		return link.Pair{}, link.Pair{}, false
	}
	pair := func(id string) (link.Pair, bool) {
		j := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == id && p.State == link.Paired })
		if j < 0 {
			return link.Pair{}, false
		}
		return s.Pairs[j], true
	}
	fresh, okNew := pair(s.Intros[i].New)
	old, okOld := pair(s.Intros[i].Old)
	if !okNew || !okOld {
		s.Intros = slices.Delete(s.Intros, i, i+1)
		link.WriteState(d.root, s)
		return link.Pair{}, link.Pair{}, false
	}
	return fresh, old, true
}

// removeIntro removes introduction id in generation gen.
func (d *Daemon) removeIntro(gen uint64, id string) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return
	}
	if i := slices.IndexFunc(s.Intros, func(in link.Intro) bool { return in.ID == id }); i >= 0 {
		s.Intros = slices.Delete(s.Intros, i, i+1)
		link.WriteState(d.root, s)
	}
}

// LinkLeaveResult counts the pairs that a leave removed.
type LinkLeaveResult struct {
	Unpaired int `json:"unpaired"`
}

// linkLeave removes every pair of this computer, and the introductions, in one write; then
// it tells each other computer when it answers, as an unpair does.
func (d *Daemon) linkLeave(ctx context.Context) (LinkLeaveResult, error) {
	d.link.mu.Lock()
	gen := d.link.gen
	s, err := link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return LinkLeaveResult{}, err
	}
	pairs := s.Pairs
	s.Pairs, s.Intros = nil, nil
	for _, p := range pairs {
		delete(d.link.observed, p.ID)
		d.dropDirectoryLocked(p.ID)
	}
	err = link.WriteState(d.root, s)
	d.link.mu.Unlock()
	if err != nil {
		return LinkLeaveResult{}, err
	}
	var errs []error
	for _, p := range pairs {
		d.closeLinkPin(p.Pin)
		if err := d.linkRetirePair(p.ID); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range pairs {
		d.notifyUnpair(ctx, gen, p)
	}
	return LinkLeaveResult{Unpaired: len(pairs)}, errors.Join(errs...)
}

// askIntroductions asks the computer whose invitation this one joined, during introWindow
// after the join, for the introductions that it did not acknowledge: the join's confirm
// reply or its acknowledgement can be lost. A computer before LINK-GROUP-JOIN answers with
// none.
func (d *Daemon) askIntroductions(session linkSession, gen uint64, p link.Pair) {
	if p.Role != link.Joiner || p.Via != "" || d.linkNow().Unix()-p.Created >= int64(introWindow/time.Second) {
		return
	}
	var confirm linkConfirmOK
	if session.call("pair-confirm", linkPairID{ID: p.ID}, "confirm-ok", &confirm) == nil {
		d.storeIntroductions(session, gen, p.ID, confirm.Introductions)
	}
}
