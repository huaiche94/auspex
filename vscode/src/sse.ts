/**
 * sse.ts — a minimal Server-Sent Events stream parser for the daemon's
 * GET /v1/events/stream (internal/httpapi/httpapi.go handleEvents).
 *
 * The server emits exactly two line shapes (see handleEvents):
 *
 *   event: <EventType>\n
 *   data: <one-line JSON>\n
 *   \n
 *
 * and heartbeat comments while quiet:
 *
 *   : ping\n
 *   \n
 *
 * No `id:` lines are ever sent and the broker keeps no history
 * (internal/daemon/broker.go: "live view for attached clients ... not an
 * event store"), so Last-Event-ID resume is NOT supported server-side —
 * reconnection is a plain reconnect and current state is re-read from the
 * status/jobs endpoints (exactly what broker.go tells subscribers to do).
 *
 * The parser still implements the general SSE field rules (comment lines,
 * multi-line data accumulation, CRLF tolerance) so it does not break if
 * the server's formatting evolves within the SSE grammar.
 */

/** One dispatched SSE message. */
export interface SSEMessage {
  /** The `event:` field value; "" when the server sent none. */
  event: string;
  /** The concatenated `data:` field value (newline-joined per SSE spec). */
  data: string;
}

/**
 * Incremental SSE parser: feed() it raw chunks as they arrive; it invokes
 * onMessage once per blank-line-terminated event block. Comment lines
 * (leading ":") are ignored, which silently absorbs the server's
 * ": ping" heartbeats.
 */
export class SSEParser {
  private buffer = '';
  private eventType = '';
  private dataLines: string[] = [];

  constructor(private readonly onMessage: (msg: SSEMessage) => void) {}

  feed(chunk: string): void {
    this.buffer += chunk;
    // Process complete lines only; keep any trailing partial line buffered.
    for (;;) {
      const nl = this.buffer.indexOf('\n');
      if (nl === -1) {
        return;
      }
      let line = this.buffer.slice(0, nl);
      this.buffer = this.buffer.slice(nl + 1);
      if (line.endsWith('\r')) {
        line = line.slice(0, -1);
      }
      this.processLine(line);
    }
  }

  private processLine(line: string): void {
    if (line === '') {
      // Blank line: dispatch the accumulated block, if it carried data.
      if (this.dataLines.length > 0 || this.eventType !== '') {
        this.onMessage({ event: this.eventType, data: this.dataLines.join('\n') });
      }
      this.eventType = '';
      this.dataLines = [];
      return;
    }
    if (line.startsWith(':')) {
      return; // comment / heartbeat
    }
    const colon = line.indexOf(':');
    let field: string;
    let value: string;
    if (colon === -1) {
      field = line;
      value = '';
    } else {
      field = line.slice(0, colon);
      value = line.slice(colon + 1);
      if (value.startsWith(' ')) {
        value = value.slice(1);
      }
    }
    switch (field) {
      case 'event':
        this.eventType = value;
        break;
      case 'data':
        this.dataLines.push(value);
        break;
      default:
        // "id" / "retry" / unknown fields: the daemon never sends them
        // (broker has no replay); ignore rather than guess semantics.
        break;
    }
  }
}

/**
 * Exponential backoff schedule for stream reconnects: 1s, 2s, 4s, ...
 * capped at 30s. Reset to the start after a connection that actually
 * delivered bytes (heartbeats count — the server pings every 15s, so a
 * healthy-but-quiet stream still proves liveness).
 */
export class Backoff {
  private attempt = 0;

  constructor(
    private readonly baseMs = 1000,
    private readonly maxMs = 30_000
  ) {}

  /** Delay to wait before the next reconnect attempt, advancing the schedule. */
  nextDelayMs(): number {
    const delay = Math.min(this.baseMs * 2 ** this.attempt, this.maxMs);
    this.attempt += 1;
    return delay;
  }

  reset(): void {
    this.attempt = 0;
  }
}
