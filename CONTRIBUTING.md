# Contributing to intent

Thanks for your interest. `intent` is intended to be a friendly project to contribute to. This document covers the few things worth knowing before you start.

## Read `INTENT.md` first

[`INTENT.md`](./INTENT.md) is the project's constitution. It describes what `intent` is, what it is not, and the principles that resolve any tradeoff. Almost every "should we add this?" question is answered there.

If a proposed change cannot be expressed as serving an item in *What this project IS*, or violates an item in *What this project is NOT*, it will not be accepted regardless of how well it is implemented. Please skim that document before opening a substantial PR — it will save both of us time.

## What kinds of contributions are welcome

All of them, including:

- Bug reports.
- Feature requests (please describe the *intent* — what you want to be able to do — before proposing a design).
- Documentation improvements, typo fixes, clarifications.
- Code: bug fixes, new features that have been discussed in an issue first, refactoring, tests.
- Triage: reproducing bugs, labeling, asking clarifying questions, closing duplicates.
- Reviews of other people's PRs.
- Examples and recipes.
- Packaging for additional platforms (Homebrew, scoop, AUR, nixpkgs, etc.).

You do not need permission to file an issue or open a PR. You don't need to be experienced. You don't need to know the codebase. First-time contributions are welcome and will be reviewed with care.

## Before you start coding on a substantial feature

For anything larger than a bug fix or small improvement, please open an issue first to discuss the approach. This is so you don't spend time on something that turns out to conflict with the intent contract or duplicate work in progress.

Small PRs without prior discussion are fine and encouraged.

## Workflow

1. Fork the repo and create a branch from `main`.
2. Make your change. Keep the diff focused.
3. If you added behavior, add or update tests.
4. Run the project's checks locally (see `README.md` once a build exists).
5. Write a clear commit message. Describe *why*, not just *what*.
6. Open a PR against `main`. The PR description should:
   - Explain what the change does and why.
   - Link any related issues.
   - Note any user-visible changes.
   - Confirm that the change is consistent with `INTENT.md`.

## Code review

- Reviews focus on correctness, alignment with `INTENT.md`, simplicity, and user impact.
- Disagreement is fine. If you think feedback is wrong, say so and explain why. The goal is the best outcome for the project, not consensus theater.
- Maintainers may ask for changes, suggest a different direction, or close a PR if it conflicts with the intent contract. None of these are personal.

## Reporting bugs

Please include:

- What you did (the exact `i` invocation, if applicable).
- What you expected to happen.
- What actually happened.
- Your OS and shell.
- The version of `intent` (`i --version` once it exists).
- Relevant entries from your `intent` audit log, if you're comfortable sharing them — please redact anything sensitive first.

## Reporting security issues

Do **not** open a public issue. See [`SECURITY.md`](./SECURITY.md) for the disclosure process.

## Communication

- Bugs, concrete feature requests, and code → GitHub Issues / PRs.
- Open-ended ideas, design discussions, questions → GitHub Discussions.
- Behavior concerns → see the [Code of Conduct](./CODE_OF_CONDUCT.md).

## Licensing of contributions

By submitting a contribution to this project, you agree that your contribution is licensed under the [Apache License 2.0](./LICENSE), the same license as the rest of the project. No CLA is required.

## Thank you

Open source works because people show up. Showing up at any level — filing a bug, fixing a typo, reviewing a PR, helping another user in Discussions — makes the project better. Thanks for being here.
