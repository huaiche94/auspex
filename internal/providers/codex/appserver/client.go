package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// maxLineBytes bounds one wire line. turn/diff/updated notifications can
// carry whole-file diffs, so this is generous; a line beyond it is a
// protocol-health failure surfaced by Wait/ReadErr, never a silent
// truncation.
const maxLineBytes = 16 << 20

// notificationBuffer is the dispatch channel depth. The read loop must
// never block on a slow consumer (a blocked read loop would also stall
// request/response correlation), so past this depth notifications are
// DROPPED and counted (Stats.NotificationsDropped) — fail-open with a
// disclosed metric, per ADD §21.7's tolerance discipline.
const notificationBuffer = 256

// Frame is one raw incoming JSON-RPC object, minimally parsed for
// routing. Payload fields stay raw JSON — typed decoding is the
// consumer's explicit choice (privacy: nothing is eagerly decoded).
type Frame struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("appserver: rpc error %d: %s", e.Code, e.Message)
}

// Notification is a server-initiated method call without an id.
type Notification struct {
	Method string
	Params json.RawMessage
}

// ServerRequest is a server-initiated call WITH an id (e.g. an approval
// ask). The consumer must answer via Client.RespondTo, or the server may
// stall the turn.
type ServerRequest struct {
	ID     int64
	Method string
	Params json.RawMessage
}

// Stats are the client's fail-open health counters (ADD §21.7: unknown
// input is tolerated and METRICED, never fatal).
type Stats struct {
	UnknownFrames         int64 // lines that were valid JSON but routable to nothing
	MalformedLines        int64 // lines that were not valid JSON objects
	NotificationsDropped  int64 // dispatch-buffer overflow drops
	ServerRequestsDropped int64 // ditto for the server-request channel
}

// Client is a Codex App Server JSON-RPC connection. Construct with Dial
// (spawns `codex app-server`) or Attach (caller-supplied transport, used
// by tests and any future daemon-proxy mode). Safe for concurrent Calls.
type Client struct {
	w  io.Writer
	wc io.Closer // transport write side, closed on Close (nil when Attach got no closer)

	cmd *exec.Cmd // non-nil only when Dial spawned the process

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan Frame
	// closed gates new work: set by Close AND by the read loop ending
	// (a dead server must fail new Calls fast, not block them on a
	// write nobody will read). tornDown makes Close itself idempotent.
	closed   bool
	tornDown bool

	// wmu serializes transport writes and is NEVER held together with
	// mu: a transport Write can block (full stdin pipe, unread io.Pipe),
	// and Close must still be able to take mu and close the transport —
	// which is exactly what unblocks the stuck Write.
	wmu sync.Mutex

	notifications  chan Notification
	serverRequests chan ServerRequest

	readDone chan struct{}
	readErr  error // set before readDone closes

	unknownFrames         atomic.Int64
	malformedLines        atomic.Int64
	notificationsDropped  atomic.Int64
	serverRequestsDropped atomic.Int64
}

// Dial spawns `codex app-server` (argv-only — Constitution §7 rule 5)
// and attaches to its stdio. bin is the binary to spawn ("codex" in
// production; tests pass a fake). The returned client owns the process:
// Close kills it if it has not exited.
func Dial(ctx context.Context, bin string, extraArgs ...string) (*Client, error) {
	args := append([]string{"app-server"}, extraArgs...)
	cmd := exec.CommandContext(ctx, bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("appserver: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("appserver: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("appserver: spawn %s app-server: %w", bin, err)
	}
	c := newClient(stdout, stdin, stdin)
	c.cmd = cmd
	return c, nil
}

// Attach builds a client over a caller-supplied transport (tests, future
// proxy modes). closer may be nil; it is closed on Close.
func Attach(r io.Reader, w io.Writer, closer io.Closer) *Client {
	return newClient(r, w, closer)
}

func newClient(r io.Reader, w io.Writer, closer io.Closer) *Client {
	c := &Client{
		w:              w,
		wc:             closer,
		pending:        make(map[int64]chan Frame),
		notifications:  make(chan Notification, notificationBuffer),
		serverRequests: make(chan ServerRequest, notificationBuffer),
		readDone:       make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// Notifications delivers server notifications in arrival order. The
// channel closes when the connection ends.
func (c *Client) Notifications() <-chan Notification { return c.notifications }

// ServerRequests delivers server-initiated requests (id + method). The
// channel closes when the connection ends.
func (c *Client) ServerRequests() <-chan ServerRequest { return c.serverRequests }

// Stats returns the fail-open health counters observed so far.
func (c *Client) Stats() Stats {
	return Stats{
		UnknownFrames:         c.unknownFrames.Load(),
		MalformedLines:        c.malformedLines.Load(),
		NotificationsDropped:  c.notificationsDropped.Load(),
		ServerRequestsDropped: c.serverRequestsDropped.Load(),
	}
}

// ReadErr reports why the read loop ended (nil for clean EOF). Valid
// after the Notifications channel closes.
func (c *Client) ReadErr() error {
	select {
	case <-c.readDone:
		return c.readErr
	default:
		return nil
	}
}

// ErrClosed is returned by calls made after Close (or after the
// connection ended).
var ErrClosed = errors.New("appserver: connection closed")

// Call sends one correlated request and decodes its result into out
// (out may be nil to discard). It returns the server's *RPCError as the
// error when the response carries one.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	ch, id, err := c.send(method, params)
	if err != nil {
		return err
	}
	defer c.forget(id)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.readDone:
		return fmt.Errorf("appserver: connection ended awaiting %s response: %w", method, ErrClosed)
	case fr, ok := <-ch:
		if !ok {
			// Channel closed by the read loop's teardown — the
			// connection ended before a response arrived.
			return fmt.Errorf("appserver: connection ended awaiting %s response: %w", method, ErrClosed)
		}
		if fr.Error != nil {
			return fr.Error
		}
		if out == nil || len(fr.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(fr.Result, out); err != nil {
			return fmt.Errorf("appserver: decode %s result: %w", method, err)
		}
		return nil
	}
}

// Notify sends a fire-and-forget notification (no id, no response).
func (c *Client) Notify(method string, params any) error {
	return c.writeFrame(map[string]any{"jsonrpc": "2.0", "method": method, "params": normalizeParams(params)})
}

// RespondTo answers a ServerRequest. Exactly one of result/rpcErr should
// be non-nil.
func (c *Client) RespondTo(id int64, result any, rpcErr *RPCError) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	return c.writeFrame(msg)
}

// Close tears the connection down: write side closed, spawned process
// (if any) killed unless already exited, read loop drained. Idempotent,
// and still performs teardown when the connection already ended on its
// own (closed set by the read loop ≠ transport torn down).
func (c *Client) Close() error {
	c.mu.Lock()
	if c.tornDown {
		c.mu.Unlock()
		return nil
	}
	c.tornDown = true
	c.closed = true
	c.mu.Unlock()

	var errs []error
	if c.wc != nil {
		if err := c.wc.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.cmd != nil {
		// Closing stdin asks the server to exit; kill covers a server
		// that ignores it. Wait reaps either way.
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	<-c.readDone
	return errors.Join(errs...)
}

func (c *Client) send(method string, params any) (chan Frame, int64, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, 0, ErrClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan Frame, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	err := c.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": normalizeParams(params)})
	if err != nil {
		c.forget(id)
		return nil, 0, err
	}
	return ch, id, nil
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) writeFrame(msg map[string]any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("appserver: encode frame: %w", err)
	}
	body = append(body, '\n')
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(body); err != nil {
		return fmt.Errorf("appserver: write frame: %w", err)
	}
	return nil
}

// normalizeParams keeps "params" present-and-object for nil params —
// the shape the codex server accepts for parameterless methods like
// `initialized`.
func normalizeParams(params any) any {
	if params == nil {
		return map[string]any{}
	}
	return params
}

func (c *Client) readLoop(r io.Reader) {
	defer func() {
		close(c.notifications)
		close(c.serverRequests)
		// Fail every in-flight call so no Call blocks forever, and gate
		// new work: writing to a server that will never read again must
		// fail fast, not block on a full pipe.
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			delete(c.pending, id)
			close(ch)
		}
		c.mu.Unlock()
		close(c.readDone)
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var fr Frame
		if err := json.Unmarshal(line, &fr); err != nil {
			c.malformedLines.Add(1)
			continue
		}
		switch {
		case fr.ID != nil && fr.Method != "":
			// Server-initiated request (approval asks etc.).
			select {
			case c.serverRequests <- ServerRequest{ID: *fr.ID, Method: fr.Method, Params: fr.Params}:
			default:
				c.serverRequestsDropped.Add(1)
			}
		case fr.ID != nil:
			c.mu.Lock()
			ch, ok := c.pending[*fr.ID]
			if ok {
				delete(c.pending, *fr.ID)
			}
			c.mu.Unlock()
			if !ok {
				c.unknownFrames.Add(1) // response to nothing we asked
				continue
			}
			ch <- fr
		case fr.Method != "":
			select {
			case c.notifications <- Notification{Method: fr.Method, Params: fr.Params}:
			default:
				c.notificationsDropped.Add(1)
			}
		default:
			c.unknownFrames.Add(1)
		}
	}
	c.readErr = sc.Err()
}
