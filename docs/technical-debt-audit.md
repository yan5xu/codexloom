# CodexLoom Technical Debt Audit

Audit date: 2026-07-15

This document is the maintained engineering audit for CodexLoom. It covers the
Go service and CLI, Codex app-server integration, durable state, Connector
gateways, React WebUI, release process, and the behavior of the current local
production instance.

The audit is intentionally stricter than a code-quality review. CodexLoom is a
governance control plane for long-lived Agents. A defect that would be minor in
a disposable local tool can expose local-user execution, lose organizational
state, or silently duplicate external communication here.

## Executive Verdict

CodexLoom has a sound product model and several good technical foundations:

- Codex rollout files remain the source of truth for Thread history.
- A shared CodexHost avoids one app-server process per Agent.
- Agent, Profile, Relationship, Membership, Inbox, and Outbox objects have
  stable identities.
- External credentials are moving behind managed Connectors and the Outbox has
  durable intent plus attachment receipt validation.
- The Go race suite, Go vet, frontend build, gateway tests, and dependency audit
  currently pass.

The original audit found one P0 trust-boundary defect and a linked set of P1
durability and scaling defects. Its most dangerous baseline chain was:

1. Event logs and full backups grow without a byte budget.
2. The production data volume is already effectively full.
3. Many state transitions ignore persistence failures and still publish success.
4. A restart can therefore restore an older state while users and Agents were
   told that the newer transition succeeded.

The 2026-07-15 remediation pass intentionally prioritized functional completeness,
durability, bounded storage, long-Thread performance, delivery fencing, restart
lifecycle, and realtime reconciliation. The user explicitly deferred security
work; F-01, F-02, and F-11 therefore remain open and must not be mistaken for
accepted production security posture.

## Remediation Status

The detailed Evidence sections below preserve the pre-remediation baseline. This
table is the current status and should be updated when later work changes it.

| Finding | Status | Current invariant / remaining work |
| --- | --- | --- |
| F-01 | Deferred | Authentication, network binding, CORS, and principal separation remain open by explicit scope decision. |
| F-02 | Deferred | Raw local-image path serving remains a security item; migrate to governed Artifact IDs later. |
| F-03 | Remediated for growth | Backups are tar.gz with count/byte/age retention; derived event logs are excluded, rotated, compressed, and pruned. Free-space admission control remains open. |
| F-04 | Remediated for core aggregates | Agent config, Communication, Inbox/Attempt/Outbox, Integration, Schedule, Human Request, and ProviderOperation use commit-before-projection or rollback. Codex runtime status is explicitly a reconstructable best-effort projection. |
| F-05 | Remediated | Required registries and NDJSON fail startup closed; startup recovery transitions are persisted. Schema checksums/quarantine tooling remain future hardening. |
| F-06 | Remediated | Rollout turn offsets update incrementally and history parses only the requested byte window; event tail/last-seq reads are bounded. A checked-in 50,000-Turn benchmark covers cold index construction and warm latest-window reads. |
| F-07 | Remediated | Every due run is a durable Agent Message occurrence keyed by `(scheduleId, scheduledAt)` before the Schedule advances. |
| F-08 | Remediated in Loom | Outbox and ProviderOperation claims have a two-minute lease and random attempt token; stale results are fenced. Provider APIs without native idempotency can still duplicate after an unknowable upstream success. |
| F-09 | Remediated for owned work | Restart waits for active Turns and live Connector claims, stops new claims/Turns, shuts HTTP first, and waits all Hub loops and registered workers before closing CodexHost. Snapshot consistency remains part of F-10. |
| F-10 | Partial | Archives are compressed, required-file short reads fail, derived events are excluded, and retention is bounded. A write barrier, checksums, restore command, and automated restore drill remain open. |
| F-11 | Deferred | On-disk permission hardening remains open by explicit scope decision. |
| F-12 | Partial | Expensive event rotation moved out of append paths and all finite goroutines are lifecycle-owned; the broad Hub mutex and synchronous projection I/O remain structural debt. |
| F-13 | Remediated for current domains | Runtime, Agent lifecycle, internal Communication, Integration/Ingress, Inbox, Outbox, external-message policy, and shutdown have focused Hub files. HTTP routes and CLI commands are registered by domain instead of one cross-domain function. Future domains should preserve these boundaries. |
| F-14 | Remediated | Global SSE has durable IDs/replay; compacted cursors emit `loom/reconcile`, and every open WebUI pane refetches its authoritative projection/history. |
| F-15 | Open | Tagged frontend contracts and automated component/E2E coverage remain missing. |
| F-16 | Open | Required CI/release/restore gates remain missing. |

## Severity Model

| Level | Meaning |
| --- | --- |
| P0 | Direct compromise or catastrophic-loss path available in the default deployment. Contain immediately. |
| P1 | Likely loss, duplication, prolonged outage, or core product contract failure under realistic conditions. |
| P2 | Structural debt that materially raises change cost or makes P1 defects hard to prevent. |
| P3 | Local maintainability, performance, or polish debt with bounded impact. |

## Findings

### F-01: The control plane is unauthenticated and cross-origin writable (P0)

**Evidence**

- `cmd/codex-loom/main.go:51-54` binds `:4870`, which listens on every network
  interface rather than loopback only.
- `internal/httpapi/server.go:59-1400` registers the REST, SSE, Agent, Profile,
  integration, credential setup, and execution endpoints without a common
  authentication middleware.
- `internal/httpapi/server.go:1907-1917` sends
  `Access-Control-Allow-Origin: *` and permits write methods.
- Only selected admin routes use `allowAdminRequest`; when no token is set it
  trusts a loopback remote address (`internal/httpapi/server.go:1769-1788`).
  Connector routes use the same fallback pattern
  (`internal/httpapi/server.go:1432-1446`). A request initiated by a local web
  page still reaches the server from loopback, so source IP is not browser
  authentication or CSRF protection.
- New and remotely adopted Agents default to `danger-full-access` with approval
  policy `never` (`internal/hub/hub.go:2039-2051` and
  `internal/hub/codex_host.go:355-381`).
- A non-destructive check against the running instance confirmed that an
  attacker Origin receives wildcard CORS on `GET /api/agents`, that preflight
  permits `POST`, and that `GET /api/admin/backups` is readable cross-origin.

**Impact**

Any caller that can reach port 4870 can inspect organizational data, mutate
Agents and integrations, or start a Turn. With the default Agent sandbox this is
effectively a remote local-user code-execution path. A malicious local webpage
also has a practical localhost attack path. Tailscale limits exposure only to a
network population; it is not an application authorization boundary.

**Required remediation**

1. Bind to `127.0.0.1` by default. Require an explicit `--listen` or environment
   setting for network exposure.
2. Put all non-health APIs and both SSE streams behind one authenticated
   principal model. Separate operator sessions, Connector credentials, and
   future read-only observer access.
3. Replace wildcard CORS with same-origin behavior. If cross-origin clients are
   required, use an explicit allowlist plus CSRF protection; never infer browser
   authority from `RemoteAddr`.
4. Scope Connector credentials per Connection and rotate them independently.
5. Make sandbox and approval defaults an explicit installation policy, not a
   silent consequence of Agent creation.
6. Add HTTP `ReadHeaderTimeout`, `ReadTimeout` or appropriate streaming-aware
   limits, `IdleTimeout`, and `MaxHeaderBytes`.

**Acceptance criteria**

- Every non-health request without a valid principal returns 401/403.
- An arbitrary Origin cannot read or write APIs, including from a local browser.
- A tailnet peer cannot create an Agent or Turn without explicit authorization.
- Connector A cannot claim or complete Connector B's work.

### F-02: Arbitrary local images can be read through the API (P0 with F-01)

**Evidence**

`internal/httpapi/server.go:1468-1503` accepts any absolute path, opens it, and
serves it when content sniffing reports an image MIME type. It has no managed
artifact check, allowed-root check, ownership check, or authorization of its
own. Wildcard CORS makes the response browser-readable.

**Impact**

Private screenshots, downloaded identity documents, QR codes, or other images
readable by the Loom process can be exfiltrated. This is a distinct capability
leak even after general API authentication because an ordinary observer should
not automatically gain arbitrary filesystem read authority.

**Required remediation**

Remove raw path serving from the public contract. Resolve a managed artifact ID
to a canonical path under Loom-owned storage, authorize it against the Agent or
operator principal, reject symlink escapes, and return a short-lived capability
URL when browser display is needed.

### F-03: Storage growth is unbounded and production is already at the limit (P1)

**Production evidence**

At audit time:

- `/System/Volumes/Data`: 460 GiB total, 421 GiB used, 1.8 GiB available, 100%
  reported utilization.
- `~/.codex-loom`: 16 GiB.
- `~/.codex-loom/backups`: 25 archives totaling 14,752,718,452 bytes.
- `~/.codex-loom/events`: 1.7 GiB.
- The largest Agent event log is approximately 632 MB / 398k records; several
  others are 100-327 MB.

No files were removed during this audit.

**Code evidence**

- Backup retention is a fixed count of 25 rather than a byte or age budget
  (`internal/backup/backup.go:84-86`, `226`, and `269-280`).
- Every backup walks the complete Loom data directory and also adds matching
  Codex rollout files (`internal/backup/backup.go:127-170`), so reconstructable
  event logs are copied repeatedly.
- Per-Agent streaming events are appended forever
  (`internal/store/store.go:418-429`); there is no segment rotation, retention,
  compaction, or index.

**Impact**

Backups intended to improve recovery are consuming most Loom storage and can
cause the write failures that make recovery necessary. Disk exhaustion affects
Loom state, Codex rollouts, gateway state, builds, and unrelated applications on
the same host.

**Required remediation**

1. Add a disk health guard with warning and hard-stop thresholds before writes,
   backups, and restart snapshots.
2. Change backup retention to a combined maximum age and maximum bytes policy.
3. Exclude derived live-event logs from full backups once their recovery purpose
   is explicitly defined, or compact them before snapshotting.
4. Segment and rotate event logs. Preserve only the replay window required by
   clients; maintain the latest sequence separately.
5. Move toward incremental or content-addressed backup storage after consistent
   snapshot semantics are in place.

**Acceptance criteria**

- A documented 30-day storage budget exists and is enforced.
- Backup creation refuses to endanger the configured free-space reserve.
- Event and backup growth are bounded without manual cleanup.
- The UI reports storage pressure before state writes fail.

### F-04: State transitions acknowledge success after durable writes fail (P1)

**Evidence**

- `persistLocked` logs and discards `SaveAgents` errors
  (`internal/hub/hub.go:510-523`).
- Agent communication transitions log and continue after append failure
  (`internal/hub/hub.go:485-495`).
- Inbox, handling-attempt, and Outbox transition helpers do the same
  (`internal/hub/inbox.go:2427-2451`).
- Schedule persistence errors are logged, while create, update, delete, and due
  processing continue (`internal/hub/scheduler.go:14-18`).
- Provider-operation transitions follow the same best-effort pattern.
- In contrast, initial Outbox creation correctly returns an error when append
  fails (`internal/hub/inbox.go:1753-1764`). The durability contract therefore
  changes depending on which transition is being executed.

**Impact**

Memory, SSE observers, API responses, and disk can disagree. After disk-full,
permission, or I/O failures, users can see a sent, renamed, closed, or scheduled
state that vanishes or reverts after restart. External side effects may already
have happened, making replay unsafe.

**Required remediation**

Introduce repository methods with an explicit commit contract:

- validate transition;
- append or atomically replace durable state;
- update the in-memory projection only after commit;
- publish the event only after commit;
- return the write failure to the caller and expose degraded health.

For operations with an external side effect, persist intent before dispatch and
persist provider receipt with an attempt fence afterward. Avoid trying to add a
database transaction abstraction around the current best-effort helpers without
first defining each aggregate's invariants.

### F-05: Corrupt state can be replaced by an empty registry during startup (P1)

**Evidence**

`hub.New` logs load errors and substitutes empty maps
(`internal/hub/hub.go:319-360`). Startup reconciliation then unconditionally
calls `persistLocked` (`internal/hub/hub.go:371-402`). `SaveAgents` writes
`agents.json` followed by a separate compatibility `sessions.json`
(`internal/store/store.go:215-221`).

Malformed NDJSON records and event records are silently skipped rather than
surfacing a degraded/corrupt state (`internal/store/store.go:369-405` and
`445-453`).

**Impact**

A malformed or partially unreadable canonical registry can be interpreted as
an empty installation and overwritten. Silent record skipping can erase part of
the projected history without an operator knowing recovery is needed.

**Required remediation**

- Fail startup closed for required state, retaining the original file.
- Quarantine corrupt input and emit a machine-readable recovery report.
- Use versioned schemas, checksums, and an explicit migration transaction.
- Stop writing the compatibility mirror as a second source of truth; generate
  it on read or remove it after a migration window.
- Persist startup recovery transitions instead of repairing only in memory.

### F-06: Long-lived Threads have O(total history) hot paths (P1)

**Evidence**

- Listing Agents calls rollout status reconciliation for every Agent.
- `rollout.LatestTurn` scans an entire rollout from the first line
  (`internal/rollout/rollout.go:211-275`).
- History pagination parses the complete rollout, then separately parses usage,
  and only afterward slices the requested page
  (`internal/hub/hub.go:2671-2741`).
- `ReadEvents` scans and allocates every matching event before applying `tail`
  (`internal/store/store.go:432-470`). `LastSeq` therefore reads a complete log
  during startup for each Agent.
- The WebUI reconciles `/api/agents` every 10 seconds
  (`web/src/App.tsx:568-579`).

Observed on the current instance, `/api/agents` took about 0.9 seconds. Fetching
one history item for a large Thread returned about 4 KB but took about 2.5
seconds because it scanned a roughly 188 MB rollout.

**Impact**

The cost of opening, listing, restarting, or observing an Agent grows with its
entire career. This directly conflicts with CodexLoom's core promise that a
Thread becomes a long-lived domain Agent rather than a disposable task.

**Required remediation**

- Build a sidecar rollout index keyed by file identity/size and turn boundaries;
  update it incrementally and rebuild it deterministically when stale.
- Read history pages by byte offsets rather than constructing all Turns.
- Keep a cached latest-turn projection updated from CodexHost events and file
  tailing; do not scan every rollout on every Agent list.
- Store latest event sequence in metadata and segment replay logs.

**Acceptance criteria**

- `/api/agents` p95 below 100 ms with 100 Agents and gigabyte-scale rollouts.
- First history page below 300 ms with bounded memory independent of total
  history size.
- Startup and SSE subscription do not scan complete event logs.

### F-07: Scheduled delivery has a crash window and no durable occurrence (P1)

**Evidence**

`collectDueSchedules` advances `LastRunAt` and `NextRunAt`, best-effort persists
the Schedule, and only then starts an untracked goroutine to send the message
(`internal/hub/scheduler.go:249-323`).

**Failure modes**

- Crash after schedule advancement but before `SendAgentMessage`: occurrence is
  permanently missed.
- Save failure followed by successful delivery: old `NextRunAt` is restored and
  the occurrence can be duplicated after restart.
- Multiple process instances or stale asynchronous completions have no durable
  occurrence identity to fence them.

**Required remediation**

Represent each due occurrence as a durable record with a stable idempotency key
such as `(schedule_id, scheduled_at)`. Commit the occurrence and next schedule
position atomically, then deliver through the normal Agent-message queue. A
crash should leave a retryable occurrence, not an ambiguous timestamp.

### F-08: Outbox claims lack leases and attempt fencing (P1)

**Evidence**

`ClaimNextOutbox` changes `pending` to `sending` and increments `AttemptCount`,
but the command/result protocol does not carry a claim token
(`internal/hub/inbox.go:1786-1808`). `CompleteOutbox` validates ownership but not
that the result belongs to the latest sending attempt
(`internal/hub/inbox.go:1812-1875`). Connector reconnect requeues every sending
item (`internal/hub/inbox.go:1767-1783`).

Slack has no provider-side idempotency key and depends on a bounded local
gateway result cache. A gateway crash after provider success but before cache or
Hub acknowledgement can therefore duplicate a message. Parall's provider-side
idempotency is a stronger implementation and should remain.

**Required remediation**

- Issue a random claim ID and lease expiry for every attempt.
- Require that ID when completing or extending the lease; reject stale results.
- Persist provider idempotency keys and all provider receipt IDs.
- Define provider-specific duplicate semantics where the upstream platform has
  no idempotent send primitive.
- Put timeouts around provider calls so one hung send cannot block a Connection's
  command stream indefinitely.

### F-09: “Graceful restart” drains Agent Turns, not the system (P1)

**Evidence**

- Restart readiness checks active Agent status, but not active Connector sends,
  ProviderOperations, backups, human-response delivery, or schedule firing.
- `Hub.Shutdown` waits for four persistent loops, while schedule, human-answer,
  watchdog, and other goroutines are created outside that wait group
  (`internal/hub/hub.go:403-407` and `2744-2769`).
- The signal handler calls `httpServer.Close()` and then `os.Exit(0)` instead of
  draining requests with `Shutdown(ctx)` (`cmd/codex-loom/main.go:56-64`).

**Impact**

The UI can report a graceful restart while external sends or internal deliveries
are inside their ambiguous side-effect window. Requests are cut rather than
drained, and background completion can race shutdown persistence.

**Required remediation**

Implement an explicit service lifecycle: `running -> draining -> checkpointing
-> stopped`. Stop accepting mutating work, cancel or await all owned goroutines,
wait for claim leases or persist them as retryable, create a consistent snapshot,
then shut down HTTP with a deadline.

### F-10: Backups are not consistent or restore-verified (P1)

**Evidence**

- Backup creation walks live files while Hub and gateways continue writing
  (`internal/backup/backup.go:127-170` and `283-316`).
- If a file shrinks after stat, `addFile` pads the archive entry with zero bytes
  (`internal/backup/backup.go:319-347`).
- A required file that cannot be read becomes a manifest warning and the backup
  still succeeds (`internal/backup/backup.go:127-140`).
- There are no per-file checksums, post-write archive verification, automated
  restore command, or routine restore test.

**Impact**

Files in one archive can describe different logical instants. An archive marked
successful may contain malformed JSON/NDJSON or omit required state. Retention
then removes older archives without evidence that the newest one restores.

**Required remediation**

Create a snapshot barrier or immutable checkpoint first, archive only that
checkpoint, checksum every entry, verify the finished archive, and provide a
restore-to-temporary-directory validation command. Required-file failure must
fail the backup. Never synthesize missing file bytes.

### F-11: Loom state is too broadly readable on disk (P1)

**Evidence**

The store creates its event directory with mode `0755` and JSON/NDJSON/event
files with `0644` (`internal/store/store.go:62-69`, `282-291`, `353-366`, and
`418-429`). The current data directory and ledgers retain those broad modes.

**Impact**

Profiles, messages, file paths, Connector metadata, and detailed tool events are
readable by other local accounts where parent-directory permissions allow it.

**Required remediation**

Create Loom state directories as `0700`, files as `0600`, use secure temp-file
creation, and run an idempotent permission migration at startup. Credentials
must remain in Keychain or an equivalent secret store rather than these files.

### F-12: One global Hub lock couples all domains and synchronous I/O (P2)

**Evidence**

The Hub has one mutex for Agents, runtime state, communication, scheduling,
integrations, Inbox/Outbox, ProviderOperations, subscriptions, and human
requests. There are 147 non-test `h.mu.Lock()` sites. Several locked sections
perform `fsync`, scan rollout/event files, spawn CodexHost, or synchronously call
event callbacks.

The Codex JSON-RPC client's single read loop invokes Hub callbacks synchronously
(`internal/codex/client.go:174-235`). A slow Hub callback therefore delays
unrelated JSON-RPC responses for every Agent sharing the Host.

**Impact**

Head-of-line blocking is system-wide, lock ownership is hard to review, and
domain invariants cannot be isolated. Adding one integration feature increases
the regression surface of Agent runtime and vice versa.

**Required remediation**

Do not replace the mutex with many arbitrary locks. First define aggregate
owners and command queues:

- Agent/Thread runtime;
- internal communication and schedules;
- integration directory and policy;
- Inbox/attempt/Outbox delivery;
- human requests;
- event projection.

Move disk and provider I/O outside projection locks. Dispatch JSON-RPC callbacks
onto bounded per-Thread or per-domain queues while preserving ordering and
backpressure.

### F-13: Domain boundaries exist in documentation more than code (P2)

**Evidence**

- `internal/hub/hub.go` is about 2,770 lines and `internal/hub/inbox.go` about
  2,785 lines.
- `internal/httpapi/server.go` is about 1,918 lines with 116 route registrations.
- `cmd/loom/main.go` is about 2,955 lines with 132 command cases.
- HTTP API code owns credential onboarding and launchd/systemd gateway process
  lifecycle, not just transport adaptation.
- Aggregate states are mostly free-form strings rather than typed transitions.

**Impact**

The DDD vocabulary is useful to product discussions but does not yet protect
business invariants. Transport, orchestration, policy, persistence, and process
management can be changed from the same modules.

**Required remediation**

Extract by behavior and ownership, not file size alone. Start with a typed
Outbox state machine and repository because it has externally visible side
effects. Follow with Schedule occurrences and integration lifecycle. Keep HTTP
handlers and CLI commands as thin adapters over application commands and
queries.

### F-14: The WebUI can miss Thread events after reconnect (P1 product contract)

**Evidence**

The global SSE stream sends snapshots but has no durable event ID, cursor, or
replay (`internal/httpapi/server.go:1313-1353`). The WebUI uses that one stream
and reconciles only Agent summaries on reconnect
(`web/src/App.tsx:490-566`). Open Agent panes receive live Thread events through
an in-memory browser pub/sub; a disconnect gap does not force their history to
reload. The older per-Agent SSE supports replay, but the multi-tab UI no longer
uses one connection per pane.

**Impact**

Mobile, Desktop Remote, Connector, and WebUI can all write the same Thread, but
the WebUI may omit messages or tool events until refresh/remount. Real-time
co-observation is a core product contract, not optional visual polish.

**Required remediation**

Give the global stream a durable monotonically increasing cursor and replay, or
send invalidation checkpoints that make each open pane reconcile history after
reconnect. Test disconnect, missed-event, duplicate replay, compaction, and
multi-tab ordering explicitly.

### F-15: Frontend and API contracts are weakly typed and largely untested (P2)

**Evidence**

- The frontend contains 83 `any` occurrences; the base `LoomEvent.data` and
  approval params are `any` (`web/src/types.ts:1-12`).
- Go response shapes and TypeScript interfaces are maintained manually.
- The largest UI components combine API calls, projection building, automation
  hooks, and rendering: `IntegrationsPane`, `TeamPane`, `App`, and `AgentPane`
  are each over 1,100 lines.
- `package.json` has development/build/preview scripts but no frontend unit,
  component, or end-to-end test command.
- The production frontend build is about 16 MB and warns about large chunks;
  Mermaid and syntax-language bundles dominate several chunks.

**Impact**

Backend contract drift becomes runtime failure, SSE event handling silently
ignores malformed data, and high-risk integration workflows have no browser
regression gate. Large mixed-responsibility components make mobile and live-data
state changes harder to reason about.

**Required remediation**

- Define tagged event unions and validate API payloads at the boundary.
- Generate or share schemas for high-value commands and projections.
- Extract tested projection/state-machine functions before splitting visual
  components.
- Add Vitest/Testing Library for pure projections and Playwright for create,
  send, restart, integration setup, reconnect, and mobile layouts.
- Lazy-load Mermaid and syntax grammars by actual content instead of bundling a
  broad catalog.

### F-16: Release engineering does not match the product's blast radius (P2)

**Evidence**

- There is no CI workflow, Go/static lint policy, frontend test setup, or
  automated release gate in the repository.
- `Makefile` release targets build artifacts but do not run Go tests, race tests,
  gateway tests, dependency audits, security checks, or restore verification.
- Embedded WebUI output is committed: 452 tracked files under
  `internal/webui/dist`. Hashed-asset churn makes incomplete or stale dist
  commits difficult to review.
- At audit time the working tree spans 124 changed files with 9,314 insertions
  and 4,751 deletions. This is not itself a bug, but it demonstrates that the
  current release unit is too broad for reliable rollback and review.
- A public repository policy surface such as `SECURITY.md`, contribution rules,
  and a license is absent.

**Required remediation**

Create a required CI matrix for Go tests/vet/race, gateway tests, frontend
typecheck/build/tests, dependency audit, `git diff --check`, generated-dist
consistency, and a minimal backup/restore drill. Produce WebUI assets in a
deterministic release step and verify their manifest against source rather than
reviewing hundreds of hash renames manually.

## Verification Performed

The following checks passed on the audited worktree:

- `go vet ./...`
- `go test -race ./...` (the HTTP package took about 81 seconds)
- `go test -cover ./...`
- `node --test gateway/*.test.mjs` (36 tests)
- `npm run build`
- `npm audit --omit=dev --json` (zero reported vulnerabilities)

Coverage identifies risk concentration rather than proving correctness:

| Package/surface | Observed coverage |
| --- | ---: |
| `internal/hub` | 59.2% |
| `internal/httpapi` | 30.8% |
| `internal/store` | 17.2% |
| `internal/codex` | 0% |
| Go gateway commands | 0% |
| React WebUI | no test runner |

`staticcheck` and `govulncheck` were not installed in the audit environment, so
they were not run. Passing race/build tests do not cover authorization,
disk-full behavior, crash consistency, restore validity, stale Connector
results, or SSE reconnect gaps.

### 2026-07-15 remediation verification

The functional-completeness remediation was verified independently from the
original audit baseline:

- `make build` rebuilt the service, reloader, CLI, and all four managed Gateway
  binaries.
- `go test ./...`, `go vet ./...`, and `go test -race ./...` passed after the
  persistence, lifecycle, event, and Connector changes.
- `node --test gateway/*.test.mjs` passed all 36 protocol tests.
- `npm run build` passed and regenerated the embedded WebUI distribution.
- A separate Hub process was started on port 4881 with a temporary data
  directory. Its health endpoint and embedded WebUI were exercised over HTTP.
- The canary created a real gzip archive. Its version 2 manifest listed the
  included files and explicitly excluded `events/**`; the prune endpoint
  returned the configured count, byte, and age policy.
- A disabled Schedule created through HTTP produced a durable global event with
  an SSE cursor. Reconnecting with `since=0` replayed the persisted event IDs.
- The canary received an interrupt, completed its graceful shutdown, restarted
  with the same state, and the already-open WebUI recovered to 12 Agents without
  a browser reload.
- After the structural refactor, a second canary exercised representative
  System, Integration, Agent, Organization, and compatibility routes. The split
  CLI successfully listed Agents and created a compressed backup against that
  instance before another graceful shutdown.
- `go test ./internal/rollout -run '^$' -bench BenchmarkReadWindowLongThread
  -benchtime=5x -count=3` measured a 50,000-Turn cold index at 164-166 ms and a
  warm latest-10 window at 124-131 microseconds on the Apple M4 audit host.

Focused failure-injection tests also verify corrupt-startup rejection,
commit-before-publish rollback for Inbox, Integration, and Schedule state,
durable Schedule occurrence deduplication, stale Connector result fencing,
global SSE replay/reconcile, and shutdown waiting for registered workers.
Production restart and post-restart checks remain an operator action because
the Hub must never terminate its own serving process during development.

The structural pass reduced the former cross-domain files to bounded adapters:
`hub.go` now contains shared runtime/event infrastructure; Agent lifecycle and
internal Communication are separate modules; Integration, Inbox, Outbox, and
external-message policy are separate modules; `server.go` delegates to five
route registrars; and `cmd/loom/main.go` delegates to five command domains.

## Remediation Order

### Phase 0: Contain immediate risk

1. Stop exposing the default control plane on all interfaces; require
   authentication before re-enabling remote access.
2. Remove wildcard CORS and the arbitrary-path image endpoint.
3. Pause automatic full backups until free space is recovered and a byte budget
   exists. Review and remove old archives only through an explicit operator
   action; do not automate destructive cleanup as part of startup.
4. Add free-space health reporting and fail writes loudly.
5. Migrate state permissions to `0700`/`0600`.

### Phase 1: Make state transitions trustworthy

1. Define authenticated operator, observer, and Connector principals.
2. Make required-state load failures stop startup instead of overwriting input.
3. Introduce commit-before-publish repositories for Agent, communication,
   Inbox/Outbox, Profile, and integration transitions.
4. Add typed Outbox states, claim leases, attempt fencing, and provider timeouts.
5. Make graceful restart drain every owned work class and use HTTP shutdown.

### Phase 2: Make long-lived Agents scale

1. Add rollout turn indexes and bounded history reads.
2. Segment event logs and persist latest sequence metadata.
3. Add durable Schedule occurrences.
4. Build consistent, checksummed, restore-tested checkpoints with byte/age
   retention.
5. Add global SSE cursor/replay or deterministic pane reconciliation.

### Phase 3: Strengthen domain and delivery boundaries

1. Extract application commands and repositories by aggregate ownership.
2. Thin the HTTP and CLI adapters and formalize versioned API schemas.
3. Add frontend projection tests, reconnect E2E tests, and mobile workflow tests.
4. Establish CI, release provenance, generated artifact verification, and public
   security/contribution policy.

## Foundations To Preserve

Technical-debt repayment should not discard the parts already aligned with the
product:

- Keep one shared CodexHost and the app-server protocol boundary.
- Keep Codex rollout files as canonical Thread history; add indexes, not a second
  transcript database.
- Keep stable Agent, Message, Membership, Inbox, Outbox, and Artifact IDs.
- Keep the durable Inbox/Outbox model and strengthen its transition semantics.
- Keep Connector credentials hidden behind managed identities and secret stores.
- Keep Profile and organization concepts independent from external Conversation
  roles.

The target is a trustworthy control plane around these foundations, not a
rewrite of the product model.

## Audit Maintenance

Update this document when a finding is accepted, superseded, or fixed. A fix is
not complete when a build turns green; attach its failure-injection or real-path
verification to the finding and record the new invariant. In particular, every
P0/P1 closure should include the relevant negative test: unauthorized request,
disk-full write, corrupt startup, process crash window, stale delivery result,
restore drill, or SSE disconnect.
