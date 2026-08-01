# Native Windows (experimental M0)

CodexLoom does not yet claim native Windows support. This document describes
the current **M0 proof-of-life scope** tracked in
[issue #2](https://github.com/yan5xu/codexloom/issues/2): every CodexLoom
binary compiles for `windows/amd64` and `windows/arm64`, a documented
PowerShell build path exists, and features outside M0 refuse clearly instead
of appearing to work. Until the M0 acceptance criteria have been validated on
a real Windows machine, **WSL2 remains the recommended path for Windows
users** (build and complete one read-only Turn there before relying on it).

## Target environment

| Component | Version |
|---|---|
| OS | Windows 11 x64 (arm64 compiles; unvalidated) |
| Shell | PowerShell 5.1 or PowerShell 7+ |
| Go | 1.25.x |
| Node.js / npm | current LTS (for the WebUI build) |
| Git | any recent version |
| Codex CLI | authenticated native install (`codex app-server` must run) |

No WSL and no Git Bash are required by the build path below.

## Build

From a clean checkout, in PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1
```

This mirrors `make build`: it builds the React console into
`internal/webui/dist` (embedded by Go at compile time), builds all binaries
into `bin\` with version metadata, creates the legacy `codex-hub*`/`chub*`
aliases, and fails if the compiled Hub does not embed the current WebUI
entrypoint. Use `-SkipWeb` only when `internal/webui/dist` is already current.

## Run

```powershell
bin\codex-loom.exe -bind 127.0.0.1 -port 4870
```

`-bind 127.0.0.1` keeps the Hub loopback-only, which is the M0 contract (and
avoids the Windows Firewall prompt for inbound access). The data directory
defaults to `%USERPROFILE%\.codex-loom`; override with `-data` or
`CODEX_LOOM_DATA`. Then:

- open `http://127.0.0.1:4870` in a browser (WebUI);
- `bin\loom.exe agent list` for the CLI;
- `GET http://127.0.0.1:4870/api/version` must report the expected commit,
  `windows/amd64`, the embedded WebUI asset, and the data directory.

## M0 validation checklist

Run on a real Windows machine — macOS/Linux cross-compilation is compile
evidence only:

1. Clean checkout builds via `build.ps1` without WSL/Git Bash.
2. `/api/version` reports the expected commit, OS/arch, WebUI asset, and data
   directory.
3. CLI and WebUI reach the loopback Hub.
4. A native authenticated `codex app-server` initializes; create or resume one
   Agent and complete one `read-only + never` Turn.
5. With no active Turn, stop the Hub (Ctrl+C) and start it again; the same
   stable Agent ID, primary Thread ID, and readable history return.
6. Excluded features (below) report unsupported instead of appearing to work.

## Known limitations on native Windows

- **Managed restart / reloader.** The in-place restart flow starts the
  reloader, but Windows has no cross-console graceful signal: the reloader
  stops the old Hub with a hard `TerminateProcess` instead of SIGTERM. Prefer
  a manual idle stop (Ctrl+C) and restart. Automatic restart is out of M0.
- **Managed Integration gateways.** Feishu/Slack/Parall gateway management
  requires launchd or systemd and reports
  `not supported on windows`. The Slack and Parall wrapper binaries refuse to
  start on Windows (they rely on Unix `exec` semantics).
- **`loom ... --agent-key-file`.** Owner-only permission verification is not
  implemented for Windows ACLs; the flag refuses with a clear error. Run
  credential imports from WSL2 or a validated Unix host.
- **Reloader/Hub log defaults.** `/tmp` does not exist on Windows; logs
  default to `%TEMP%\codex-loom.log` and `%TEMP%\codex-loom-reloader.log`
  (override with `CODEX_LOOM_LOG` / `CODEX_LOOM_RESTART_LOG`).
- **Legacy `~/.codex-hub` migration.** The rename leaves a symlink behind for
  legacy paths; creating symlinks on Windows may require Developer Mode or
  elevation. Fresh installs are unaffected.
- **Backup/restore, credential storage, browser startup** compile but carry no
  native Windows validation claim yet.

## Rollback

M0 changes nothing about validated platforms. If a native Windows attempt
fails, fall back to the documented WSL2 workaround: complete a local build and
a first read-only Turn inside WSL2 before relying on it for real work. Durable
state lives entirely under the data directory; removing
`%USERPROFILE%\.codex-loom` (after backing it up) returns the machine to a
clean state.
