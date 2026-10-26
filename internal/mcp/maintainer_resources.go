package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// The change notices of the maintainer mode (PDR 009, Decision 3). The resources are the
// maintainer's inbox, the decision board's waiting items and each room. While a resource is
// subscribed, one goroutine runs the daemon's long poll, POST /v1/maintainer/changes, and a
// change of a subscribed resource writes notifications/resources/updated with only its URI.
// The client reads the resource again, as the content-free wakes of agents work (ADR 001,
// Decision 3).

const (
	inboxURI     = core.MaintainerURI + "inbox"
	decisionsURI = core.MaintainerURI + "decisions"
	roomURI      = core.MaintainerURI + "rooms/"
)

// maintainerResource maps a resource URI to the route and body of the tool that reads it:
// inbox, board_list with the filter waiting, or room_read. A room ID is 32 lowercase hex
// characters, as the daemon makes it.
func maintainerResource(uri string) (string, map[string]any, bool) {
	switch {
	case uri == inboxURI:
		return "inbox", map[string]any{}, true
	case uri == decisionsURI:
		return "board-list", map[string]any{"filter": "waiting"}, true
	case strings.HasPrefix(uri, roomURI):
		id := uri[len(roomURI):]
		if _, err := hex.DecodeString(id); err != nil || len(id) != 32 || strings.ToLower(id) != id {
			return "", nil, false
		}
		return "room-read", map[string]any{"room_id": id}, true
	}
	return "", nil, false
}

// rpcRefusal is a JSON-RPC error that carries a refusal's code in its data.
type rpcRefusal struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

// refuse answers a resource request with err as a JSON-RPC error: -32002 when the resource
// does not exist, -32603 otherwise, with the refusal's code, text and details in data. It
// reports whether the code is maintainer_token_expired.
func (s *maintainerServer) refuse(id json.RawMessage, err error) bool {
	data := map[string]any{"code": code(err)}
	message := code(err)
	var refused core.RefusedError
	if errors.As(err, &refused) {
		if refused.Message != "" {
			data["error"], message = refusalText(refused.Message), refusalText(refused.Message)
		}
		if len(refused.Details) > 0 {
			data["details"] = refused.Details
		}
	}
	rpc := -32603
	if data["code"] == "unknown_resource" || data["code"] == "room_not_found" {
		rpc = -32002
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcRefusal{rpc, message, data}})
	return data["code"] == ErrTokenExpired.Code
}

// route calls one maintainer route with the token.
func (s *maintainerServer) route(ctx context.Context, route string, body map[string]any) (json.RawMessage, error) {
	request := maps.Clone(body)
	request["token"] = s.c.Token
	return s.daemon(ctx, "/v1/maintainer/"+route, request, 10*time.Second)
}

// listResources lists the inbox, the decision board and each room.
func (s *maintainerServer) listResources(ctx context.Context, id json.RawMessage) bool {
	data, err := s.route(ctx, "room-list", map[string]any{})
	var reply struct {
		Result struct {
			Rooms []struct {
				ID   string `json:"room_id"`
				Name string `json:"name"`
			} `json:"rooms"`
		} `json:"result"`
	}
	if err == nil && json.Unmarshal(data, &reply) != nil {
		err = errors.New("invalid daemon response")
	}
	if err != nil {
		return s.refuse(id, err)
	}
	resource := func(uri, name, description string) map[string]any {
		return map[string]any{"uri": uri, "name": name, "description": description, "mimeType": "application/json"}
	}
	resources := []map[string]any{
		resource(inboxURI, "inbox", "The maintainer's own inbox, as the inbox tool reads it."),
		resource(decisionsURI, "decisions", "The decision board's counts and waiting items, as board_list reads it."),
	}
	for _, room := range reply.Result.Rooms {
		resources = append(resources, resource(roomURI+room.ID, room.Name, "The room's retained posts, as room_read reads them."))
	}
	s.reply(id, map[string]any{"resources": resources})
	return false
}

// readResource answers with the JSON of the tool that reads the resource.
func (s *maintainerServer) readResource(ctx context.Context, id json.RawMessage, uri, route string, body map[string]any) bool {
	data, err := s.route(ctx, route, body)
	if err != nil {
		return s.refuse(id, err)
	}
	s.reply(id, map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": "application/json", "text": string(data)}}})
	return false
}

// subscribe reads the current versions before it answers, so a change after the answer is
// not lost, and starts the long poll if none runs. After the answer it writes one notice for
// the URI: a change between the client's last read and the subscription is in no baseline, so
// the client reads again. A room that does not exist is refused with room_not_found.
func (s *maintainerServer) subscribe(ctx context.Context, id json.RawMessage, uri string) bool {
	versions, err := s.changes(ctx, nil)
	if err == nil && strings.HasPrefix(uri, roomURI) && versions[uri] == "" {
		err = core.RefusedError{Code: "room_not_found", Message: "no room has that ID"}
	}
	if err != nil {
		return s.refuse(id, err)
	}
	s.subMu.Lock()
	s.subs[uri] = versions[uri]
	if s.poll == nil {
		poll, cancel := context.WithCancel(ctx)
		s.poll = &changePoll{cancel, make(chan struct{})}
		go s.pollChanges(poll, versions, s.poll.done)
	}
	s.subMu.Unlock()
	s.reply(id, map[string]any{})
	s.subMu.Lock()
	if _, ok := s.subs[uri]; ok {
		s.updated(uri)
	}
	s.subMu.Unlock()
	return false
}

// unsubscribe ends a subscription. The last one ends the long poll and joins it before the
// answer, so no poll starts and no notice is written after it.
func (s *maintainerServer) unsubscribe(id json.RawMessage, uri string) {
	s.subMu.Lock()
	delete(s.subs, uri)
	last := len(s.subs) == 0
	s.subMu.Unlock()
	if last {
		s.stopPoll()
	}
	s.reply(id, map[string]any{})
}

// stopPoll ends the long poll, if one runs, and waits for its goroutine.
func (s *maintainerServer) stopPoll() {
	s.subMu.Lock()
	poll := s.poll
	s.poll = nil
	s.subMu.Unlock()
	if poll != nil {
		poll.cancel()
		<-poll.done
	}
}

// changes calls the long poll with the cursor; without one, it returns the current versions
// at once.
func (s *maintainerServer) changes(ctx context.Context, cursor map[string]string) (map[string]string, error) {
	request := map[string]any{"token": s.c.Token, "wait_seconds": pollSeconds}
	if cursor != nil {
		request["cursor"] = cursor
	}
	data, err := s.daemon(ctx, "/v1/maintainer/changes", request, (pollSeconds+5)*time.Second)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Result struct {
			Cursor map[string]string `json:"cursor"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Result.Cursor == nil {
		return nil, errors.New("invalid daemon response")
	}
	return reply.Result.Cursor, nil
}

// pollChanges runs the long poll until ctx ends. A change of a subscribed URI writes its
// notice. maintainer_token_expired ends the server; another failure, such as a daemon that
// does not answer, waits and polls again.
func (s *maintainerServer) pollChanges(ctx context.Context, cursor map[string]string, done chan struct{}) {
	defer close(done)
	retry := time.Second
	for {
		versions, err := s.changes(ctx, cursor)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if code(err) == ErrTokenExpired.Code {
				s.expireOnce.Do(func() { close(s.expired) })
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(retry):
			}
			retry = min(2*retry, 30*time.Second)
			continue
		}
		retry, cursor = time.Second, versions
		s.notify(ctx, versions)
	}
}

// notify writes one notice for each subscribed URI whose version changed, with only the URI.
// It holds subMu while it writes, so no notice follows an unsubscribe's answer.
func (s *maintainerServer) notify(ctx context.Context, versions map[string]string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, uri := range slices.Sorted(maps.Keys(s.subs)) {
		if ctx.Err() != nil {
			return
		}
		if version := versions[uri]; version != s.subs[uri] {
			s.subs[uri] = version
			s.updated(uri)
		}
	}
}

// updated writes the notice of a URI, with only the URI. The caller holds subMu.
func (s *maintainerServer) updated(uri string) {
	s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/resources/updated", "params": map[string]any{"uri": uri}})
}
