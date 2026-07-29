package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServer is the in-process App Server stand-in: it owns the far end
// of two pipes and lets a test script the exchange line-by-line — the
// Constitution §5 rule 4 fixture harness (no live account, recorded
// frames only).
type fakeServer struct {
	t      *testing.T
	in     *bufio.Scanner // client -> server
	out    io.WriteCloser // server -> client
	client *Client
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	// The closer must cover the client's READ side too: Close waits for
	// the read loop, and an Attach transport has no process whose death
	// would EOF it — closing only the write side would deadlock Close
	// against a still-open read pipe.
	c := Attach(clientIn, clientOut, multiCloser{clientOut, clientIn})
	t.Cleanup(func() { _ = c.Close(); _ = serverOut.Close() })
	sc := bufio.NewScanner(serverIn)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	return &fakeServer{t: t, in: sc, out: serverOut, client: c}
}

// multiCloser closes every member, joining errors.
type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var errs []error
	for _, c := range m {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// recv reads one frame the client sent.
func (s *fakeServer) recv() map[string]any {
	s.t.Helper()
	if !s.in.Scan() {
		s.t.Fatalf("fake server: client closed the pipe before an expected frame (err=%v)", s.in.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(s.in.Bytes(), &m); err != nil {
		s.t.Fatalf("fake server: client sent invalid JSON %q: %v", s.in.Text(), err)
	}
	return m
}

// send writes one raw line to the client.
func (s *fakeServer) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.out, line+"\n"); err != nil {
		s.t.Fatalf("fake server: write: %v", err)
	}
}

func transcriptLines(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "transcripts", name))
	if err != nil {
		t.Fatalf("reading transcript %s: %v", name, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestClient_Initialize_RecordedHandshake(t *testing.T) {
	s := newFakeServer(t)
	lines := transcriptLines(t, "initialize.jsonl") // [response, trailing notification]

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := s.recv()
		if req["method"] != "initialize" || req["jsonrpc"] != "2.0" {
			t.Errorf("initialize frame = %v", req)
		}
		params := req["params"].(map[string]any)
		ci := params["clientInfo"].(map[string]any)
		if ci["name"] != "auspex" || ci["version"] == "" {
			t.Errorf("clientInfo = %v, want name auspex + a version", ci)
		}
		// Respond with the RECORDED bytes, id rewritten to match: the
		// real server's response shape (no jsonrpc field) must decode.
		s.send(strings.Replace(lines[0], `"id":1`, `"id":1`, 1))
		s.send(lines[1]) // unsolicited notification right after — tolerated

		// The initialized notification follows (§21.2).
		note := s.recv()
		if note["method"] != "initialized" || note["id"] != nil {
			t.Errorf("second frame = %v, want initialized notification without id", note)
		}
	}()

	res, err := s.client.Initialize(context.Background(), ClientInfo{Name: "auspex", Title: "Auspex", Version: "test"})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !strings.Contains(res.UserAgent, "auspex/") || res.CodexHome == "" {
		t.Errorf("InitializeResult = %+v, want recorded userAgent/codexHome", res)
	}
	<-done

	// The trailing remoteControl notification is delivered, not fatal.
	select {
	case n := <-s.client.Notifications():
		if n.Method != MethodRemoteControlStatus {
			t.Errorf("notification = %q, want %q", n.Method, MethodRemoteControlStatus)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("trailing notification never delivered")
	}
}

func TestClient_CallCorrelation_OutOfOrderResponses(t *testing.T) {
	s := newFakeServer(t)

	go func() {
		first := s.recv()  // id 1
		second := s.recv() // id 2
		// Answer in REVERSE order: correlation must route by id.
		s.send(`{"id":` + jsonID(second) + `,"result":{"tag":"second"}}`)
		s.send(`{"id":` + jsonID(first) + `,"result":{"tag":"first"}}`)
	}()

	type tagged struct {
		Tag string `json:"tag"`
	}
	ctx := context.Background()
	res1 := make(chan tagged, 1)
	go func() {
		var out tagged
		if err := s.client.Call(ctx, "a/one", nil, &out); err != nil {
			t.Errorf("call one: %v", err)
		}
		res1 <- out
	}()
	// Give call one a head start so ids are deterministic (1 then 2).
	time.Sleep(50 * time.Millisecond)
	var out2 tagged
	if err := s.client.Call(ctx, "a/two", nil, &out2); err != nil {
		t.Fatalf("call two: %v", err)
	}
	out1 := <-res1
	if out1.Tag != "first" || out2.Tag != "second" {
		t.Errorf("correlation mixed results: one=%q two=%q", out1.Tag, out2.Tag)
	}
}

func jsonID(frame map[string]any) string {
	b, _ := json.Marshal(frame["id"])
	return string(b)
}

func TestClient_RPCErrorSurfaces(t *testing.T) {
	s := newFakeServer(t)
	go func() {
		req := s.recv()
		s.send(`{"id":` + jsonID(req) + `,"error":{"code":-32601,"message":"method not found"}}`)
	}()
	err := s.client.Call(context.Background(), "no/such", nil, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("err = %v, want *RPCError code -32601", err)
	}
}

func TestClient_MalformedAndUnknownLinesTolerated(t *testing.T) {
	s := newFakeServer(t)
	s.send(`this is not json`)
	s.send(`{"jsonrpc":"2.0"}`)                       // routable to nothing
	s.send(`{"id":99,"result":{}}`)                   // response to nothing
	s.send(`{"method":"future/unknown","params":{}}`) // unknown method: still a notification

	select {
	case n := <-s.client.Notifications():
		if n.Method != "future/unknown" {
			t.Errorf("notification = %q", n.Method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unknown-method notification should still be delivered (consumer decides)")
	}
	st := s.client.Stats()
	if st.MalformedLines != 1 || st.UnknownFrames != 2 {
		t.Errorf("stats = %+v, want 1 malformed + 2 unknown", st)
	}
}

func TestClient_ServerRequest_RespondTo(t *testing.T) {
	s := newFakeServer(t)
	s.send(`{"id":7,"method":"execCommandApproval","params":{"callId":"c1"}}`)

	var req ServerRequest
	select {
	case req = <-s.client.ServerRequests():
	case <-time.After(5 * time.Second):
		t.Fatal("server request never surfaced")
	}
	if req.ID != 7 || req.Method != "execCommandApproval" {
		t.Fatalf("req = %+v", req)
	}
	// io.Pipe writes block until read: respond concurrently with the
	// server-side recv, as a real transport would interleave them.
	respErr := make(chan error, 1)
	go func() {
		respErr <- s.client.RespondTo(req.ID, map[string]string{"decision": "denied"}, nil)
	}()
	resp := s.recv()
	if err := <-respErr; err != nil {
		t.Fatalf("RespondTo: %v", err)
	}
	if jsonID(resp) != "7" || resp["result"].(map[string]any)["decision"] != "denied" {
		t.Errorf("response frame = %v", resp)
	}
}

func TestClient_ConnectionEnd_FailsPendingAndClosesChannels(t *testing.T) {
	s := newFakeServer(t)

	go s.recv() // real servers read the request even when they never answer

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.client.Call(context.Background(), "hangs/forever", nil, nil)
	}()
	time.Sleep(50 * time.Millisecond) // let the call register as pending
	_ = s.out.Close()                 // server dies mid-call

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("pending call err = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call hung after connection end")
	}
	if _, ok := <-s.client.Notifications(); ok {
		t.Error("Notifications channel should be closed")
	}
	if err := s.client.ReadErr(); err != nil {
		t.Errorf("ReadErr = %v, want nil for clean EOF", err)
	}
	if err := s.client.Call(context.Background(), "after/close", nil, nil); err == nil {
		t.Error("Call after connection end should fail")
	}
}

func TestClient_CallContextCancel(t *testing.T) {
	s := newFakeServer(t)
	go s.recv() // swallow the request, never answer

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err := s.client.Call(ctx, "never/answered", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// --- typed notification decoding (schema-shaped fixtures) ---------------

func TestDecode_ThreadTokenUsageNotification(t *testing.T) {
	raw := `{"threadId":"th_1","turnId":"tu_1","tokenUsage":{"last":{"inputTokens":1200,"cachedInputTokens":1000,"outputTokens":80,"reasoningOutputTokens":30,"totalTokens":1280},"total":{"inputTokens":5000,"cachedInputTokens":4000,"outputTokens":400,"reasoningOutputTokens":100,"totalTokens":5400},"modelContextWindow":272000}}`
	var n ThreadTokenUsageNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n.ThreadID != "th_1" || n.TurnID != "tu_1" ||
		n.TokenUsage.Last.CachedInputTokens != 1000 ||
		n.TokenUsage.Total.TotalTokens != 5400 ||
		*n.TokenUsage.ModelContextWindow != 272000 {
		t.Errorf("decoded = %+v", n)
	}
}

func TestDecode_RateLimitsNotification_PartialWindows(t *testing.T) {
	// Only the required usedPercent on primary; secondary absent —
	// unknown is not zero (§8.8: missing windows on some plans).
	raw := `{"rateLimits":{"limitId":"codex_5h","primary":{"usedPercent":37},"planType":"plus"}}`
	var n RateLimitsNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rl := n.RateLimits
	if *rl.LimitID != "codex_5h" || rl.Primary.UsedPercent != 37 || rl.Primary.ResetsAt != nil || rl.Secondary != nil {
		t.Errorf("decoded = %+v", rl)
	}
}

func TestDecode_TurnDiff_MeasuresAndDropsContent(t *testing.T) {
	raw := `{"threadId":"th","turnId":"tu","diff":"--- a/x\n+++ b/x\n+secret content\n"}`
	var n TurnDiffNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n.DiffByteLen != 32 || n.ThreadID != "th" {
		t.Errorf("decoded = %+v (want byte length 32, no content field to leak)", n)
	}
}

func TestDecode_TurnPlan_CountsSteps(t *testing.T) {
	raw := `{"threadId":"th","turnId":"tu","explanation":"secret","plan":[{"step":"a"},{"step":"b"},{"step":"c"}]}`
	var n TurnPlanNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n.PlanSteps != 3 {
		t.Errorf("PlanSteps = %d, want 3", n.PlanSteps)
	}
}

func TestDecode_TurnWithErrorAndItems(t *testing.T) {
	raw := `{"id":"tu_9","status":"failed","items":[{"type":"commandExecution","id":"i1"},{"type":"agentMessage","id":"i2"}],"error":{"message":"boom boom"}}`
	var turn Turn
	if err := json.Unmarshal([]byte(raw), &turn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if turn.Status != TurnStatusFailed || turn.ItemCount() != 2 || turn.Error.MessageLen != 9 {
		t.Errorf("decoded = %+v itemCount=%d msgLen=%d", turn, turn.ItemCount(), turn.Error.MessageLen)
	}
}

// --- schema pin (drift tripwire) -----------------------------------------

// TestVendoredSchema_PinsConsumedDefinitions fails when a regenerated
// schema drops or reshapes a definition this package's stable subset
// depends on — the §21.7 "required field missing => capability degraded"
// contract needs the drift to be VISIBLE at test time, not discovered in
// production decode failures.
func TestVendoredSchema_PinsConsumedDefinitions(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "codex-schema", "codex_app_server_protocol.v2.schemas.json"))
	if err != nil {
		t.Fatalf("vendored schema missing: %v", err)
	}
	var doc struct {
		Definitions map[string]struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("vendored schema unparseable: %v", err)
	}

	requireDef := func(name string, wantRequired ...string) {
		t.Helper()
		def, ok := doc.Definitions[name]
		if !ok {
			t.Errorf("schema definition %s vanished — stable-subset drift", name)
			return
		}
		have := make(map[string]bool, len(def.Required))
		for _, r := range def.Required {
			have[r] = true
		}
		for _, want := range wantRequired {
			if !have[want] {
				t.Errorf("%s no longer requires %q (required=%v)", name, want, def.Required)
			}
		}
	}

	requireDef("TurnInterruptParams", "threadId", "turnId")
	requireDef("TurnStartParams", "input", "threadId")
	requireDef("ThreadResumeParams", "threadId")
	requireDef("TokenUsageBreakdown", "inputTokens", "cachedInputTokens", "outputTokens", "reasoningOutputTokens", "totalTokens")
	requireDef("ThreadTokenUsageUpdatedNotification", "threadId", "turnId", "tokenUsage")
	requireDef("RateLimitWindow", "usedPercent")
	requireDef("TurnCompletedNotification", "threadId", "turn")
	requireDef("Turn", "id", "status")
	requireDef("InitializeParams", "clientInfo")
}
