// Command fakeappserver is the test stand-in for the real `codex` CLI in
// the App Server managed-path tests (issue #9 M7 Phase 2 — the "fake App
// Server E2E" the M7 acceptance names). Like testdata/fakeprovider it is
// a compiled Go helper driven by environment variables, so the identical
// test runs on windows-latest CI.
//
// Behavior by argv:
//
//	argv[1] == "app-server": speak newline-delimited JSON-RPC on stdio —
//	    initialize -> canned result; thread/start -> thread th_fake with
//	    a declared model; turn/start -> turn tu_fake inProgress, then
//	    every line of AUSPEX_FAKE_AS_EVENTS (a JSONL file of notification
//	    frames) verbatim; turn/interrupt -> empty result then an
//	    interrupted turn/completed. Runs until stdin EOF.
//	    AUSPEX_FAKE_AS_FAIL=1 exits 1 immediately instead (a binary
//	    without app-server support — the fallback path's trigger).
//	anything else: the exec fallback stand-in, reusing fakeprovider's
//	    env contract (AUSPEX_FAKE_STREAM_FILE copied to stdout,
//	    AUSPEX_FAKE_SLEEP_MS, AUSPEX_FAKE_EXIT_CODE) so one binary can
//	    serve a run that degrades from app-server to exec.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "app-server" {
		runAppServer()
		return
	}
	runExecFallback()
}

func runAppServer() {
	if os.Getenv("AUSPEX_FAKE_AS_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "fakeappserver: app-server mode disabled by AUSPEX_FAKE_AS_FAIL")
		os.Exit(1)
	}
	out := bufio.NewWriter(os.Stdout)
	send := func(line string) {
		_, _ = out.WriteString(line + "\n")
		_ = out.Flush()
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var frame struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &frame); err != nil || frame.ID == nil {
			continue // notifications from the client (initialized) and junk: ignored
		}
		id := *frame.ID
		switch frame.Method {
		case "initialize":
			send(fmt.Sprintf(`{"id":%d,"result":{"userAgent":"fake/0.0.0 (test)","codexHome":"/tmp/fake-codex","platformFamily":"unix","platformOs":"testos"}}`, id))
		case "thread/start":
			send(fmt.Sprintf(`{"id":%d,"result":{"thread":{"id":"th_fake"},"model":"gpt-5.3-codex","modelProvider":"openai","cwd":"/tmp","approvalPolicy":"never","approvalsReviewer":"none","sandbox":"workspace-write"}}`, id))
		case "turn/start":
			send(fmt.Sprintf(`{"id":%d,"result":{"turn":{"id":"tu_fake","status":"inProgress","items":[]}}}`, id))
			streamEventsFile(send)
		case "turn/interrupt":
			send(fmt.Sprintf(`{"id":%d,"result":{}}`, id))
			send(`{"method":"turn/completed","params":{"threadId":"th_fake","turn":{"id":"tu_fake","status":"interrupted","items":[]}}}`)
		default:
			send(fmt.Sprintf(`{"id":%d,"error":{"code":-32601,"message":"fakeappserver: unknown method"}}`, id))
		}
	}
}

// streamEventsFile replays the scripted notification frames, verbatim.
func streamEventsFile(send func(string)) {
	path := os.Getenv("AUSPEX_FAKE_AS_EVENTS")
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeappserver: reading events file:", err)
		os.Exit(3)
	}
	start := 0
	for i := 0; i <= len(body); i++ {
		if i == len(body) || body[i] == '\n' {
			if i > start {
				send(string(body[start:i]))
			}
			start = i + 1
		}
	}
}

// runExecFallback mirrors testdata/fakeprovider's env contract.
func runExecFallback() {
	if path := os.Getenv("AUSPEX_FAKE_STREAM_FILE"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeappserver: reading stream file:", err)
			os.Exit(3)
		}
		if _, err := os.Stdout.Write(body); err != nil {
			os.Exit(3)
		}
	}
	if v := os.Getenv("AUSPEX_FAKE_SLEEP_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			time.Sleep(time.Duration(n) * time.Millisecond)
		}
	}
	code := 0
	if v := os.Getenv("AUSPEX_FAKE_EXIT_CODE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			code = n
		}
	}
	os.Exit(code)
}
