import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

test('Parall reports connected only after the WebSocket upgrade opens', async (t) => {
  const tempDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'loom-parall-liveness-'));
  t.after(() => fs.promises.rm(tempDir, { recursive: true, force: true }));
  const stateFile = path.join(tempDir, 'state.json');
  const apiCredential = crypto.randomBytes(48).toString('base64url');
  const heartbeats = [];
  let resolveFirstHeartbeat;
  let resolveConnectedHeartbeat;
  let resolveUpgrade;
  const firstHeartbeat = new Promise((resolve) => { resolveFirstHeartbeat = resolve; });
  const connectedHeartbeat = new Promise((resolve) => { resolveConnectedHeartbeat = resolve; });
  const upgradeReceived = new Promise((resolve) => { resolveUpgrade = resolve; });
  let pendingUpgrade;

  const websocketServer = http.createServer();
  websocketServer.on('upgrade', (request, socket) => {
    pendingUpgrade = { request, socket };
    resolveUpgrade();
  });
  await listen(websocketServer);
  t.after(() => {
    pendingUpgrade?.socket.destroy();
    websocketServer.closeAllConnections?.();
    websocketServer.close();
  });

  const server = http.createServer(async (request, response) => {
    const target = new URL(request.url, 'http://loom.test');
    const body = await readJSON(request);
    if (target.pathname === '/api/v1/orgs/org_test/dispatch') {
      return json(response, { data: [], has_more: false });
    }
    if (target.pathname === '/api/v1/ws/ticket') {
      return json(response, {
        ws_url: `ws://127.0.0.1:${websocketServer.address().port}/ws`,
        ticket: 'ticket_test',
      });
    }
    if (target.pathname === '/api/v1/orgs/org_test/members/agent_test/chats') {
      return json(response, { data: [], has_more: false });
    }
    if (target.pathname === '/hub/api/integrations/connections/conn_test/commands') {
      response.writeHead(200, { 'Content-Type': 'text/event-stream', Connection: 'keep-alive' });
      response.write(': ready\n\n');
      return;
    }
    if (target.pathname === '/hub/api/integrations/connections/conn_test/heartbeat') {
      heartbeats.push(body);
      if (heartbeats.length === 1) resolveFirstHeartbeat(body);
      if (body.status === 'connected') resolveConnectedHeartbeat(body);
      return json(response, { connection: { id: 'conn_test', status: body.status } });
    }
    if (target.pathname === '/hub/api/integrations/addresses/addr_test/conversation-candidates') {
      return json(response, { conversations: [] });
    }
    return json(response, { error: 'not found' }, 404);
  });
  await listen(server);
  t.after(() => {
    server.closeAllConnections?.();
    server.close();
  });

  const child = spawn(process.execPath, [
    path.resolve('gateway/parall.mjs'),
    '--hub', `http://127.0.0.1:${server.address().port}/hub`,
    '--connection', 'conn_test',
    '--address', 'addr_test',
    '--state-file', stateFile,
    '--gateway-generation', 'ggen_test',
    '--gateway-build', 'build_test',
    '--gateway-executable-sha256', 'a'.repeat(64),
  ], {
    cwd: path.resolve('.'),
    env: {
      ...process.env,
      PRLL_API_URL: `http://127.0.0.1:${server.address().port}`,
      PRLL_API_KEY: apiCredential,
      PRLL_ORG_ID: 'org_test',
      PRLL_AGENT_ID: 'agent_test',
    },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  t.after(() => { if (child.exitCode === null) child.kill('SIGTERM'); });

  const beforeOpen = await withTimeout(firstHeartbeat, 5000, 'initial heartbeat');
  assert.notEqual(beforeOpen.status, 'connected', 'gateway claimed connected before WebSocket open');
  await withTimeout(upgradeReceived, 5000, 'WebSocket upgrade');
  acceptWebSocket(pendingUpgrade.request, pendingUpgrade.socket);
  const afterOpen = await withTimeout(connectedHeartbeat, 5000, 'connected heartbeat');
  assert.equal(afterOpen.gatewayGeneration, 'ggen_test');
  assert.equal(afterOpen.gatewayBuild, 'build_test');
  assert.equal(afterOpen.gatewayExecutableSha256, 'a'.repeat(64));

  const childClosed = new Promise((resolve) => child.once('close', resolve));
  child.kill('SIGTERM');
  pendingUpgrade.socket.destroy();
  await withTimeout(childClosed, 5000, 'gateway shutdown');
  assert.equal(stderr.includes(apiCredential), false, 'gateway stderr exposed its credential');
});

function listen(server) {
  return new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
}

function acceptWebSocket(request, socket) {
  const accept = crypto
    .createHash('sha1')
    .update(`${request.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
    .digest('base64');
  socket.write([
    'HTTP/1.1 101 Switching Protocols',
    'Upgrade: websocket',
    'Connection: Upgrade',
    `Sec-WebSocket-Accept: ${accept}`,
    '',
    '',
  ].join('\r\n'));
}

function readJSON(request) {
  return new Promise((resolve) => {
    let raw = '';
    request.setEncoding('utf8');
    request.on('data', (chunk) => { raw += chunk; });
    request.on('end', () => {
      try { resolve(raw ? JSON.parse(raw) : {}); } catch { resolve({}); }
    });
  });
}

function json(response, value, status = 200) {
  response.writeHead(status, { 'Content-Type': 'application/json' });
  response.end(JSON.stringify(value));
}

async function withTimeout(promise, milliseconds, label) {
  let timeout;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error(`${label} timed out`)), milliseconds);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
}
