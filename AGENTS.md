# Repository Instructions

## Local Owner Trust Principle

CodexLoom is a local-first, single-owner application. Unless a task explicitly
introduces a different trust boundary, trust the Owner and processes running
under the same local user account. Do not design against a malicious local
Owner or a malicious same-UID process.

Engineering priorities, in order, are:

1. Deliver useful and innovative product functionality.
2. Preserve correctness, reliability, and a low bug rate.
3. Provide a clear, efficient user experience.
4. Keep the implementation simple and maintainable.
5. Add security controls proportional to a concrete, currently reachable
   boundary.

Security is part of correctness when a real boundary exists, but speculative
hardening must not block product work. Before adding security complexity,
identify the concrete actor, reachable entry point, affected asset, realistic
impact, and smallest effective mitigation. If the scenario requires the
attacker to already control the Owner account or an arbitrary same-UID process,
it is outside the default threat model.

Apply these rules:

- Build for currently supported platforms. Prefer an explicit unsupported
  result over a premature platform-specific permission or ACL framework.
- Prefer fixed locations, generated identifiers, and narrow internal inputs
  over general interfaces that require broad validation machinery.
- Do not add authentication, authorization, CSRF, multi-user, or tenant
  infrastructure for APIs, sharing modes, or trust boundaries that do not
  exist in the current product.
- Distinguish reliability from speculative hardening. Atomic writes, recovery
  from partial failure, clear errors, and prevention of ordinary data loss are
  correctness requirements when the feature needs them.
- Treat remote callers, external messages, imported files, cross-user access,
  and third-party-controlled input as untrusted at the boundary where they
  actually enter the product.
- Keep explicit authorization and auditable results for external sends,
  destructive actions, credential migration, install/restart, deployment, and
  production changes.
- Keep secrets out of logs, errors, public responses, process arguments, and
  ordinary backups. For local storage, use the simplest owner-only file
  protection supported by the target platform and current feature.
- Do not introduce a framework when a direct implementation satisfies the
  current product contract.

Reviewers must not block a change on an out-of-scope hypothetical threat. A
security finding must name the concrete boundary, reachable scenario, user
impact, and smallest proportional fix. When those cannot be shown, record the
idea as future hardening rather than expanding the current scope.

The default review question is: does this complexity solve a current user
problem or a demonstrated reachable failure, or does it only defend against an
attacker who already controls the local Owner account?

## Frontend package manager

The WebUI uses pnpm 11.1.2, pinned by the `packageManager` field in
`web/package.json`. Use `web/pnpm-lock.yaml` as the only frontend lockfile and
install dependencies with `pnpm --dir web install --frozen-lockfile`.

- Run frontend scripts with pnpm, for example `pnpm --dir web test` and
  `pnpm --dir web run dev`.
- Do not add or regenerate npm or Yarn lockfiles.
- `make build` remains the canonical production build and embed entrypoint. It
  verifies the pnpm version, performs a frozen install, builds the WebUI, then
  compiles and verifies the embedded Go binary.

## Production build and restart

CodexLoom embeds `internal/webui/dist` into the Go server binary at compile
time. A process restart does not reread files from that directory.

- Use `make build` (or the compatibility alias `make release`) before a
  production restart. The target always builds the WebUI first, compiles the
  binaries second, and verifies that `bin/codex-loom` contains the current Vite
  entrypoint.
- Do not publish or restart a production binary created with a bare
  `go build ./cmd/codex-loom`; it may embed stale frontend assets.
- After restart, verify `GET /api/version`: `build.webAsset` must match the
  module entrypoint in `internal/webui/dist/index.html`.
- When a frontend feature adds an API, verify that API returns JSON after the
  restart. An HTML response from an `/api/...` URL means the running binary
  does not contain that route and the SPA fallback handled the request.
