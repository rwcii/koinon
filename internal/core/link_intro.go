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
//  1. In the join's exchange, after pair-confirm, the new computer asks with introductions.
//     The first ask makes the introductions; the reply lists the other computers' names,
//     tailnet names, addresses, link ports and pins. The new computer stores these pairs and
//     acknowledges them with introduced; it accepts no other key from that list.
//  2. Then each other computer gets the new computer's details with introduce, from this
//     paired key only, and stores the pair with the same ID.
//
// The introductions wait in link.json, so a computer that is off gets its introduction when
// it answers again. A new computer that has not got its introductions (asking) asks again
// after each successful ping, with no time limit; the ones that it already took or refused
// (seen) are never added again, so a repeated answer cannot undo an unpair.

// ErrLinkIntrosFull is an ask for introductions while the waiting ones leave no room for all
// of them; none is made, and the new computer asks again.
var ErrLinkIntrosFull = Refusal{"link_introductions_full", "the other computer holds the most introductions that can wait; they go as its paired computers answer, and this computer asks again"}

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

// linkIntroductions is the body of introductions-ok: the computers that this computer
// introduces the new computer to, that the new computer has not acknowledged.
type linkIntroductions struct {
	Introductions []linkIntroduce `json:"introductions"`
}

// linkIntroduced is the new computer's acknowledgement: the pair ID of its join, and the
// IDs of the introduced pairs that it holds. The others are dropped.
type linkIntroduced struct {
	ID  string   `json:"id"`
	IDs []string `json:"ids"`
}

// addIntrosLocked adds one introduction for each other paired computer of pair i, all or
// none, and marks pair i introduced. The link lock is held; the caller writes.
func addIntrosLocked(s *link.State, i int, now int64) error {
	var add []link.Intro
	for _, p := range s.Pairs {
		if p.ID == s.Pairs[i].ID || p.State != link.Paired {
			continue
		}
		id, err := link.NewID()
		if err != nil {
			return err
		}
		add = append(add, link.Intro{ID: id, New: s.Pairs[i].ID, Old: p.ID, Created: now})
	}
	if len(s.Intros)+len(add) > link.MaxIntros {
		return ErrLinkIntrosFull
	}
	s.Intros = append(s.Intros, add...)
	s.Pairs[i].Introduced = true
	return nil
}

// introsFor returns the introductions that the new computer of pair id has not acknowledged,
// each with the other computer's details.
func introsFor(s link.State, id string) []linkIntroduce {
	out := []linkIntroduce{}
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

// fenceIntros waits for an introduction that is being sent. Called after an unpair's
// write, it makes sure that no introduction of the removed pair goes after the unpair
// returns: the next one checks the state first.
func (d *Daemon) fenceIntros() {
	d.link.introSend.Lock()
	d.link.introSend.Unlock()
}

// answerIntroductions answers the new computer's ask, in generation gen: the first ask of a
// pair of this computer's invitation makes its introductions.
func (d *Daemon) answerIntroductions(pin, id string, gen uint64) ([]linkIntroduce, error) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return nil, ErrLinkRemoteOff
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool {
		return p.ID == id && p.Pin == pin && p.State == link.Paired && p.Role == link.Inviter
	})
	if i < 0 {
		return nil, ErrLinkNoPair
	}
	if !s.Pairs[i].Introduced {
		if err := addIntrosLocked(&s, i, d.linkNow().Unix()); err != nil {
			return nil, err
		}
		if err := link.WriteState(d.root, s); err != nil {
			return nil, err
		}
	}
	return introsFor(s, id), nil
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

// linkAsked is the result of one ask for introductions.
type linkAsked struct {
	names   []string
	waiting bool
	skipped int
}

// askIntroductions asks the computer of pair viaID, whose invitation this computer joined,
// for its introductions, stores them and acknowledges them, in generation gen. While the
// exchange does not finish, the pair stays asking, and the next monitor pass asks again. A
// computer before LINK-GROUP-JOIN does not know the operation: then this computer asks no
// more.
func (d *Daemon) askIntroductions(session linkSession, gen uint64, viaID string) linkAsked {
	var reply linkIntroductions
	err := session.call("introductions", linkPairID{ID: viaID}, "introductions-ok", &reply)
	var refusal Refusal
	if errors.As(err, &refusal) && refusal.Code == "link_operation_unknown" {
		d.finishAsking(gen, viaID)
		return linkAsked{}
	}
	if err != nil {
		return linkAsked{waiting: true}
	}
	asked, ids, ok := d.storeIntroductions(gen, viaID, reply.Introductions)
	if !ok {
		return linkAsked{waiting: true}
	}
	if len(reply.Introductions) > 0 {
		if session.call("introduced", linkIntroduced{ID: viaID, IDs: ids}, "introduced-ok", nil) != nil {
			asked.waiting = true
			return asked
		}
	}
	d.finishAsking(gen, viaID)
	return asked
}

// storeIntroductions stores the introductions that the computer of pair viaID listed, in
// generation gen, and returns the IDs of the ones that this computer holds. An introduction
// in the pair's seen list is never added again: it was taken, or refused, before.
func (d *Daemon) storeIntroductions(gen uint64, viaID string, list []linkIntroduce) (linkAsked, []string, bool) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen || len(list) > link.MaxIntros {
		return linkAsked{}, nil, false
	}
	own, err := d.linkIdentityLocked()
	if err != nil {
		return linkAsked{}, nil, false
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return linkAsked{}, nil, false
	}
	v := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == viaID && p.State == link.Paired && p.Asking })
	if v < 0 {
		return linkAsked{}, nil, false
	}
	via := s.Pairs[v]
	now := d.linkNow().Unix()
	var asked linkAsked
	ids := []string{}
	seen := slices.Clone(via.Seen)
	for _, in := range list {
		held := false
		if slices.Contains(seen, in.ID) {
			held = slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.ID == in.ID && p.Pin == in.Pin })
		} else {
			var err error
			_, held, err = addIntroducedLocked(&s, via, in, own.Pin, now)
			if errors.Is(err, ErrLinkLimit) {
				asked.skipped++
			}
			if len(seen) < link.MaxIntros {
				seen = append(seen, in.ID)
			}
		}
		if held {
			ids = append(ids, in.ID)
			asked.names = append(asked.names, s.Pairs[slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == in.ID })].Effective)
		}
	}
	v = slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == viaID })
	s.Pairs[v].Seen = seen
	if link.WriteState(d.root, s) != nil {
		return linkAsked{}, nil, false
	}
	return asked, ids, true
}

// finishAsking ends the asking of pair viaID in generation gen.
func (d *Daemon) finishAsking(gen uint64, viaID string) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return
	}
	if i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == viaID && p.Asking }); i >= 0 {
		s.Pairs[i].Asking, s.Pairs[i].Seen = false, nil
		link.WriteState(d.root, s)
	}
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
		err = d.sendIntroduce(session, gen, in.ID, fresh, old)
		session.close()
		cancel()
		var refusal Refusal
		if err == nil || errors.As(err, &refusal) && refusal.Code != ErrLinkRemoteOff.Code {
			d.removeIntro(gen, in.ID)
		}
	}
}

// errIntroGone is an introduction whose pair was removed before it was sent.
var errIntroGone = errors.New("link_introduction_gone")

// sendIntroduce sends introduction id of fresh to old and reads the answer. The check that
// the introduction and both pairs still exist, and the write, run under introSend, which an
// unpair takes after its write: an unpair that returned is never followed by this send.
func (d *Daemon) sendIntroduce(session linkSession, gen uint64, id string, fresh, old link.Pair) error {
	f, err := link.NewFrame("introduce", linkIntroduce{ID: id, Name: fresh.Advertised, DNS: fresh.DNSName, IPs: fresh.IPs, Port: fresh.Port, Pin: fresh.Pin})
	if err != nil {
		return err
	}
	d.link.introSend.Lock()
	now, nowOld, ok := d.introPairs(gen, id)
	if !ok || now.Pin != fresh.Pin || nowOld.Pin != old.Pin {
		d.link.introSend.Unlock()
		return errIntroGone
	}
	session.conn.SetDeadline(time.Now().Add(linkIOTimeout))
	err = link.WriteFrame(session.conn, f)
	d.link.introSend.Unlock()
	if err != nil {
		return err
	}
	reply, err := link.ReadFrame(session.conn)
	if err != nil {
		return err
	}
	if reply.Op == "error" {
		return Refusal{reply.Error, reply.Detail}
	}
	var ok2 linkIntroduceOK
	if reply.Op != "introduce-ok" || reply.Decode(&ok2) != nil {
		return errors.New("link_reply_invalid")
	}
	return nil
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
	i := slices.IndexFunc(s.Intros, func(in link.Intro) bool { return in.ID == id && in.NewSent })
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
	d.fenceIntros()
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
