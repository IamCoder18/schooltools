# Support

This document lists where to get help with **schooltools**.

---

## Read the docs first

Most questions are answered by these files in this repository:

- [README.md](./README.md) — quickstart, command reference, flags,
  session file format, archive / prune workflow, migration notes.
- [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) — known UX rough edges with
  reproduction steps and the source file most likely responsible.
- [D2L_API_RESEARCH.md](./D2L_API_RESEARCH.md) — long-form notes on the
  CBE / D2L Valence surface area, including endpoints that are
  disabled on the CBE tenant.
- [SECURITY.md](./SECURITY.md) — how to report a vulnerability.

`schooltools --help` and `schooltools <command> --help` are the
authoritative reference for flags and subcommands.

---

## Asking a question

1. **Search the issue tracker.** Most non-bug questions have been
   answered already.
2. If you don't find an answer, **open a GitHub issue** using the
   "Question" template. Please include:
   - `schooltools --version` output.
   - The exact command and its output.
   - What you were trying to do.

We do not maintain a chat room. Issues are the support channel.

---

## Filing a bug

Use the "Bug report" issue template. Include:

- `schooltools --version` output.
- The exact command you ran.
- Observed vs. expected behaviour.
- A redacted `~/.config/schooltools/auth.log` line if it's a session
  or auth issue (the log is JSONL; the most recent line is usually
  enough).
- Anything you have already tried.

---

## Reporting a security issue

**Do not** file a public issue. See
[SECURITY.md](./SECURITY.md#reporting-a-vulnerability).

---

## When not to open an issue

- "I lost my password" — that's a CBE account issue, not a
  schooltools issue. Reset it via the CBE / D2L login page.
- "D2L is down" — check the CBE status page first; if D2L is down,
  the CLI is down too.
- "Can you add feature X?" — open a "Feature request" issue with the
  user story. We may close it as out-of-scope; we are a small project
  and cannot build everything.

---

## Maintainers

- **Aarav Sharma** — [@IamCoder18](https://github.com/IamCoder18)
- Email: [iamcoder18@gmail.com](mailto:iamcoder18@gmail.com)
  *(please use GitHub issues unless the topic is security-sensitive)*
