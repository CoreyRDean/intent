# intent — v1 specification

> This document is the binding implementation contract for v1. It is downstream of [`INTENT.md`](../INTENT.md). Anything here that conflicts with INTENT.md is wrong; fix the spec, not the constitution.

Status: **draft, frozen for v1 unless explicitly versioned.**

## 1. Surface

### 1.1 Binary names

The project ships two binaries:

- `intent` — primary, full name.
- `i` — symlink to `intent`. Behaviorally identical.

### 1.2 Invocation modes

`intent` selects a mode by inspecting the first non-flag argument, unless `--literal` forces natural-language mode:

| Mode | Trigger | Example |
|---|---|---|
| **Subcommand** | First non-flag arg matches a known subcommand | `i config get model` |
| **Natural language** | Any other input, or any argv tail after `--literal` | `i check if google's dns is up` |

There is no "shell" or "REPL" mode in v1. Conversational follow-up (`i and now sort by date`) is single-shot and uses the cached previous turn for context; it is not a persistent session.

### 1.3 Subcommands

Frozen for v1. New top-level subcommands require a spec amendment.

| Subcommand | Purpose |
|---|---|
| `init` | First-run wizard: model selection, daemon install prompt, shell-completion install. |
| `doctor` | Diagnose installation, model integrity, daemon status, sandbox tooling, GPU. |
| `config` | `get`, `set`, `edit`, `path`. Manage `config.toml`. |
| `model` | `list`, `pull`, `use`, `rm`. Manage local models. |
| `daemon` | `start`, `stop`, `status`, `logs`, `install`, `uninstall`. |
| `history` | List, show, replay prior interactions. |
| `pin` | Promote an accepted command to a named, deterministic skill. |
| `run` | Run a pinned skill by name. |
| `explain` | Reverse mode: explain what an arbitrary shell command does. |
| `fix` | Re-attempt the last failed run with stderr context. |
| `report` | Convert natural language into one or more GitHub issues, dedupe against existing. |
| `update` | `check`, `now`, `auto`, `off`. Self-update. |
| `version` | Print version, commit, build date, model. |

Top-level flags that are *not* subcommands but change global behavior:

| Flag | Purpose |
|---|---|
| `--uninstall` | Remove binary, daemon, and state. Confirms before destroying anything. |
| `--update` | Equivalent to `update check`; with arg `now`, `auto`, `off`, equivalent to subcommand. Aliased here for muscle memory. |
| `--version`, `-V` | Print version and exit. |
| `--help`, `-h` | Help text. |

### 1.4 Flags for natural-language mode

| Flag | Default | Effect |
|---|---|---|
| `--yes`, `-y` | off | Auto-confirm `safe` and `network` risk levels. Never auto-confirms `mutates`, `destructive`, or `sudo`. |
| `--dry` | off | Print what would happen; do not execute. Sets `risk` policy to never run. |
| `--literal` | off | Treat everything after this flag as natural-language prompt text, even if it looks like a subcommand or another intent-mode flag. |
| `--sandbox` | off | Execute under platform sandbox (`bwrap` on Linux, `sandbox-exec` on macOS). |
| `--ro` | off | Cwd bind-mounted read-only inside sandbox. Implies `--sandbox`. |
| `--json` | auto | Emit structured response on stdout. Auto-on when stdout is not a TTY and stdin is from another `i`. |
| `--raw` | off | Emit only the generated command on stdout. Implies `--quiet`. |
| `--quiet`, `-q` | auto | Suppress spinner and decoration. Auto-on when stdout is not a TTY. |
| `--bool` | off | Force the model to produce a yes/no answer. Maps to exit code 0/1. Implies `--quiet` unless TTY. |
| `--explain` | off | Show plain-English breakdown without running. |
| `--no-cache` | off | Do not consult or write the skill cache for this invocation. |
| `--from-intent` | auto | Treat stdin as additional natural-language context, not as data. Auto-on when stdin marker indicates upstream is `intent`. |
| `--context <key=value>` | (repeatable) | Inject ad-hoc context into the model prompt. |
| `--model <name>` | from config | Override model for this call. |
| `--backend <name>` | from config | Override backend for this call. |
| `--timeout <duration>` | `60s` | Hard cap on model + execution wall time. |
| `-n <N>` | 1 | Generate N alternative approaches; user picks. |

### 1.5 Default UX flow (TTY mode)

```
$ i check if google's dns is up
  Understanding... (380ms)
  → ping -c 1 -W 1 8.8.8.8
    Send one ICMP echo to 8.8.8.8 and report whether it responded.
    risk: network · runtime: instant
  [Enter] run · [p] preview · [e] edit · [n] cancel
```

Status lines render in this exact order, only as needed:

1. `Invoking…` — spawning backend, before any model token.
2. `Constructing…` — runtime or model not yet downloaded; downloading now. Shown with byte progress.
3. `Understanding…` — model is generating tokens. Replaced by elapsed time when complete.
4. `Verifying…` — running safety guard. Almost always sub-millisecond; rendered only if it takes >50ms.
5. The proposed command, description, risk, runtime estimate.
6. The action prompt.

Cached responses skip 1–4 and show a `⚡` indicator on the proposed command.

### 1.6 Default UX flow (non-TTY mode)

When stdout is not a TTY:

- No spinners. No status lines. No colors.
- No interactive prompt.
- If `--yes` is set or risk is `safe`/`network`, run silently and pipe the executed command's stdout/stderr/exit code through unchanged.
- If `--yes` is not set and risk is higher, **fail closed**: exit non-zero with a stderr message indicating that confirmation was required and `--yes` was not passed.
- If `--json` is requested or auto-enabled, emit one JSON object on stdout per invocation.

This is the silent contract that makes `i ... | i ... | i ...` work.

### 1.7 Inter-`intent` wire format

When `intent` writes to a pipe, it sets the env var `INTENT_PIPE_FROM=intent` for the child process. When `intent` starts and detects this var on stdin (via parent process inspection or env passthrough through `sh -c`), it auto-enables `--json` for input parsing and `--from-intent` semantics.

The wire format is a JSON object with at least `intent_response`, plus execution metadata the downstream turn may need for context preservation:

```json
{
  "intent_response": ResponseObject,
  "prompt": "original natural-language request",
  "cwd": "/absolute/working/directory",
  "exit_code": 0,
  "stdout": "captured stdout when execution happened"
}
```

`prompt` and `cwd` exist specifically so chained invocations can keep path and target context even when `stdout` only contains bare filenames or other abbreviated output.

## 2. Model contract

The model — regardless of backend — is required to return JSON conforming to the schema below. Local backends enforce this via grammar-constrained sampling. Cloud backends enforce it via `response_format: {type: "json_schema"}` or equivalent. If a backend cannot enforce schema, `intent` validates after-the-fact and rejects on parse failure.

### 2.1 Response schema

```json
{
  "intent_summary": "string, one sentence",
  "approach": "command | script | tool_call | clarify | refuse | inform",
  "command": "string?",
  "script": {
    "interpreter": "bash | sh | python3 | node | ...",
    "body": "string"
  },
  "tool_call": {
    "name": "list_dir | read_file | head_file | which | stat | env_get | cwd | os_info | git_status | help | grep | find_files | web_fetch | ask_user",
    "arguments": { ... }
  },
  "clarifying_question": "string?",
  "refusal_reason": "string?",
  "stdout_to_user": "string?",
  "description": "string",
  "risk": "safe | network | mutates | destructive | sudo",
  "needs_sudo": "boolean",
  "expected_runtime": "instant | seconds | minutes | long",
  "alternatives": [
    { "command": "string", "description": "string", "risk": "..." }
  ],
  "confidence": "low | medium | high"
}
```

Required fields per `approach`:

| approach | required additional fields |
|---|---|
| `command` | `command`, `description`, `risk`, `expected_runtime`, `confidence` |
| `script` | `script`, `description`, `risk`, `expected_runtime`, `confidence` |
| `tool_call` | `tool_call` |
| `clarify` | `clarifying_question` |
| `refuse` | `refusal_reason` |
| `inform` | `stdout_to_user` |

### 2.2 Risk levels

| level | meaning | autorun-eligible with `--yes`? |
|---|---|---|
| `safe` | Read-only or self-contained computation. No network, no filesystem mutation, no privilege escalation. | yes |
| `network` | Makes outbound network requests. No filesystem mutation. | yes |
| `mutates` | Writes to the filesystem within the user's normal working area (cwd, $HOME, /tmp). | no |
| `destructive` | Deletes, overwrites, or moves files; partitions disks; truncates tables; etc. | no |
| `sudo` | Requires elevated privileges. | no |

The model classifies; the static guard (§3) can override upward but never downward.

### 2.3 Tool catalog (read-only only)

Tools the model may invoke during a multi-step turn. All are read-only (except `ask_user`, which reads one line from the user's TTY) and execute without confirmation. Hard-bounded by `max_tool_steps` (default 12).

| Name | Arguments | Returns |
|---|---|---|
| `list_dir` | `{path: string, depth?: int=1, max_entries?: int=200}` | List of `{name, path, type, size}` relative to the requested directory; `depth>1` recurses |
| `read_file` | `{path: string, max_bytes?: int=8192, start_line?: int, end_line?: int}` | `{content, truncated, size, total_lines?, start_line?, end_line?}` |
| `head_file` | `{path: string, lines?: int=50}` | `{lines, total_lines}` |
| `which` | `{name: string}` | `{found: bool, path?: string}` |
| `stat` | `{path: string}` | `{exists, type, size, perms, mtime}` |
| `env_get` | `{name: string}` | `{found: bool, value?: string}` — denylisted patterns return `redacted: true` (see §3.4) |
| `cwd` | `{}` | `{path}` |
| `os_info` | `{}` | `{os, arch, kernel, distro, shell}` |
| `git_status` | `{}` | `{is_repo, branch, dirty, files_changed}` (cwd only) |
| `help` | `{name: string, max_bytes?: int=8192}` | `{found, path, strategy, output, tried}` — probes `--help`, `-h`, `help`, then `man`. Refuses shell-metachar names. |
| `grep` | `{pattern: string, path?: string=".", max_matches?: int=100, case_insensitive?: bool}` | `{matches: []string, count, truncated, tool}` — wraps `rg` if available else `grep -rEn`. |
| `find_files` | `{pattern: string, path?: string=".", max_results?: int=200, type?: "file"\|"dir"\|"any"}` | `{paths: []string, count, truncated, tool}` — wraps `fd` if available else `find`. |
| `web_fetch` | `{url: string, max_bytes?: int=32768}` | `{status, content_type, body, truncated, url}` — http(s) only; HTML is stripped to text. |
| `ask_user` | `{question: string, choices?: []string}` | `{answered: bool, answer?: string, error?: string}` — requires an interactive TTY; otherwise `answered=false`. |

Tool calls outside this catalog are rejected and fed back to the model as an error.

### 2.4 Multi-step turn protocol

```
1. User input + system context → model
2. Model returns response with approach=tool_call
3. intent executes the tool, captures result
4. intent appends {role: "tool", name, result} to conversation
5. Goto 2, up to max_tool_steps times
6. Model returns response with approach in {command, script, clarify, refuse, inform}
7. intent renders to user (or executes, if auto-confirm and risk allows)
```

If the model exceeds `max_tool_steps`, intent forces a `refuse` with reason `"exceeded tool-call budget"` and does not execute anything.

### 2.5 System prompt structure

The system prompt is assembled by intent at runtime and includes:

- The schema and an instruction to always conform to it.
- The tool catalog and an instruction that tool calls must come from it.
- OS, kernel, shell, cwd.
- Output of `which` for ~30 commonly-needed binaries (rg, fd, jq, curl, etc.) — cached.
- Git context, if cwd is a repo.
- Project-level `.intentrc` directives, if present.
- The user's `--context` flags.

The exact prompt template is versioned and lives in `internal/model/prompt.go`. Changes to the template are spec amendments.

## 3. Safety layer

Safety is structural code, not model instructions. The model is never the last line of defense.

### 3.1 Pipeline order

```
model_response
  → schema_validate
  → static_guard (may bump risk; may hard-reject)
  → cache_check
  → confirm (or auto-confirm rules)
  → execute
  → audit_log
```

Each step can short-circuit. `static_guard` and `audit_log` cannot be disabled.

### 3.2 Static guard

A deterministic check that runs against the generated command/script. Patterns are versioned in `internal/safety/guard_patterns.go`. The guard:

- **Hard-rejects** patterns that can plausibly cause catastrophic, irrecoverable damage (`rm -rf /`, `dd of=/dev/sd*`, `mkfs.*` on a non-loop device, `:(){:|:&};:`, `chmod -R 777 /`, `> /dev/sd*`, untrusted `curl ... | sh`).
- **Bumps risk to `destructive`** for: `rm -r`/`rm -rf` outside `/tmp`, `find ... -delete`, `mv` overwriting outside cwd, truncating writes (`> file`) outside cwd or `/tmp`, `git reset --hard` on dirty tree.
- **Bumps risk to `sudo`** if `sudo`/`doas`/`pkexec` appears and `needs_sudo` was false.
- **Bumps risk to `mutates`** for any write-implying primitive that the model classified `safe`.
- **Bumps risk to `network`** for `curl`, `wget`, `nc`, `ssh`, `scp`, `rsync` over network if the model classified `safe`.

Hard-rejects emit a `refuse` response with reason and are recorded in the audit log with the original model output.

### 3.3 Sandbox modes

| Mode | Linux | macOS |
|---|---|---|
| `--dry` | Don't execute. Pretty-print. | Same. |
| `--sandbox` | `bwrap --ro-bind / / --bind <cwd> <cwd> --proc /proc --dev /dev --tmpfs /tmp --new-session --die-with-parent` | `sandbox-exec` with a generated profile that allows read-everywhere, write-cwd-and-tmp, network. |
| `--ro` | As above with `--ro-bind <cwd> <cwd>`. | Generated profile denies write everywhere. |

If the required sandbox tool isn't installed, `intent` fails closed with an actionable message; it never silently runs unsandboxed when `--sandbox` was requested.

### 3.4 Secret redaction

A regex set covering common secret patterns runs over: anything written to `audit.jsonl`, anything `intent --json` emits, anything `i export` produces, and any value returned by `env_get`. Patterns include AWS keys, GitHub tokens, JWT-looking strings, hex blobs ≥64 chars, env var names matching `(?i)(token|secret|password|api[_-]?key|auth)`. Matches are replaced with `<redacted:type>`.

Redaction is best-effort and documented as such. The user is told in `i doctor`.

### 3.5 Audit log

Append-only JSONL at `<state_dir>/audit.jsonl`. One line per turn. Schema:

```json
{
  "ts": "RFC3339",
  "id": "uuidv7",
  "version": "intent version",
  "backend": "string",
  "model": "string",
  "prompt": "string (redacted)",
  "context": { ... },
  "model_response": { ... },
  "guard_actions": ["bumped_to_destructive", ...],
  "user_decision": "confirmed | cancelled | edited | autorun | dry",
  "executed_command": "string?",
  "exit_code": "int?",
  "stdout_hash": "sha256?",
  "stderr_hash": "sha256?",
  "stderr_excerpt": "string?",
  "duration_ms": "int?"
}
```

The user can `i history`, `i history show <id>`, or open the file. They can delete it with `i history clear` or `rm <state_dir>/audit.jsonl` — the binary recreates it on next run.

## 4. State directory layout

Resolved as:

| OS | Path |
|---|---|
| Linux | `${XDG_STATE_HOME:-$HOME/.local/state}/intent/` |
| macOS | `$HOME/Library/Application Support/intent/` |
| Windows (future) | `%LOCALAPPDATA%\intent\` |

Cache (models, runtime binaries) is separate and resolved to:

| OS | Path |
|---|---|
| Linux | `${XDG_CACHE_HOME:-$HOME/.cache}/intent/` |
| macOS | `$HOME/Library/Caches/intent/` |
| Windows (future) | `%LOCALAPPDATA%\intent\Cache\` |

Layout:

```
<state>/
  config.toml          # user config
  audit.jsonl          # append-only audit log
  skills/              # pinned skills, one TOML file per skill
  history/             # per-day rotated history index (parsed from audit.jsonl)
  daemon.sock          # unix socket (Linux/macOS only)
  daemon.pid

<cache>/
  runtime/             # llamafile binaries, versioned
    llamafile-0.10.0
  models/              # GGUF files
    qwen2.5-coder-7b-instruct-q4_k_m.gguf
  context/             # cached `which`, `os_info`, etc.
  skills_cache.db      # sqlite or bolt; cached deterministic responses
```

## 5. Daemon

### 5.1 When the daemon exists

The daemon is **opt-in**, prompted on first run with default `Y`. If declined, every invocation cold-loads the model. If accepted, a platform launch agent keeps `intentd` running.

| Platform | Mechanism |
|---|---|
| macOS | LaunchAgent at `~/Library/LaunchAgents/sh.intent.daemon.plist` |
| Linux | systemd user unit at `~/.config/systemd/user/intent.service` |
| Windows (future) | Task Scheduler entry |

`intentd` is a separate binary symlinked to `intent` with arg `__daemon__` (so we ship one binary).

### 5.2 Protocol

Unix domain socket at `<state>/daemon.sock`, line-delimited JSON. One request per connection, server closes after response. Schema:

```json
// request
{
  "op": "complete | tool_result | health | flush_cache",
  "id": "uuid",
  "payload": { ... }
}

// response (may be multiple, ending with {"final": true})
{
  "id": "uuid",
  "type": "token | response | error | final",
  "payload": { ... }
}
```

Tokens stream when the CLI is in TTY mode and asks for streaming. The daemon owns: model loading/unloading, the skill cache, the context cache, and tool execution scheduling. The CLI owns: TTY rendering, user prompts, executing the final shell command.

### 5.3 Lifecycle

- Start: explicit `i daemon start` or LaunchAgent/systemd.
- Idle timeout: 30 min (configurable). On timeout, daemon stays alive but unloads the model from memory; reloads on next request.
- Shutdown: `i daemon stop` or signal.
- Crash: launch agent restarts. If three crashes in 60s, agent backs off and `i doctor` reports.

## 6. Skill cache

Cached deterministic responses keyed on:

```
sha256(
  normalized(prompt) || ":" ||
  cwd_fingerprint   || ":" ||
  os || ":" ||
  available_binaries_fingerprint || ":" ||
  model_name || ":" ||
  prompt_template_version
)
```

`normalized(prompt)`: lowercased, whitespace-collapsed, common stopwords stripped, ordering of quoted segments preserved.

`cwd_fingerprint`: hash of `(basename(cwd), is_git_repo, git_remote_url_or_empty)`. Deliberately *not* the full path, so the same intent in `~/proj-a` and `~/proj-b` doesn't share a cache, but the same intent in two clones of the same repo does.

Cache is consulted only after schema_validate and static_guard would have passed for the cached response — i.e., we re-run the static guard on the cached command, in case patterns were updated.

`--no-cache` skips read and write. `i pin <name>` upgrades a cached entry to a named skill. `i forget <hash|prompt>` evicts.

## 7. Backend interface

```go
type Backend interface {
    Name() string
    Available(ctx context.Context) error                                  // health check
    Complete(ctx context.Context, req CompleteRequest) (Response, error)  // single round
    Stream(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) // optional
}

type CompleteRequest struct {
    Messages         []Message
    SchemaJSON       []byte             // for grammar / response_format
    GrammarGBNF      string             // optional, for llama.cpp
    Temperature      float64
    MaxTokens        int
    Seed             *int64
}
```

Implementations in v1:

- `llamafile-local` (default): spawns/uses `llamafile --server` against a downloaded GGUF.
- `llamafile-network`: HTTP to a configured `http://host:port`.
- `ollama`: HTTP to local Ollama.
- `openai`: OpenAI-compatible HTTP, configurable base URL (covers OpenAI, OpenRouter, vLLM).
- `anthropic`: Messages API.

A backend that does not natively support schema-constrained output must validate after generation and may retry once with an instruction to fix the JSON.

## 8. Configuration

`config.toml` example with all defaults:

```toml
backend       = "llamafile-local"
model         = "qwen2.5-coder-7b-instruct-q4_k_m"
auto_run      = false             # equivalent to always passing --yes
sandbox       = false             # default execution sandbox
max_tool_steps = 5
timeout        = "60s"
update_channel = "stable"         # "stable" | "nightly" | "off"
auto_update    = false

[ui]
spinner_style = "dots"
color         = "auto"

[daemon]
enabled       = true
idle_unload_after = "30m"
host          = "127.0.0.1"       # loopback only; remote hosts are rejected

[cache]
enabled    = true
max_entries = 5000

[backends.llamafile-local]
runtime_version = "0.10.0"
gpu_layers      = 999

[backends.llamafile-network]
endpoint = "http://localhost:8080"

[backends.openai]
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
model    = "gpt-4o-mini"
```

Project-level `.intentrc` (TOML) overrides global config for invocations whose cwd is at or below the file's directory.

For `llamafile-local`, the daemon-managed model HTTP endpoint is loopback-only by contract. `daemon.host` may be omitted or set to a loopback value such as `127.0.0.1`, `localhost`, or `::1`; non-loopback hosts are rejected.

## 9. Update channel

- `stable`: tagged releases of the form `vMAJOR.MINOR.PATCH`.
- `nightly`: pre-releases of the form `vMAJOR.MINOR.PATCH-<unix-timestamp>`, auto-tagged from `main` HEAD nightly.
- `off`: never check.

Update behavior:

- On first invocation of the day (or first since daemon start), check GitHub Releases asynchronously. Cache the latest version for 5 hours.
- If a newer version exists on the user's channel, show a one-line banner once per session: `update: v0.4.2 → v0.5.0 available · run "i update now"`.
- `i update auto`: write `auto_update = true`. The daemon performs the check every 5 hours and, if a new version exists, downloads it to `<cache>/runtime/intent-<version>.staged` and atomically swaps in on next idle period (no commands in flight).
- `i update off`: disables checks entirely.
- `i --uninstall` removes the binary, the daemon, the launch agent, and (after a `[y/N]` confirmation) the state directory.

## 10. `i report`

Converts natural language into one or more GitHub issues against `CoreyRDean/intent`:

`i report` accepts natural language from argv, stdin, or both. When both are present, argv text comes first and stdin is appended after it.

1. Parse the user's input. The model returns an array of `{title, body, labels, kind}` proposals.
2. For each proposal, query GitHub Search (`is:issue repo:CoreyRDean/intent <terms>`) for the top 5 candidates.
3. For each candidate, compute a similarity score (token-set ratio + embedding-similarity if a small embedding model is available; otherwise just token-set ratio). If ≥0.85, treat as duplicate.
4. For duplicates: prompt user to confirm posting a comment on the existing issue.
5. For new: prompt user to preview the issue body and confirm.
6. Authentication: prefer `gh` CLI if installed; fall back to `INTENT_GITHUB_TOKEN` env var; otherwise instruct user.

Always `--dry` by default unless `--yes` is passed. Prints a list of would-create / would-comment URLs.

## 11. Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (or, in `--bool` mode, predicate true) |
| 1 | Generic failure / predicate false |
| 2 | User cancelled |
| 3 | Model unavailable or backend error |
| 4 | Static guard hard-rejected the proposal |
| 5 | Schema validation failed and retries exhausted |
| 6 | Sandbox required but unavailable |
| 7 | Confirmation required but no TTY and `--yes` not set |
| Other | Passthrough of executed command's exit code (≥10 reserved for intent itself) |

When `intent` would return >9 and the executed command's exit code is in that range, `intent` adds `INTENT_EXIT_OVERRIDE=<code>` to its own stderr summary; programmatic callers should prefer `--json` to get the unambiguous `exit_code` field.

---

*Spec version: v1-draft.0. Bumped on any change to public surface.*
