# Security

## Reporting a vulnerability

Report it privately through [GitHub's advisory form](https://github.com/ericliswan/termeownal/security/advisories/new), not in a public issue. Don't include real cookies, session files or Instagram data; a description and steps against `meow --backend=fake` are enough.

Expect a reply within a week. Fixes ship in a patch release, and the advisory is published once the release is out.

## Supported versions

Only the latest release gets fixes.

## Scope

In scope: anything in this repository that could expose the saved session, cookies or message text — the session file and its key (`internal/secrets`), logs, the cache, the login flow and its error messages.

Out of scope:
- Bugs in [mautrix/meta](https://github.com/mautrix/meta): report them there.
- Programs running as the same user reading the keyring or session file: termeownal doesn't defend against them (see AGENTS.md §7.2).
- The cache holding message text in plaintext: documented, not a bug.
- Instagram flagging or locking an account: termeownal is an unofficial client (see the README).
