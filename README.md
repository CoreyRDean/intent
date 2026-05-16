# intent

> You say what you want. The terminal does it.

`intent` (alias `i`) is a natural-language command interpreter for the terminal. You describe what you want in plain English; a local model translates it into the shell command or script that satisfies it; you confirm; it runs. Output behaves like any other Unix tool — it pipes, it scripts, it returns sensible exit codes.

```
$ i "check if google's dns server is online"
  Understanding... done
  → ping -c 1 -W 1 8.8.8.8
  This will send one ICMP echo to 8.8.8.8 and report whether it responded.
  [Enter] run · [p] preview · [e] edit · [n] cancel
```

It is **local-first** by default (no network required after first run, no prompts leave your machine), **safe by construction** (risk-classified, deterministic guards, audit log), and **composable** (`i "ping google's dns" | i "if reachable exit 0 else exit 1"`).

That composability applies to subcommands that consume natural language too: `i report "first problem" < extra-notes.txt` appends the piped text after the command-line text before proposing issues.

> **Status: pre-alpha.** The binary builds and the mock backend round-trips the full prompt → propose → confirm → run loop, but the local model runtime, daemon, and self-update flows are still being wired up. See [`INTENT.md`](./INTENT.md) for the full project charter, [`docs/SPEC.md`](./docs/SPEC.md) for the implementation contract, and [open issues](https://github.com/CoreyRDean/intent/issues) for the roadmap.

## Building from source

```
git clone https://github.com/CoreyRDean/intent
cd intent
make build           # produces ./bin/intent and ./bin/i
INTENT_FORCE_BACKEND=mock ./bin/i hello   # smoke test without a model
```

Requires Go 1.26 or newer.

## Running the tests

```bash
# Unit tests only (fast, no binary build required)
go test ./internal/safety/... ./internal/update/...

# Full suite including CLI integration smoke tests (builds the binary)
go test ./...

# Run only the integration smoke tier
go test ./internal/cli/ -v -run Test
```

The integration smoke tests in `internal/cli/smoke_test.go` build the binary once in `TestMain`, then run a hermetic suite that covers:
- CLI dispatch: `--version` / `-V` / `version` subcommand, `--help`, no-args help
- Mock backend round-trip: `hello` (inform response) and `--dry --json list files` (JSON output)
- Safety guard integration: a dangerous command is hard-rejected with exit 4 through the full dispatch
- Config round-trip: `config set` / `config get` using an isolated temp directory

No network, real model, or daemon is required. Set `INTENT_STATE_DIR` and `INTENT_CACHE_DIR` to override state paths; `INTENT_FORCE_BACKEND=mock` to force the mock backend.

---

## Shell integration

Add the shell integration so that glob characters (`?`, `*`, `[`, `]`) in your prompts are not expanded by the shell before they reach intent:

```sh
# zsh — add to ~/.zshrc
eval "$(intent shell-init zsh)"

# bash — add to ~/.bashrc
eval "$(intent shell-init bash)"

# fish — add to ~/.config/fish/config.fish
intent shell-init fish | source
```

### Known limitation: apostrophes and single quotes (zsh)

> **tl;dr** — on zsh, wrap prompts that contain apostrophes in double quotes:
> `i "what's the status of nginx"` works; `i what's the status of nginx` does not.

The `noglob` alias that the zsh integration installs fires *after* zsh has already tokenised the command line. An apostrophe or single quote in an unquoted prompt (`don't`, `it's`, `Google's`) creates an unmatched quote context at tokenisation time, causing zsh to emit a parse error before intent is ever invoked:

```
% i what's the status of nginx
zsh: unmatched '
```

**This is a fundamental zsh constraint** — it applies to any alias or shell function. By the time any user-defined code can run, the line has already been tokenised; there is no hook that fires before tokenisation on an interactive command line.

**Workaround:** wrap the whole prompt in double quotes.

```sh
i "what's the status of nginx"        # ✓ works
i "check if today's date is right"    # ✓ works
i "is google's dns up"                # ✓ works

i what's the status of nginx          # ✗ zsh: unmatched '
```

Prompts without apostrophes are unaffected and need no quoting.

---

## Invocation context flags

Use `--context key=value` to inject ephemeral, per-call hints into the model prompt without changing global config.

```sh
i --context repo=core --context task=triage "summarize recent risky changes"
i --context env=staging --dry "check whether the api is reachable"
```

When chaining intent into intent, `INTENT_PIPE_FROM=intent` is used to auto-enable inter-intent behavior (`--json` and `--from-intent` semantics). You can still pass `--from-intent` explicitly when testing or scripting that flow:

```sh
INTENT_PIPE_FROM=intent i --from-intent --json "if upstream output indicates failure, exit 1 else 0"
```

The inter-intent JSON envelope also carries the upstream prompt and cwd alongside the response metadata and captured stdout, so downstream invocations can keep path context instead of treating bare filenames as if they came from the current directory.

Use `--literal` when your prompt starts with a word that is also a subcommand or when later words should stay prompt text instead of being parsed as intent-mode flags.

```sh
i --dry --json --literal version
i --literal explain --raw grep output
```

With `--literal`, everything after the flag is treated as natural-language prompt text.

---

## Managing models

intent ships with a curated catalog of small-to-medium GGUF models that run locally via [llamafile](https://github.com/mozilla-ai/llamafile). You can also point it at any public Hugging Face GGUF repo.

```sh
# See what's on offer and which one is current.
i model list

# Switch to a catalog model (downloads on first use).
i model use qwen2.5-coder-3b

# Switch to an arbitrary Hugging Face repo. We probe the Hub to pick
# a sensible quant (Q4_K_M by default), show the size, and download.
i model use bartowski/Phi-3.5-mini-instruct-GGUF
i model use bartowski/Phi-3.5-mini-instruct-GGUF:Q6_K   # explicit quant

# Pre-fetch without switching.
i model pull llama-3.1-8b

# Show details + probe HF for compatibility before committing.
i model show bartowski/gemma-2-2b-it-GGUF
```

Custom models are persisted at `custom-models.json` alongside `config.toml`. `i model rm <id>` forgets a custom entry; add `--purge` to also delete the GGUF file from the cache.

---

## Read this first

[**`INTENT.md`** — what this project is, what it is not, and why it should exist.](./INTENT.md)

That document is the project's constitution. Every feature, dependency, and design decision is checked against it. If you are considering contributing, please read it before opening a substantial PR.

## Quick links

- [Intent contract](./INTENT.md)
- [Implementation spec](./docs/SPEC.md)
- [Architectural decisions](./docs/DECISIONS.md)
- [Releasing](./docs/RELEASING.md)
- [Contributing](./CONTRIBUTING.md)
- [Code of conduct](./CODE_OF_CONDUCT.md)
- [Security policy](./SECURITY.md)
- [Issues](https://github.com/CoreyRDean/intent/issues)

## License

Apache License 2.0 — see [`LICENSE`](./LICENSE).
