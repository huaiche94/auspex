/**
 * client.test.ts — getSessionStatus against a real loopback HTTP server
 * (client.ts is deliberately vscode-free and uses only Node built-ins, so
 * it runs verbatim under node --test). Covers the request URL for both
 * route forms, the Authorization header, 404 → undefined (the "no session
 * yet" normal state, per internal/sessionstatus/reader.go), the §23.5
 * error envelope on auth failure, and fail-soft parsing of a foreign
 * schema_version.
 */
import assert from 'node:assert/strict';
import * as http from 'node:http';
import { AddressInfo } from 'node:net';
import { test } from 'node:test';

import { DaemonApiError, DaemonConnection, getSessionStatus } from '../client';

interface SeenRequest {
  method: string | undefined;
  url: string | undefined;
  authorization: string | undefined;
}

interface FakeDaemon {
  conn: DaemonConnection;
  requests: SeenRequest[];
  close: () => Promise<void>;
}

/** Start a loopback server answering every request with one canned response. */
async function startFakeDaemon(status: number, body: unknown): Promise<FakeDaemon> {
  const requests: SeenRequest[] = [];
  const server = http.createServer((req, res) => {
    requests.push({ method: req.method, url: req.url, authorization: req.headers.authorization });
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(body));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  const conn: DaemonConnection = {
    metadata: {
      schema_version: 'auspex.daemon.v1',
      pid: 4242,
      address: `127.0.0.1:${port}`,
      token_file: '/unused/daemon.token',
      started_at: '2026-07-16T10:00:00Z',
      version: '0.1.0',
    },
    token: 'test-token',
  };
  return {
    conn,
    requests,
    close: () => new Promise<void>((resolve, reject) => server.close((err) => (err ? reject(err) : resolve()))),
  };
}

/** Minimal but complete auspex.daemon.session_status.v1 envelope. */
const OK_BODY = {
  schema_version: 'auspex.daemon.session_status.v1',
  session_id: 'sess-1',
  risk: null,
  runway: null,
  quota: { as_of: '2026-07-16T10:00:00Z', windows: [] },
  progress: { task_id: null, nodes: [], edges: [] },
  checkpoint: null,
  pause: null,
};

test('getSessionStatus hits GET /v1/session/status with the bearer token by default', async () => {
  const daemon = await startFakeDaemon(200, OK_BODY);
  try {
    const snap = await getSessionStatus(daemon.conn);
    assert.ok(snap);
    assert.equal(snap.session_id, 'sess-1');
    assert.equal(daemon.requests.length, 1);
    assert.equal(daemon.requests[0].method, 'GET');
    assert.equal(daemon.requests[0].url, '/v1/session/status');
    assert.equal(daemon.requests[0].authorization, 'Bearer test-token');
  } finally {
    await daemon.close();
  }
});

test('getSessionStatus hits GET /v1/session/{id}/status with the id URL-encoded', async () => {
  const daemon = await startFakeDaemon(200, { ...OK_BODY, session_id: 'sess/2' });
  try {
    const snap = await getSessionStatus(daemon.conn, 'sess/2');
    assert.ok(snap);
    assert.equal(daemon.requests[0].url, '/v1/session/sess%2F2/status');
  } finally {
    await daemon.close();
  }
});

test('getSessionStatus maps 404 (no sessions exist yet) to undefined, not an error', async () => {
  const daemon = await startFakeDaemon(404, {
    error: {
      code: 'AUSPEX_NOT_FOUND',
      message: 'sessionstatus: no sessions exist yet',
      retryable: false,
    },
  });
  try {
    assert.equal(await getSessionStatus(daemon.conn), undefined);
  } finally {
    await daemon.close();
  }
});

test('getSessionStatus surfaces the §23.5 envelope on auth failure', async () => {
  const daemon = await startFakeDaemon(401, {
    error: { code: 'AUSPEX_UNAUTHORIZED', message: 'missing or invalid bearer token', retryable: false },
  });
  try {
    await assert.rejects(
      () => getSessionStatus(daemon.conn),
      (err: unknown) => {
        assert.ok(err instanceof DaemonApiError);
        assert.equal(err.status, 401);
        assert.equal(err.code, 'AUSPEX_UNAUTHORIZED');
        return true;
      }
    );
  } finally {
    await daemon.close();
  }
});

test('getSessionStatus degrades a foreign schema_version to undefined (fail-soft parse)', async () => {
  const daemon = await startFakeDaemon(200, { ...OK_BODY, schema_version: 'auspex.daemon.session_status.v9' });
  try {
    assert.equal(await getSessionStatus(daemon.conn), undefined);
  } finally {
    await daemon.close();
  }
});
