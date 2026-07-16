import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

test('gateway sends a Loom outbox command through Slack and reports the durable result', async (t) => {
  const tempDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'loom-slack-test-'));
  const stateFile = path.join(tempDir, 'state.json');
  const attachmentFile = path.join(tempDir, 'report.txt');
  await fs.promises.writeFile(attachmentFile, 'attachment payload');
  let slackRequest;
  let uploadRequest;
  let completeRequest;
  let reportedResult;
  let resolveReported;
  const reported = new Promise((resolve) => { resolveReported = resolve; });

  const server = http.createServer(async (request, response) => {
    const body = await readJSON(request);
    if (request.url === '/slack/auth.test') {
      return json(response, { ok: true, user_id: 'U_BOT', team_id: 'T_TEAM' });
    }
    if (request.url === '/slack/chat.postMessage') {
      slackRequest = body;
      return json(response, { ok: true, ts: '1710000009.900' });
    }
    if (request.url === '/slack/files.getUploadURLExternal') {
      uploadRequest = body;
      return json(response, {
        ok: true,
        upload_url: `http://127.0.0.1:${server.address().port}/upload/F_REPORT`,
        file_id: 'F_REPORT',
      });
    }
    if (request.url === '/upload/F_REPORT') {
      response.writeHead(200);
      response.end('ok');
      return;
    }
    if (request.url === '/slack/files.completeUploadExternal') {
      completeRequest = body;
      return json(response, { ok: true, files: [{ id: 'F_REPORT', title: 'report.txt' }] });
    }
    if (request.url === '/hub/api/integrations/connections/conn_test/commands') {
      response.writeHead(200, { 'Content-Type': 'text/event-stream', Connection: 'keep-alive' });
      response.write(`data: ${JSON.stringify({
        type: 'connector/command',
        data: {
          outboxItem: {
            id: 'out_test',
            attemptToken: 'claim_test',
            content: {
              text: 'review complete',
              attachments: [{ id: 'art_report', path: attachmentFile, name: 'report.txt', mimeType: 'text/plain' }],
            },
            conversation: {
              conversationId: 'C_DEV',
              messageId: '1710000000.100',
              conversationType: 'channel',
            },
          },
        },
      })}\n\n`);
      return;
    }
    if (request.url === '/hub/api/integrations/connections/conn_test/outbox/out_test/result') {
      reportedResult = body;
      json(response, { outboxItem: { id: 'out_test', state: 'sent' } });
      resolveReported();
      return;
    }
    if (request.url === '/hub/api/integrations/connections/conn_test/heartbeat') {
      return json(response, { connection: { id: 'conn_test', status: 'connected' } });
    }
    json(response, { error: 'not found' }, 404);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const port = server.address().port;

  const child = spawn(process.execPath, [
    path.resolve('gateway/slack.mjs'),
    '--socket', 'false',
    '--hub', `http://127.0.0.1:${port}/hub`,
    '--connection', 'conn_test',
    '--address', 'addr_test',
    '--state-file', stateFile,
  ], {
    cwd: path.resolve('.'),
    env: {
      ...process.env,
      SLACK_API_URL: `http://127.0.0.1:${port}/slack`,
      SLACK_BOT_TOKEN: 'xoxb-test',
    },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  t.after(() => { if (child.exitCode === null) child.kill('SIGTERM'); });

  let timeout;
  try {
    await Promise.race([
      reported,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error(`gateway timed out:\n${stderr}`)), 5000);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('close', resolve));

  assert.deepEqual(slackRequest, {
    channel: 'C_DEV',
    text: 'review complete',
    unfurl_links: false,
    unfurl_media: false,
    thread_ts: '1710000000.100',
  });
  assert.deepEqual(uploadRequest, { filename: 'report.txt', length: 18 });
  assert.deepEqual(completeRequest, {
    files: [{ id: 'F_REPORT', title: 'report.txt' }],
    channel_id: 'C_DEV',
    thread_ts: '1710000000.100',
  });
  assert.deepEqual(reportedResult, {
    attemptToken: 'claim_test',
    success: true,
    externalMessageId: '1710000009.900',
    externalMessageIds: ['1710000009.900', 'F_REPORT'],
    deliveryReceipts: [
      { kind: 'text', externalMessageId: '1710000009.900' },
      { kind: 'attachment', artifactId: 'art_report', externalAttachmentId: 'F_REPORT' },
    ],
  });
  const state = JSON.parse(await fs.promises.readFile(stateFile, 'utf8'));
  assert.deepEqual(state.outboxResults.out_test, ['1710000009.900', 'F_REPORT']);
});

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
