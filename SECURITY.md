# Security policy

`intent` runs shell commands on the user's machine. Security is not a feature of this project; it is the project. We take vulnerability reports seriously and we want it to be easy to send them.

## Scope

In scope:

- Vulnerabilities in the `intent` binary, daemon, installer, updater, or any first-party packaging (Homebrew tap, install script, release artifacts).
- Vulnerabilities in the safety classifier, static guard, sandbox modes, or audit log.
- Supply-chain issues affecting how `intent` is built, signed, or distributed.
- Privacy regressions: any path by which prompts, history, context, or output could leave the user's machine without explicit configuration.

Out of scope (please don't file as security):

- The model producing a wrong, unhelpful, or low-quality command. Open a normal issue.
- The user explicitly approving a destructive command and being surprised by the result. The safety layer is designed to surface, not prevent, intentional choices.
- Vulnerabilities in third-party model backends accessed via configured network endpoints. Report those upstream.

## Reporting a vulnerability

**Do not open a public GitHub issue for a security report.**

Use GitHub's private vulnerability reporting:

> Repository → Security → Report a vulnerability

This delivers your report privately to the maintainers and creates a private advisory we can collaborate on.

If for any reason you cannot use that channel, contact the maintainer directly via the email listed on the maintainer's GitHub profile and include `[intent security]` in the subject line.

## What to include

- A clear description of the issue and its impact.
- Steps to reproduce, including platform, shell, `intent` version, and (if applicable) model backend.
- Any proof-of-concept code or commands. Please redact secrets.
- Whether you are willing to be credited in the advisory.

## What to expect

- Acknowledgment within **72 hours**.
- An initial assessment and severity within **7 days**.
- Status updates at least every **14 days** until resolution.
- Coordinated disclosure: a fix and a public advisory, with credit to the reporter unless you ask otherwise.

We will not pursue legal action against researchers who:

- Make a good-faith effort to avoid privacy violations, data destruction, and service disruption.
- Report the issue privately and give us reasonable time to respond before any public disclosure.
- Do not exfiltrate data beyond the minimum necessary to demonstrate the issue.

## Safe harbor

Research conducted under the guidelines above is considered authorized. We will not initiate or support legal action against you. If a third party brings legal action, we will make it known publicly that your research was authorized.

## Hardening commitments

These are commitments the project makes about its own security posture. They are tracked as work, not aspirations:

- All releases are built reproducibly from a tagged commit.
- All release artifacts are signed; signatures and checksums are published alongside binaries.
- The install script and updater verify signatures before installing or replacing a binary.
- The daemon, when installed, runs with the minimum privileges required and never as root.
- The audit log is append-only and protected against in-place modification by other processes where the OS supports it.
- Secrets detection runs on anything written to logs, caches, or shared exports.

If any of these is not yet true, that is a known gap, tracked in a public issue, not a quiet exception.
