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

> **Status: pre-alpha.** This repository currently contains the project's intent contract and scaffolding. The binary does not yet exist. See [`INTENT.md`](./INTENT.md) for the full project charter and [open issues](https://github.com/CoreyRDean/intent/issues) for the roadmap.

---

## Read this first

[**`INTENT.md`** — what this project is, what it is not, and why it should exist.](./INTENT.md)

That document is the project's constitution. Every feature, dependency, and design decision is checked against it. If you are considering contributing, please read it before opening a substantial PR.

## Quick links

- [Intent contract](./INTENT.md)
- [Contributing](./CONTRIBUTING.md)
- [Code of conduct](./CODE_OF_CONDUCT.md)
- [Security policy](./SECURITY.md)
- [Issues](https://github.com/CoreyRDean/intent/issues)
- [Discussions](https://github.com/CoreyRDean/intent/discussions)

## License

Apache License 2.0 — see [`LICENSE`](./LICENSE).
