# Decision log

A short, append-only record of decisions that would otherwise have been lost in chat. Each entry is a paragraph or two — what we picked, what we rejected, and why. New decisions go at the top.

---

## D-006 — Native tool-calling vs. schema-encoded tool-calling

**2026-04-19.** We need multi-step read-only context gathering (e.g., "the model needs to know if `rg` exists before generating a command"). llama.cpp's OpenAI-compatible server supports native `tools` / `tool_calls`, but the field is gated on the build, the model, and the chat template, and several deployment targets we want to support (older Ollama, llamafile pre-0.10) don't reliably surface it.

**Decision:** Encode tool calls inside our own JSON response schema (`approach: "tool_call"`). This works on every backend that can constrain output to JSON, including the dumbest possible one. It also makes the protocol legible in the audit log without any backend-specific parsing. We lose nothing; native `tool_calls` would have been wrapped in our schema anyway.

---

## D-005 — Update channel naming format

**2026-04-19.** Owner asked for nightly tags shaped like `v1.2.3-123456789`.

**Decision:** Nightly pre-release tag format is `v{next-semver}-{unix-timestamp}`. `next-semver` is the patch-bumped version of the latest stable tag, or `0.0.1` if no stable tag exists. The unix timestamp is the seconds-since-epoch of the build start. This is a valid SemVer pre-release string (the part after `-` sorts lexically; numeric pre-release identifiers sort numerically, so timestamps order correctly). Update channel selects `stable` (no `-` in tag) or `nightly` (any tag with the timestamp suffix).

---

## D-004 — Daemon is opt-in, default Yes

**2026-04-19.** Cold-loading a 4.7 GB model on every invocation is unacceptable. A persistent daemon fixes it at the cost of a background process the user didn't ask for.

**Decision:** On first run, prompt: `Keep intent warm in the background so it never has to load? [Y/n]`. Default Y. With `--yes`, install. Records the choice in config; never re-asks. The daemon idles cheaply (model unloads from RAM after 30 min, process stays up). Removing the daemon is one subcommand: `i daemon uninstall`. Removing everything is `i --uninstall`.

---

## D-003 — State directory layout

**2026-04-19.** We need a stable, OS-appropriate place for config, audit logs, skills, and the daemon socket — separate from the cache (models, runtime, context).

**Decision:** State at `${XDG_STATE_HOME:-$HOME/.local/state}/intent` on Linux, `~/Library/Application Support/intent` on macOS. Cache at `${XDG_CACHE_HOME:-$HOME/.cache}/intent` on Linux, `~/Library/Caches/intent` on macOS. Windows TBD; defer to a v1.x. The split matters because users (and OS-level cache cleaners) should be able to nuke `~/Library/Caches/intent` without losing audit history or pinned skills.

---

## D-002 — Implementation language: Go

**2026-04-19.** Considered Go and Rust. Both produce single static binaries; both have good CLI/TUI ecosystems; both have a credible Homebrew + cross-compile story.

**Decision:** **Go.** Cold-start matters more than peak throughput for this workload (the bottleneck is the model, not the host process). Go's standard library covers everything we need (HTTP, unix sockets, signals, archive/zip, crypto, JSON). The CLI binary will be invoked thousands of times a day per user; sub-50ms cold start is the floor. Cross-compilation is one env var. Build cache and dependency management are uniform across platforms. We accept that we give up some safety guarantees Rust would have given for free; we make it up by writing safety logic deterministically and testing it.

---

## D-001 — Model contract is JSON, schema-constrained, defined by us

**2026-04-19.** We need every backend to produce structured responses we can trust without prompt-engineering hope.

**Decision:** Define the response schema (`docs/SPEC.md` §2) as the binding contract. Local backends use grammar-constrained sampling (llama.cpp). Cloud backends use `response_format: json_schema`. Backends that can't constrain output validate-then-retry once. Tool calls are encoded as `approach: "tool_call"` inside the same schema, not as native `tool_calls` (see D-006). This means the entire pipeline — model, audit log, cache, daemon protocol — speaks one shape.

---

## D-000 — INTENT.md is the constitution

**2026-04-19.** Initial commit. Anything that conflicts with `INTENT.md` is wrong; fix the spec or the code, not the constitution. Changes to `INTENT.md` require explicit discussion, a recorded rationale here, and a version bump.
