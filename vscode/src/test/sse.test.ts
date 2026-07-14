/**
 * sse.test.ts — the SSE parser against the exact stream shapes
 * internal/httpapi/httpapi.go handleEvents emits (event/data blocks,
 * ": ping" heartbeats), plus grammar edge cases (chunk splits, CRLF,
 * multi-line data).
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { Backoff, SSEMessage, SSEParser } from '../sse';

function collect(): { messages: SSEMessage[]; parser: SSEParser } {
  const messages: SSEMessage[] = [];
  const parser = new SSEParser((m) => messages.push(m));
  return { messages, parser };
}

test('parses the daemon handler shape: event + one-line JSON data', () => {
  const { messages, parser } = collect();
  parser.feed('event: pause.wake.triggered\ndata: {"SchemaVersion":"auspex.event.v1"}\n\n');
  assert.equal(messages.length, 1);
  assert.equal(messages[0].event, 'pause.wake.triggered');
  assert.deepEqual(JSON.parse(messages[0].data), { SchemaVersion: 'auspex.event.v1' });
});

test('ignores heartbeat comments (": ping")', () => {
  const { messages, parser } = collect();
  parser.feed(': ping\n\n: ping\n\nevent: x\ndata: {}\n\n');
  assert.equal(messages.length, 1);
  assert.equal(messages[0].event, 'x');
});

test('handles chunks split at arbitrary byte boundaries', () => {
  const { messages, parser } = collect();
  const full = 'event: provider.turn.completed\ndata: {"a":1}\n\n';
  for (const ch of full) {
    parser.feed(ch); // worst case: one character per chunk
  }
  assert.equal(messages.length, 1);
  assert.equal(messages[0].event, 'provider.turn.completed');
  assert.equal(messages[0].data, '{"a":1}');
});

test('tolerates CRLF line endings', () => {
  const { messages, parser } = collect();
  parser.feed('event: x\r\ndata: {"a":1}\r\n\r\n');
  assert.equal(messages.length, 1);
  assert.equal(messages[0].data, '{"a":1}');
});

test('joins multi-line data with newlines per the SSE spec', () => {
  const { messages, parser } = collect();
  parser.feed('data: line1\ndata: line2\n\n');
  assert.equal(messages.length, 1);
  assert.equal(messages[0].event, '');
  assert.equal(messages[0].data, 'line1\nline2');
});

test('blank lines without accumulated fields dispatch nothing', () => {
  const { messages, parser } = collect();
  parser.feed('\n\n\n');
  assert.equal(messages.length, 0);
});

test('event type resets between blocks', () => {
  const { messages, parser } = collect();
  parser.feed('event: a\ndata: 1\n\ndata: 2\n\n');
  assert.equal(messages.length, 2);
  assert.equal(messages[0].event, 'a');
  assert.equal(messages[1].event, ''); // no carry-over from the previous block
});

test('backoff doubles to the cap and resets', () => {
  const b = new Backoff(1000, 30_000);
  assert.equal(b.nextDelayMs(), 1000);
  assert.equal(b.nextDelayMs(), 2000);
  assert.equal(b.nextDelayMs(), 4000);
  for (let i = 0; i < 10; i++) {
    b.nextDelayMs();
  }
  assert.equal(b.nextDelayMs(), 30_000); // capped
  b.reset();
  assert.equal(b.nextDelayMs(), 1000);
});
