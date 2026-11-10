## Summary
<!-- What changed and why. -->

Closes #

## How was this tested?
<!-- Commands run, tests added. `make check` passes. -->

## Manual checks against Instagram
<!-- Steps for the human with a test account, or "none". -->

## Security checklist (AGENTS.md §8)
- [ ] No secrets or message text in logs, errors shown to the user, panics or test output
- [ ] Secrets only via `internal/secrets`; nothing secret in SQLite, config, flags or env
- [ ] Downloaded media goes to a `0700` temp dir, deleted on exit, never auto-opened
- [ ] Fixtures are scrubbed: no real IDs, usernames, names or message text
- [ ] Any new outbound network call lives in `backend/instagram`
- [ ] Docs updated: AGENTS.md, ROADMAP.md checkboxes, `docs/ids.md` if ID behaviour changed
