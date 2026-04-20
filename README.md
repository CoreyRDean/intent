# intent

> You say what you want. The terminal does it.

`intent` (alias `i`) is a natural-language command interpreter for the terminal. You describe what you want in plain English; a local model translates it into the shell command or script that satisfies it; you confirm; it runs. Output behaves like any other Unix tool — it pipes, it scripts, it returns sensible exit codes.

```
$ i check if google's dns server is online
  Understanding... done
  → ping -c 1 -W 1 8.8.8.8
  This will send one ICMP echo to 8.8.8.8 and report whether it responded.
  [Enter] run · [p] preview · [e] edit · [n] cancel
```

It is **local-first** by default (no network required after first run, no prompts leave your machine), **safe by construction** (risk-classified, deterministic guards, audit log), and **composable** (`i ping google's dns | i if reachable exit 0 else exit 1`).

> **Status: pre-alpha.** The binary builds and the mock backend round-trips the full prompt → propose → confirm → run loop, but the local model runtime, daemon, and self-update flows are still being wired up. See [`INTENT.md`](./INTENT.md) for the full project charter, [`docs/SPEC.md`](./docs/SPEC.md) for the implementation contract, and [open issues](https://github.com/CoreyRDean/intent/issues) for the roadmap.

## Building from source

```
git clone https://github.com/CoreyRDean/intent
cd intent
make build           # produces ./bin/intent and ./bin/i
INTENT_FORCE_BACKEND=mock ./bin/i hello   # smoke test without a model
```

Requires Go 1.26 or newer.

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
