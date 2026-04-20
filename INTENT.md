# INTENT

> The intent contract for the `intent` (`i`) project.
> This document describes **what this project is, what it is not, and why it should exist.**
> It is intentionally free of implementation detail.
> When in doubt about any decision — design, dependency, feature, or UX — return here first.

---

## Why this should exist

The shell is the most powerful interface a computer offers, and the least forgiving.

To use it well you have to remember exact incantations of programs you may invoke once a year, written by different people across forty years, with inconsistent flags, opaque error messages, and footguns in every corner. Every developer carries a private folder of half-remembered one-liners and a browser tab of Stack Overflow answers they hope still apply. Everyone else just opens a GUI and lives with the limits.

Large language models are now small enough, fast enough, and good enough at code that this asymmetry no longer needs to exist. A model that fits on a laptop can read your intent in plain English and produce the exact command you would have written if you remembered. The technology to close the gap between *what a person wants* and *what the shell needs to hear* is sitting on disk, waiting for someone to wire it up correctly.

`intent` exists to wire it up correctly.

The promise is small and sharp:

> **You say what you want. The terminal does it.**
> Locally. Composably. Safely. Without ceremony.

Not as a chatbot bolted onto a terminal emulator. Not as a cloud service that ships your shell history to a vendor. Not as a wrapper that only works on its own prompt. As a first-class Unix citizen — a binary called `intent`, aliased `i` — that behaves the way `grep` and `curl` behave: predictable, pipeable, scriptable, and yours.

---

## What this project IS

### A natural-language command interpreter for the terminal

The primary, default, and most-used mode of `intent` is:

```
i <whatever you want, in plain language>
```

Anything that follows `i` or `intent` that isn't a recognized subcommand or flag is treated as natural language describing what the user wants the computer to do. A local model interprets the request, produces the shell command or script that satisfies it, and (with the user's consent) runs it. The result is delivered as ordinary terminal output — stdout, stderr, exit code — indistinguishable in form from any other Unix tool.

### Local-first by default

The reference experience does not require an internet connection after first run, does not transmit prompts off the machine, and does not depend on a vendor account. A local model and runtime are downloaded on first use and cached. Cloud backends are supported as a deliberate, opt-in choice — never as the path of least resistance.

This is not a privacy feature in the cosmetic sense. It is the foundation that makes every other promise in this document possible: the audit log can be honest, the cache can be deterministic, the latency can be flat, the cost can be zero, and the user owns their own commands.

### A first-class Unix citizen

`intent` participates in pipelines. It reads stdin. It writes stdout. It returns meaningful exit codes. It composes with itself and with every other tool in the shell. Constructions like:

```
i ping google's dns | i if reachable exit 0 else exit 1
some-command | i extract just the email column > emails.txt
i list large files in this repo | xargs -I{} ls -lh {}
```

are not edge cases or curiosities. They are the language the project is designed in. If a feature breaks composability, the feature is wrong.

### Safe by construction

A natural-language interface to the shell is an enormous footgun if naive. `intent` treats every generated command as untrusted code, regardless of who or what produced it. Risk is classified before any prompt reaches the user. Destructive operations require explicit, friction-bearing confirmation regardless of any "auto-run" setting. Static guards run independently of the model. An audit log records every prompt, every generated command, and every outcome.

The user must be able to trust the binary in the same way they trust `rm` — knowing exactly what it will and will not do, and that it will not surprise them.

### Magical, not flashy

The goal is for `intent` to feel like the terminal finally understanding you, not like a chatbot has been installed on your machine. That means: short, clear status messages; instant responses for things it has done before; sensible defaults that virtually no one needs to override; and the absence — wherever possible — of configuration, jargon, decision fatigue, and waiting.

If a user has to read documentation to perform their first successful command, the project has failed at its primary job.

### A platform for accumulating personal automation

Every confirmed intent is, in effect, a small piece of automation the user has now expressed in plain language. `intent` remembers these. Repeated requests become instant and deterministic without re-invoking the model. Useful patterns can be promoted to named, reusable skills. Over time, `intent` should become *more useful the longer you use it* — a personal layer of automation that grows out of ordinary work, with no separate "scripting" step.

### Open

The project is open source under a permissive license. Anyone can read the code, file an issue, propose a change, or fork it. The roadmap is public. The decisions are documented. The model interface is pluggable, so users are not locked to any single vendor — including the project's own defaults.

---

## What this project is NOT

These are not features that have been deferred. These are commitments to *not build them*. They define the shape of the project as much as the things it will do.

### Not a chatbot

`intent` is not a conversational assistant. It does not have a personality. It does not greet you. It does not offer to help. It does not say "Sure! I'd be happy to..." The interaction model is **request → action → result**, the same as every other shell command. Conversational follow-up exists where it is genuinely useful (e.g., "and now sort by date"), but the project will never optimize for talking to the user. It optimizes for getting out of their way.

### Not a cloud service

There is no `intent` server. There is no `intent` account. There is no telemetry endpoint enabled by default. The project will never require a network connection to perform its core function. It will never gate features behind a subscription. It will never make the local-only path a worse experience than the cloud-only path. Cloud model backends exist as one option among several, configured by the user, never as the default.

### Not a terminal emulator, IDE, or shell

`intent` does not replace your terminal, your shell, your editor, or your prompt. It is a single binary that runs inside whatever you already use. It does not require a GUI, a TUI takeover, an alternate screen buffer, or any change to how the user already works. If the user uninstalls it, their environment returns to exactly what it was before.

### Not an autonomous agent

`intent` does not run loops on its own. It does not pursue goals across many actions without checking in. It does not "decide" to do things the user did not ask for. Read-only context-gathering steps (listing a directory, reading a file the user mentioned, checking which tools are installed) are permitted as part of producing a single response, but `intent` does not take destructive or external actions without an explicit user confirmation for that specific action. The user is always the agent. `intent` is a translator.

### Not a code generation tool for projects

`intent` is not Copilot, not Cursor, not an IDE assistant. It does not write your application code. It does not edit your repository's source files as a feature. It produces *ephemeral* commands and scripts that exist to satisfy a single intent in the moment. If the user wants to author durable code, they should use a tool designed for that.

### Not opinionated about what you do with your computer

`intent` does not refuse benign requests because they look unusual. It does not lecture. It does not editorialize. It does not add commentary the user did not ask for. The safety layer exists to prevent the user from being harmed by malformed or malicious *commands*, not to police what the user is allowed to want.

### Not a marketplace, social network, or community platform

`intent` may eventually allow users to share recipes or skills with each other. It will never become a feed, a leaderboard, a profile system, or a content platform. Sharing is a file format, not a service.

### Not a Trojan horse for any other product

`intent` does not exist to drive adoption of a model, a vendor, a framework, or a follow-on commercial offering. If a user installs `intent`, uses it for years, never upgrades, never visits a website, and never pays anyone a dollar — that is a successful outcome, not a leak.

---

## Principles

These are the tiebreakers for any decision that this document does not directly answer.

1. **Magic over configuration.** If a setting exists, the default must be right for almost everyone. If it isn't, fix the default; don't add a flag.
2. **Local before cloud.** Any feature that exists in a cloud variant must work, or have a graceful local equivalent, with no internet connection.
3. **Composition over completeness.** A small tool that pipes well beats a large tool that does it all.
4. **Predictable over clever.** Surprise is the enemy. The same input should produce the same kind of output, every time, on every machine.
5. **Safety is structural, not behavioral.** Guards are deterministic code, not model instructions. The model is never the last line of defense.
6. **Latency is a feature.** A response that arrives in 80ms changes how a tool feels. The architecture exists in service of this.
7. **Honest about uncertainty.** When the model is unsure, the UI says so. When a command is risky, the UI says so. When something is cached, the UI says so. The user is never lied to about what just happened.
8. **Pipes are sacred.** When stdout is not a terminal, all decoration disappears. Spinners, colors, prompts, banners — gone. What remains is data.
9. **The user owns their data.** Prompts, history, cached skills, audit logs — all live on the user's machine, in plain formats, fully exportable, fully deletable.
10. **Uninstall must be trivial.** A user who decides this was a mistake should be able to remove `intent` and every trace of it with one command, in seconds.

---

## The user experience promise

A first-time user, on any reasonably modern laptop, should be able to:

1. Install `intent` with a single command they can read and understand.
2. Run their first natural-language request immediately afterward.
3. Receive a useful result without configuring anything.
4. Understand, before any command runs, what is about to happen and why.
5. Cancel, preview, edit, or confirm — and have the tool remember their preference.
6. Pipe the output into another command without thinking about it.
7. Uninstall the tool just as easily as they installed it.

A returning user should find that `intent` has gotten faster and more accurate the more they use it, not slower and more cluttered.

A power user should find that every behavior has a flag, every flag has a sensible default, every default is documented, and nothing important is hidden.

A skeptical user should be able to read the audit log, inspect the cached skills, point `intent` at a model they trust, and verify that nothing is happening behind their back.

---

## The composability promise

`intent` is designed so that the following sentences are true:

- If `intent` is in a pipeline, it behaves like a Unix tool, not like an interactive program.
- Any output `intent` produces can be consumed by another `intent` invocation without loss of meaning.
- The exit code of `intent` reflects the exit code of the work it performed, unless the user asks otherwise.
- `intent` can be embedded in shell scripts, Makefiles, cron jobs, CI pipelines, and editor integrations without any special mode.
- Structured output (machine-readable form) is always available on request, in a format that is documented and stable.

If any of these stops being true, that is a bug, not a tradeoff.

---

## The safety promise

`intent` makes the following commitments to the user about what it will and will not do with the privilege of running shell commands on their behalf:

- It will never run a command the user has not been shown or has not consented to a class of, except in modes the user has explicitly enabled and that the project has classified as low-risk.
- It will never silently elevate privileges. If a command requires `sudo`, the user is told before any prompt appears.
- It will never run a command flagged as destructive without an explicit, friction-bearing confirmation for that specific command, regardless of any "auto-run" setting.
- It will keep an append-only audit log of every prompt, every generated command, and every outcome, on the user's machine, that the user can read or delete at any time.
- It will redact secrets from anything it logs, displays, caches, or shares.
- It will never transmit a prompt, a command, a piece of context, or a piece of output off the user's machine without an explicit configuration choice that the user made.
- It will fail closed. When uncertain, when misconfigured, when the model is unavailable, when a guard cannot run — `intent` does nothing, and says so.

---

## The privacy promise

- Default mode is fully local. No prompts, history, telemetry, or context leaves the machine.
- All persistent state lives in a single, documented directory under the user's home, in plain, inspectable files.
- Cloud backends, if used, are configured explicitly and named visibly in every interaction that uses them.
- There is no analytics endpoint. There is no usage beacon. There is no first-party telemetry, opt-in or otherwise, in the v1 product. If telemetry is ever added, it will be opt-in, local-summarized, and disclosed in this document first.

---

## How this document is used

This is the constitution. Code, features, dependencies, and roadmap items are checked against it.

- **A proposed feature must be expressible as serving an item in *What this project IS*.** If it can't, it doesn't belong here.
- **A proposed feature must not violate any item in *What this project is NOT*.** Not even a little. Not even temporarily. Not even "we'll fix it later."
- **A tradeoff between principles is resolved by the principle higher in the list.** Magic beats configuration. Local beats cloud. Composition beats completeness.
- **Changes to this document are larger than changes to any code.** They require explicit discussion, a recorded rationale, and a version bump.

When the project is small, this document keeps it focused.
When the project is large, this document keeps it honest.

---

*v0 — initial intent contract.*
