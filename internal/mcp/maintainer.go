package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/signed"
)

// The maintainer mode of koinon mcp (PDR 009, Decision 3): a stdio server that acts as the
// built-in maintainer session with a maintainer token. It registers no session, renews and
// observes nothing, and is never in peers; it has its own tools, and the agent tools stay as
// they are. Each action records this client as its via (Decision 4).

// MaintainerConfig is a maintainer-mode server's daemon, token and client.
type MaintainerConfig struct {
	StateDir string
	Address  string
	// Resolve gives the daemon address for each call, as Config.Resolve does (#549); nil
	// keeps Address.
	Resolve func() (string, error)
	// Token is the maintainer token from MaintainerSession; Client is the --client name.
	Token   string
	Client  string
	Version string
	// Signed is a signed session (#693; Decision 5.3 of the signed maintainer contract): the
	// server has the one tool signed, no resources and no notices, and Token is the session's.
	Signed bool
}

// ErrTokenExpired ends a maintainer-mode server after it answered a call with
// maintainer_token_expired, so the client starts a new one.
var ErrTokenExpired = core.RefusedError{Code: "maintainer_token_expired", Message: "the maintainer token has ended; start koinon mcp --maintainer again"}

// MaintainerSession exchanges the maintainer secret for a maintainer token for client.
func MaintainerSession(ctx context.Context, stateDir, address, secret, client string) (string, error) {
	bearer, err := core.ClientSecret(stateDir, address)
	if err != nil {
		return "", err
	}
	data, err := core.Call(ctx, address, bearer, "/v1/maintainer/session", map[string]any{"secret": secret, "client": client})
	if err != nil {
		return "", err
	}
	var reply struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Token == "" {
		return "", errors.New("invalid daemon response")
	}
	return reply.Token, nil
}

var (
	verdictField = map[string]any{"type": "string", "enum": []string{"approved", "declined", "feedback", "completed", "acknowledged"}}
	boardFilter  = map[string]any{"type": "string", "enum": []string{"waiting", "active", "returned", "in_progress", "archived"}}
	pageLimit    = map[string]any{"type": "integer", "minimum": 0, "maximum": 100}
)

// SignedSession opens a signed maintainer session with a session envelope, as the client
// signed it. It returns the token and the client, key:CLIENT.
func SignedSession(ctx context.Context, stateDir, address string, envelope []byte) (string, string, error) {
	bearer, err := core.ClientSecret(stateDir, address)
	if err != nil {
		return "", "", err
	}
	data, err := core.CallEnvelope(ctx, address, bearer, "/v1/maintainer/signed/session", envelope, "", 10*time.Second)
	if err != nil {
		return "", "", err
	}
	var reply struct {
		Token  string `json:"token"`
		Client string `json:"client"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Token == "" {
		return "", "", errors.New("invalid daemon response")
	}
	return reply.Token, reply.Client, nil
}

// signedTools is the one tool of a signed session.
var signedTools = []map[string]any{
	{"name": "signed", "description": "Run one signed maintainer action: envelope is the envelope that the maintainer client signed, as a JSON object (or as a string of its JSON text). The answer is the target's answer unchanged: {\"ok\": true, \"result\": ...} or {\"ok\": false, \"code\", \"error\", \"details\"}; for a koinon-maintainer-2 envelope, the signed answer object (v koinon-answer-2) that carries it, in each status.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"envelope": map[string]any{"type": []string{"object", "string"}}},
			"required": []string{"envelope"}, "additionalProperties": false}},
}

// maintainerTools are the tools of the maintainer mode. Each one calls the daemon route with
// its name, with - for _, under /v1/maintainer/.
var maintainerTools = []map[string]any{
	{"name": "board_list", "description": "Read the decision board as the maintainer: the counts of waiting, returned, in-progress, open fyi and archived items, and one page of item summaries for a filter (waiting by default). Page with next_after.",
		"inputSchema": object(map[string]any{"filter": boardFilter, "after": integer, "limit": pageLimit})},
	{"name": "decision_get", "description": "Read one decision item by reference with its full text, recipients, current revision and current verdict.",
		"inputSchema": object(map[string]any{"reference": text}, "reference")},
	{"name": "decision_history", "description": "Read an item's verdicts in order, each with the text that the maintainer saw and via, the client that gave it (dashboard or mcp:NAME). Page with next_after.",
		"inputSchema": object(map[string]any{"reference": text, "after": integer, "limit": map[string]any{"type": "integer", "minimum": 0, "maximum": 16}}, "reference")},
	{"name": "decision_verdict", "description": "Give the maintainer's verdict on an item at the revision that decision_get showed: approved, declined or feedback for a decision item; completed, declined or feedback for a task; acknowledged for an fyi item. The note is optional, except for feedback, where it holds the maintainer's guidance; feedback approves and declines nothing and returns the item to its author for a new revision. stale_revision means the item changed; read it again. Each named agent and the item's author get the verdict.",
		"inputSchema": object(map[string]any{"reference": text, "revision": integer, "verdict": verdictField, "note": text}, "reference", "revision", "verdict")},
	{"name": "inbox", "description": "Read the maintainer's own inbox after a sequence number: the messages that agents sent to the maintainer. Reading acknowledges nothing.",
		"inputSchema": object(map[string]any{"after": integer, "limit": pageLimit})},
	{"name": "ack", "description": "Acknowledge the maintainer's inbox through a sequence number.",
		"inputSchema": object(map[string]any{"through": integer}, "through")},
	{"name": "send", "description": "Send a message as the maintainer to an agent session by its peer name or alias. The agent reads it as the maintainer's instruction (PDR 005).",
		"inputSchema": object(map[string]any{"to": text, "body": text}, "to", "body")},
	{"name": "room_list", "description": "List every room with its roster revision and current and former members, each with its ordinal.",
		"inputSchema": object(map[string]any{})},
	{"name": "room_read", "description": "Read a room's retained posts after a sequence, in order, with sender, typed origin and time. Page with next_after; gaps name sequences that retention no longer holds.",
		"inputSchema": object(map[string]any{"room_id": text, "after": integer, "limit": pageLimit}, "room_id")},
	{"name": "room_post", "description": "Post to a room as the maintainer. It reaches every active member. key and deadline (Unix seconds) make a retry safe.",
		"inputSchema": object(map[string]any{"room_id": text, "body": text, "key": text, "deadline": retryDeadline}, "room_id", "body", "key", "deadline")},
	{"name": "room_create", "description": "Create an empty room with a name. key makes a retry return the same room.",
		"inputSchema": object(map[string]any{"name": text, "key": text}, "name", "key")},
	{"name": "room_add", "description": "Add an active session to a room by its peer name: as the participant whose address it holds, otherwise as that session.",
		"inputSchema": object(map[string]any{"room_id": text, "peer": text}, "room_id", "peer")},
	{"name": "room_remove", "description": "Remove a current member from a room by the member's ordinal from room_list.",
		"inputSchema": object(map[string]any{"room_id": text, "ordinal": integer}, "room_id", "ordinal")},
	{"name": "peers", "description": "List the agent sessions that Koinon knows: peer name, alias, family, state and repository.",
		"inputSchema": object(map[string]any{})},
	{"name": "peer_status", "description": "Read a peer's observed model, context and activity by published name or held alias.",
		"inputSchema": object(map[string]any{"peer": text}, "peer")},
}

type maintainerServer struct {
	c      MaintainerConfig
	out    *json.Encoder
	outMu  sync.Mutex
	mu     sync.Mutex
	bearer string
	// subMu guards the change notices: subs holds each subscribed URI with the version that
	// the client last learned of, "" for a deleted room, and poll is the running long poll.
	subMu sync.Mutex
	subs  map[string]string
	poll  *changePoll
	// expired is closed when the long poll reads maintainer_token_expired.
	expired    chan struct{}
	expireOnce sync.Once
	// calls counts a signed session's tool calls in flight, and slots bounds them.
	calls sync.WaitGroup
	slots chan struct{}
}

// maxSignedCalls bounds a signed session's tool calls in flight: a client keeps one changes
// wait per target, at most link.MaxPairs + 1, with room for as many actions besides.
const maxSignedCalls = core.MaxSignedCalls

// changePoll is a running long poll and the means to end and join it.
type changePoll struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// pollSeconds is the wait of each long poll; the daemon accepts 1 to 30 seconds.
const pollSeconds = 25

// ServeMaintainer runs the maintainer-mode server until the input ends, ctx is cancelled or
// the token ends. After it answers a request with maintainer_token_expired, or the long poll
// of its change notices reads that code, it returns ErrTokenExpired, also while the input is
// open and idle. A request in progress gets its reply first. The long poll ends before
// ServeMaintainer returns, so nothing is written after it.
func ServeMaintainer(ctx context.Context, c MaintainerConfig, in io.Reader, out io.Writer) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &maintainerServer{c: c, out: json.NewEncoder(out), subs: map[string]string{}, expired: make(chan struct{}), slots: make(chan struct{}, maxSignedCalls)}
	defer s.stopPoll()
	// A signed session's calls in flight write their replies before the server returns, and
	// an expiry that one of them reads, also after the end of the input, is the result.
	defer func() {
		s.calls.Wait()
		select {
		case <-s.expired:
			if err == nil {
				err = ErrTokenExpired
			}
		default:
		}
	}()
	type input struct {
		line []byte
		err  error
	}
	lines := make(chan input)
	go func() {
		reader := bufio.NewReaderSize(in, 64*1024)
		for {
			line, err := readLine(reader)
			select {
			case lines <- input{line, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.expired:
			return ErrTokenExpired
		case read := <-lines:
			if len(bytes.TrimSpace(read.line)) > 0 && s.handle(ctx, read.line) {
				return ErrTokenExpired
			}
			if errors.Is(read.err, io.EOF) {
				return nil
			}
			if read.err != nil {
				return read.err
			}
		}
	}
}

func (s *maintainerServer) send(value any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Encode(value)
}

func (s *maintainerServer) reply(id json.RawMessage, result any) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *maintainerServer) fail(id json.RawMessage, code int, message string) {
	if id == nil {
		id = json.RawMessage("null")
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{code, message}})
}

// handle answers one message and reports whether the answer was maintainer_token_expired.
func (s *maintainerServer) handle(ctx context.Context, line []byte) bool {
	var r rpcRequest
	if err := json.Unmarshal(line, &r); err != nil {
		s.fail(nil, -32700, "parse error")
		return false
	}
	if r.JSONRPC != "2.0" || r.Method == "" {
		s.fail(r.ID, -32600, "invalid request")
		return false
	}
	if r.ID == nil {
		return false // A notification needs no answer.
	}
	switch r.Method {
	case "initialize":
		s.initialize(r)
	case "ping":
		s.reply(r.ID, map[string]any{})
	case "tools/list":
		if s.c.Signed {
			s.reply(r.ID, map[string]any{"tools": signedTools})
		} else {
			s.reply(r.ID, map[string]any{"tools": maintainerTools})
		}
	case "tools/call":
		if s.c.Signed {
			s.signedAsync(ctx, r)
			return false
		}
		result, expired := s.call(ctx, r.Params)
		s.reply(r.ID, result)
		return expired
	case "resources/list", "resources/read", "resources/subscribe", "resources/unsubscribe":
		if s.c.Signed {
			// A signed session learns of changes with signed changes waits (Decision 6).
			s.fail(r.ID, -32601, "method not found")
			return false
		}
		return s.resources(ctx, r)
	default:
		s.fail(r.ID, -32601, "method not found")
	}
	return false
}

// resources answers the resource methods of a secret session.
func (s *maintainerServer) resources(ctx context.Context, r rpcRequest) bool {
	switch r.Method {
	case "resources/list":
		return s.listResources(ctx, r.ID)
	case "resources/read", "resources/subscribe", "resources/unsubscribe":
		var p struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(r.Params, &p) != nil {
			s.fail(r.ID, -32602, "invalid params")
			return false
		}
		route, body, known := maintainerResource(p.URI)
		if !known {
			s.refuse(r.ID, core.RefusedError{Code: "unknown_resource", Message: "no maintainer resource has this URI"})
			return false
		}
		switch r.Method {
		case "resources/read":
			return s.readResource(ctx, r.ID, p.URI, route, body)
		case "resources/subscribe":
			return s.subscribe(ctx, r.ID, p.URI)
		}
		s.unsubscribe(r.ID, p.URI)
	}
	return false
}

func (s *maintainerServer) initialize(r rpcRequest) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(r.Params, &p) != nil {
		s.fail(r.ID, -32602, "invalid params")
		return
	}
	version := protocolVersions[0]
	for _, v := range protocolVersions {
		if v == p.ProtocolVersion {
			version = v
		}
	}
	release := s.c.Version
	if release == "" {
		release = "dev"
	}
	if s.c.Signed {
		s.reply(r.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "koinon", "version": release},
			"instructions":    "Koinon signed maintainer session: the tool signed runs one action that the maintainer client signed with its own key, as the maintainer, with the weight of the dashboard (PDR 005, Decision 3). The target computer checks the envelope and records the user and client of the key's authorization. Learn of changes with signed changes waits. When the session ends, a call answers maintainer_token_expired and this server exits; sign a new session envelope and start koinon mcp --maintainer --signed again.",
		})
		return
	}
	s.reply(r.ID, map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{"subscribe": true}},
		"serverInfo":      map[string]any{"name": "koinon", "version": release},
		"instructions": "Koinon maintainer mode: every tool acts as the maintainer, with the weight of the dashboard (PDR 005, Decision 3), and each action records mcp:" + s.c.Client +
			" as its via (PDR 009, Decision 4). Read an item with decision_get and give decision_verdict that revision. Subscribe to the resources koinon://maintainer/inbox, koinon://maintainer/decisions and koinon://maintainer/rooms/{room_id}: a change sends notifications/resources/updated with only the URI; read the resource again. When the maintainer token ends, a request answers maintainer_token_expired and this server exits; start koinon mcp --maintainer again.",
	})
}

// call forwards one tool call to its maintainer route with the token. It reports whether the
// daemon answered maintainer_token_expired.
func (s *maintainerServer) call(ctx context.Context, raw json.RawMessage) (map[string]any, bool) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return failure("invalid_arguments"), false
	}
	var schema map[string]any
	for _, tool := range maintainerTools {
		if tool["name"] == p.Name {
			schema = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		return failure("unknown_tool"), false
	}
	properties := schema["properties"].(map[string]any)
	body := map[string]any{}
	for name, value := range p.Arguments {
		if _, known := properties[name]; !known {
			return invalidArguments("unknown argument " + name), false
		}
		body[name] = value
	}
	required, _ := schema["required"].([]string)
	for _, name := range required {
		if _, given := p.Arguments[name]; !given {
			return invalidArguments("missing argument " + name), false
		}
	}
	// The token is this server's own; a call never supplies it.
	body["token"] = s.c.Token
	data, err := s.daemon(ctx, "/v1/maintainer/"+strings.ReplaceAll(p.Name, "_", "-"), body, 10*time.Second)
	if err != nil {
		return refusal(core.Key{Family: "maintainer"}, err), code(err) == ErrTokenExpired.Code
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}, "isError": false}, false
}

func invalidArguments(text string) map[string]any {
	return result(map[string]any{"ok": false, "code": "invalid_arguments", "error": refusalText(text)}, true)
}

// signedAsync runs one signed tool call in its own goroutine, so that a changes wait does not
// hold the session's other calls (Decisions 5.3 and 6.2 of the signed maintainer contract). A
// call beyond maxSignedCalls is refused at once with nothing sent. maintainer_token_expired
// from any call ends the server after the calls in flight have answered.
func (s *maintainerServer) signedAsync(ctx context.Context, r rpcRequest) {
	select {
	case s.slots <- struct{}{}:
	default:
		s.reply(r.ID, result(map[string]any{"ok": false, "code": "too_many_calls",
			"error": "too many calls of this session are in flight; send it again when one has answered", "details": map[string]any{"limit": maxSignedCalls}}, true))
		return
	}
	s.calls.Add(1)
	go func() {
		defer s.calls.Done()
		defer func() { <-s.slots }()
		answer, expired := s.signedCall(ctx, r.Params)
		s.reply(r.ID, answer)
		if expired {
			s.expireOnce.Do(func() { close(s.expired) })
		}
	}()
}

// signedCall runs the signed tool: it takes the envelope out of the call as the client wrote
// it and posts those bytes with the session's token. It reports whether the daemon answered
// maintainer_token_expired.
func (s *maintainerServer) signedCall(ctx context.Context, raw json.RawMessage) (map[string]any, bool) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return failure("invalid_arguments"), false
	}
	if p.Name != "signed" {
		return failure("unknown_tool"), false
	}
	for name := range p.Arguments {
		if name != "envelope" {
			return invalidArguments("unknown argument " + name), false
		}
	}
	value := bytes.TrimSpace(p.Arguments["envelope"])
	var envelope []byte
	switch {
	case len(value) > 0 && value[0] == '{':
		envelope = value
	case len(value) > 0 && value[0] == '"':
		// The string form: what decoding the string would replace silently is refused first.
		if signed.ValidText(value) != nil {
			return invalidArguments("envelope is not valid text"), false
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			return invalidArguments("envelope is not a JSON string"), false
		}
		envelope = []byte(text)
	default:
		return invalidArguments("missing argument envelope: a JSON object or a string of its JSON text"), false
	}
	data, err := s.withBearer(func(address, bearer string) (json.RawMessage, error) {
		// A forwarded changes wait answers within the node's write deadline; this is longer.
		return core.CallEnvelope(ctx, address, bearer, "/v1/maintainer/signed", envelope, s.c.Token, core.SignedCallTimeout)
	})
	if err != nil {
		return refusal(core.Key{Family: "maintainer"}, err), code(err) == ErrTokenExpired.Code
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}, "isError": false}, false
}

// daemon calls a maintainer route with the daemon's bearer secret and a time limit, reading
// the secret again after a failure so that a recreated state root is picked up.
func (s *maintainerServer) daemon(ctx context.Context, path string, body any, timeout time.Duration) (json.RawMessage, error) {
	return s.withBearer(func(address, bearer string) (json.RawMessage, error) {
		return core.CallTimeout(ctx, address, bearer, path, body, timeout)
	})
}

// withBearer runs one daemon call with the daemon's address and bearer secret.
func (s *maintainerServer) withBearer(call func(address, bearer string) (json.RawMessage, error)) (json.RawMessage, error) {
	address := s.c.Address
	if s.c.Resolve != nil {
		var err error
		if address, err = s.c.Resolve(); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	bearer := s.bearer
	s.mu.Unlock()
	if bearer == "" {
		var err error
		if bearer, err = core.ReadSecret(s.c.StateDir); err != nil {
			return nil, core.ErrUnavailable
		}
		s.mu.Lock()
		s.bearer = bearer
		s.mu.Unlock()
	}
	data, err := call(address, bearer)
	var refused core.RefusedError
	if errors.Is(err, core.ErrUnavailable) || errors.As(err, &refused) && refused.Code == "unauthorized" {
		s.mu.Lock()
		s.bearer = ""
		s.mu.Unlock()
	}
	return data, err
}

// maintainerToolNames lists the maintainer tools by name, for tests and docs checks.
func maintainerToolNames() []string {
	names := []string{}
	for _, tool := range maintainerTools {
		names = append(names, tool["name"].(string))
	}
	slices.Sort(names)
	return names
}
