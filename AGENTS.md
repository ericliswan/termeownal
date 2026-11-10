# AGENTS.md — termeownal

Instructions for coding agents working in this repository. Read this whole file before making changes. The human-facing plan, with reasoning behind each decision, is in `ROADMAP.md`; if the two disagree, stop and ask, then update both.

---

## 1. What this project is

**termeownal** is a terminal app for Instagram DMs: log in once, see all your threads, read history, send messages, and receive new ones live. It is a single Go binary with a Bubble Tea TUI.

**Naming:** the repo and Go module are `termeownal`; the binary and command are **`meow`** (built from `cmd/meow`). Use "termeownal" for the project, data directory and keyring service name, and `meow` for anything the user types.

It talks to Instagram through **`instameow`** (`go.mau.fi/mautrix-meta/pkg/instameow`), the Instagram client library inside the [mautrix/meta](https://github.com/mautrix/meta) bridge. termeownal does **not** run the bridge, Matrix, or any server.

**Status:** Phase 0 (spike) done, findings in `docs/ids.md`. Phase 1 (MVP) done. Work proceeds phase by phase (§12). Phase 2 (password login, v0.1.0) done.

---

## 2. Hard rules

These are not style preferences. Breaking one is a bug.

1. **Only `internal/backend/instagram` may import `go.mau.fi/mautrix-meta/...` or `maunium.net/go/mautrix/...`.** Every other package uses termeownal's own `internal/core` types. A test enforces this (§10).
2. **Never handle real credentials.** Don't ask the user for cookies, passwords or session files; don't read, print, copy or commit them. Live Instagram testing is done by the human.
3. **No live Instagram calls in tests or CI.** Tests use `backend/fake` or recorded, scrubbed fixtures.
4. **Never log secrets or message text.** No cookie names with values, tokens, `DumpState()` contents, passwords, or message bodies at any log level above `debug` — and redact them at `debug` too.
5. **Never accept secrets via command-line flags or environment variables.** Read them from the terminal with echo off.
6. **Never commit** `*.enc`, `cookies*.json`, `session*`, `*.db`, recorded raw payloads, or anything under a local spike secrets path. Keep `.gitignore` covering these.
7. **Don't bump the pinned `mautrix-meta` version** unless the task is to upgrade it, and then follow §11.
8. **Don't drop live events.** The event handler blocks on a full queue; it never discards (§6.4).
9. **License is AGPL-3.0** (required because we import an AGPL package). Don't add dependencies with incompatible licenses; ask before adding any new dependency.
10. **Don't commit to `main` directly or force-push.** Work on a branch; commit, push and open PRs without asking. Changes reach `main` only by merging a PR once CI passes.

---

## 3. Commands

Keep this section in sync with the `Makefile`.

| Task | Command |
|---|---|
| All pre-finish checks | `make check` (gofmt, vet, race tests, lint) |
| Build | `go build ./...` |
| Build static binary | `CGO_ENABLED=0 go build -o bin/meow ./cmd/meow` (`make static`) |
| Run against the fake backend (no Instagram) | `go run ./cmd/meow --backend=fake` |
| Run for real (human only) | `go run ./cmd/meow` / `meow login` / `meow logout [--purge]` |
| Test | `go test ./...` |
| Test with race detector | `go test -race ./...` |
| Lint | `golangci-lint run` |
| Format | `gofmt -w . && goimports -w .` |
| Update UI golden files | `go test ./internal/ui/... -update` |
| Record the README demo GIF (needs `vhs`, fake backend) | `make demo` |
| Check the release config | `goreleaser check` (CI runs it) |
| Build every release target into `dist/` without publishing | `make snapshot` (needs `goreleaser`) |
| Print the version (`dev` unless built by GoReleaser) | `meow --version` |

Requirements: **Go 1.26 or newer** (upstream's `go.mod` declares `go 1.26.0`; its `toolchain` line doesn't apply to us). Pure Go — no cgo. SQLite is `modernc.org/sqlite`.

Before finishing any task: `gofmt`, `go vet ./...`, `go test -race ./...` and `golangci-lint run` must pass.

---

## 4. Repository layout

```
termeownal/
├── cmd/meow/
│   ├── main.go                   # CLI: meow, meow login, meow logout, flags
│   ├── login.go                  # login and logout
│   └── console.go                # prompts, read in raw mode
├── internal/
│   ├── app/                      # wiring, lifecycle state machine, event pump
│   ├── core/                     # domain types, Backend, Authenticator, Event
│   ├── backend/
│   │   ├── instagram/            # ONLY importer of upstream (rule 1)
│   │   │   ├── client.go         # construction, resume paths, connect
│   │   │   ├── login.go          # login method choice, cookie login
│   │   │   ├── password.go       # password login step machine
│   │   │   ├── convert.go        # slidetypes → core
│   │   │   ├── events.go         # ClientEvent / Delta → core.Event
│   │   │   ├── ids.go            # ThreadKey ↔ short ID ↔ long ID
│   │   │   └── testdata/         # scrubbed recorded payloads
│   │   └── fake/                 # deterministic fake backend
│   ├── store/                    # SQLite schema, migrations, queries
│   ├── secrets/                  # encrypted session file + keyring key
│   ├── config/                   # TOML config, platform paths
│   └── ui/
│       ├── root.go               # top-level model, focus, layout
│       ├── threadlist/
│       ├── chat/
│       ├── composer/
│       ├── overlay/              # help, new DM, errors, emoji picker
│       └── theme/
├── sessions/                     # local session summaries, git-ignored (§13)
├── docs/
│   ├── ids.md                    # verified ID behaviour (Phase 0 output)
│   └── architecture.md
├── AGENTS.md
├── ROADMAP.md
├── LICENSE                       # AGPL-3.0
├── README.md
├── SECURITY.md                   # private vulnerability reporting
├── go.mod
├── Makefile
├── imports_test.go               # import boundary test (rule 1)
└── .github/
    ├── workflows/ci.yml, release.yml
    ├── ISSUE_TEMPLATE/           # bug and feature forms; blank issues off
    └── pull_request_template.md  # mirrors the §8 checklist
```

Put new code in the package that owns the concern. Don't create `util`/`helpers`/`common` packages.

---

## 5. Architecture

```
ui (Bubble Tea) ◄── tea.Msg ── app (pump) ◄── core.Event ── backend/instagram ── instameow ──► Instagram
       │                         │                                ▲
       └──── tea.Cmd ───────────►├── calls Backend ───────────────┘
                                 ├── store (SQLite cache)
                                 └── secrets (session file)
```

### 5.1 Layers
- **`core`** has no dependencies on other termeownal packages. It defines:
  - Types: `ThreadID` (= upstream `ThreadKey`, int64; short and long IDs never leave the adapter), `UserID` (= FBID), `MessageID`, `User`, `Thread`, `Message` (+ `SendState`), `Session` (= opaque `AuthData` + seq ID). `AuthData` redacts itself in `fmt` output.
  - `Backend` interface: `Restore`, `Snapshot`, `History`, `NewOTID`, `Send`, `MarkRead`, `Typing`, `Connect` (blocks; upstream reconnects on its own), `Events() <-chan Event`, `ExportAuth`, `Close`. The seq ID isn't part of `ExportAuth`: the pump saves it from `SeqAdvanced`. `Send` takes the ID of the message replied to, or `""`. Add methods only when a phase needs them (Phase 3: `SearchUsers`, `StartDM`, reactions, edits, unsends).
  - `Authenticator` interface for multi-step login: `Start`, then `Submit` each `LoginStep`'s fields until a step carries a `LoginResult`.
  - `Event` types (Phase 1): `StatusChanged`, `AuthFailed`, `ResyncNeeded`, `MessageAdded`, `ThreadRead`, `ThreadUpdated`, `SeqAdvanced`, `SessionChanged`. Phase 3 adds `ReadReceipt` (another member's; the viewer's own becomes `ThreadRead`) and `Typing` (another member's; the viewer's own, from another device, is dropped), then `MessageEdited`, `MessageDeleted`, `ReactionAdded`, `ReactionRemoved`, `ThreadRemoved`.
  - Sentinel errors for §6.6: `ErrSessionExpired`, `ErrRateLimited`, `ErrVerificationRequired`, `ErrConsentRequired`, `ErrAccountSuspended`.
- **`backend/*`** implements `core.Backend`. The fake backend must support everything the real one does.
- **`store`** is a disposable cache. Everything in it can be re-fetched.
- **`ui`** only sees `core` types and `tea.Msg`s. It never calls the backend directly; it returns `tea.Cmd`s that `app` provides.

### 5.2 Concurrency
- **One pump goroutine** in `app` reads `Backend.Events()` in order, writes to the store, then calls `program.Send(...)`. It is the only writer for live events.
- The pump shows the cache, then stores and shows `Backend.Snapshot`, and only then asks for `Connect`; it does the same after `ResyncNeeded`. `Snapshot` runs on the pump goroutine, so it must never wait for `Events` to be read.
- The pump also saves the session: the seq ID once the deltas before it are stored, and `ExportAuth` on `SessionChanged` and after `Events` closes. After a failed store write it saves no more seq IDs that run, so the next start replays what the cache missed; login data is still saved, with the last good seq ID.
- All network calls from the UI run as `tea.Cmd`s, never inside `Update`.
- Never mutate UI model state from any goroutine other than Bubble Tea's.
- Every blocking call takes a `context.Context` and respects cancellation.
- Run `go test -race` on anything touching the pump, adapter or store.

### 5.3 Lifecycle state machine (`app`)
`NeedsLogin → Restoring → Syncing → Live ⇄ Reconnecting`; `Live → Resyncing → Live` on resnapshot; any state `→ AuthFailed` on auth errors (show banner, stop retrying, tell the user to run `meow login`).

---

## 6. Working with `instameow`

Names below are from `mautrix/meta` `main` as of 27 Oct 2026. Upstream is an **internal, unstable API**: verify against the pinned version before relying on a detail, and update this section when it changes.

### 6.1 Client
`instameow.NewClient(instameow.ClientParams{Cookies, Log, Settings, EventHandler, SeqID, SeqIDTS, DisableTyping, NativeSession})`. `Cookies` is a `*cookies.Cookies{Platform: types.Instagram}` from `pkg/messagix/cookies`. Leave `DisableTyping` unset: it stops incoming typing too, so `send_typing = false` (§7.7) is applied in `app`, which then never calls `Backend.Typing`. See `igconnector/login.go` → `getInstaClient` for a working example.

Other client methods: `LoadState`, `DumpState`, `SetSeqID`, `HasSeqID`, `GetOwnFBID`, `GetCookies`, `IsAuthenticated`, `SetLogger`, `SetEventHandler`, `GetHTTP`.

### 6.2 Calls

| Need | Call |
|---|---|
| Login check + full snapshot | `LoadIndex(ctx)` → viewer, mailbox |
| Refresh tokens only | `ReloadIndex(ctx)` |
| Threads | `GetMailbox`, `PaginateMailbox` |
| One thread | `GetThread(ctx, slidetypes.MakeGetThreadInfoRequest(shortID))` |
| History | `PaginateMessages(&PaginateMessagesRequest{ThreadID: shortID, OlderThanMessageID, FirstN})` |
| Send text | `SendMessage(&SendTextRequest{IGThreadIGID: &shortID, Text, OfflineThreadingID, ReplyToMessageID})`; first message to a new user: `RecipientIGIDs: []string{userFBID}` and no `IGThreadIGID` |
| Mark read | `MarkRead(req)` then `MarkReadValidation(req)`, `req.Metadata.IGThreadIGID = longID`, `req.Data.MessageID` |
| Typing out | `SetTyping(ctx, shortID, bool)` (no-op when `DisableTyping` is set; fails before `Connect`) |
| Search / new DM | `SearchUsers`, `GetUserForNewDM(ctx, fbid)`, `GetProfile`, `FetchThreadID(ctx, userFBID)` |
| Reactions, edit, unsend | `SendReaction`, `EditMessage`, `UnsendMessage` |
| Message requests | `AcceptMessageRequest` |
| Media | `FetchMedia`; sending = `GetHTTP().SendMercuryUploadRequest` then `SendMedia` |
| ID lookup | `FetchThreadID(ctx, threadKey)` → `ThreadIGIDs{LongID, ShortID}` (verified with 1:1 keys) |
| Socket | `Connect(ctx)`, `Disconnect()`, `ForceReconnect()` |
| Generate OTID | `methods.GenerateEpochID()` (`pkg/messagix/methods`) |

### 6.3 ⚠️ Thread IDs — read this before touching anything thread-related

| `ThreadInfo` field | Type | Call it | Used by |
|---|---|---|---|
| `ThreadKey` | int64 | **key** | termeownal's `core.ThreadID`; `FetchThreadID`. 1:1: the **other user's FBID**. Group: **equal to the short ID**. |
| `ID` | string | **short ID** (JSON `id`; equals `thread_fbid`) | `PaginateMessages`, `GetThread`, `SendMessage`, `SetTyping`, **and every live delta** |
| `ThreadID` | string | **long ID** (JSON `thread_id`; 39 digits, keep as a string) | `MarkRead`, incoming typing notifications |

- The delta field is named `ThreadIGID` but its JSON tag is `thread_fbid` — it holds the **short** ID. If empty, fall back to `Message.ThreadFBID`. Trust JSON tags, not Go field names.
- Deltas with an empty `thread_fbid` are ignored types (e.g. `Create1To1Thread`); don't look them up.
- Typing notifications carry the **long** ID and the sender's **IGID**, so keep long → key and IGID → FBID maps too (from thread user lists, which exclude the viewer). An unknown long ID or IGID is logged at `debug` and dropped: typing is transient, not worth a lookup.
- `ids.go` maintains `key ↔ short ↔ long` in memory from every mailbox, thread and `FetchThreadID` response; it isn't persisted. Unknown short ID in a delta → `GetThread`, store mappings, send the thread as `ThreadUpdated`, then apply the delta. A key with no mapping (e.g. a cached thread opened before the first `Snapshot`) → `FetchThreadID(key)`, and if that fails `GetThread(key)` (a group's key is its short ID): the adapter can't tell a 1:1 key from a group's.
- Users: `core.UserID` = FBID (`SenderFBID`, `GetOwnFBID`); store IGID and username alongside. A thread's `users` list excludes the viewer.
- `docs/ids.md` is the verified source of truth. Read it before touching IDs.

### 6.4 Events and durability
The handler receives `slidetypes.ClientEvent`: `Connected`, `Disconnected`, `ReconnectionStateUpdate`, `SeqIDUpdate`, `ResnapshotRequired`, `AuthError`, `TypingNotification`, and `Delta` (wrapping `NewMessageEvent`, `AdminMessageEvent`, `EditMessageEvent`, `DeleteMessageEvent`, `Create/DeleteReactionEvent`, `ReadReceiptEvent`, `MarkRead/UnreadEvent`, `DeleteThreadEvent`, `UpdateThreadName/Image/FolderEvent`, `ParticipantJoin/LeaveEvent`, `AdminChangeEvent`, `MuteThreadEvent`, `PinThread/MessageEvent`, `UnknownEvent`, `IgnoredEvent`).

How upstream calls it (`instameow/socket.go`): once per delta in a batch, **then** one `SeqIDUpdate` for the batch. Live batches are usually one delta; the catch-up after connect is one large batch. A handler error makes the socket reconnect.

Rules:
- Translate and push onto a buffered channel. If full, **block** (with `ctx`). Never drop.
- `SeqIDUpdate` → `core.SeqAdvanced` on the **same channel**. The pump saves the seq ID only after the preceding deltas are written, so a crash can't save a seq ID ahead of stored data.
- Delivery is **at-least-once**: deltas after the last saved seq ID are replayed on connect, and some (`ReadReceipt`, `MarkRead`) arrive twice anyway. Applying a delta twice must be harmless: upsert by message ID, keep the highest watermark, match echoes by OTID.
- Return an error only for real failures (e.g. cancelled context, or `GetThread` failing for an unknown short ID, so the delta is delivered again). Log unknown events at `debug` and return `nil`.
- Keep the handler free of disk work. Its only network call is `GetThread`, for an unknown short ID or a thread rename.
- `ReconnectionStateUpdate` and `AuthError` arrive on the goroutine of whichever call is running (`LoadIndex` sends the new state), so they never wait on the channel: the adapter keeps them in fields.
- `ResnapshotRequired`, or a refresh falling due (§6.5), → `Connect` returns `nil` after sending `ResyncNeeded`. `AuthError`, or `Disconnected` with an unauthorized error, → `Connect` stops and returns `ErrSessionExpired`, as does every later `Connect`.

### 6.5 Resume logic (mirror `igconnector/client.go` → `connectWithRetry`)
1. Show cached threads from SQLite immediately.
2. Build the client with saved cookies; `SetSeqID` if saved.
3. **Fast:** saved state, `LoadState` succeeds, `HasSeqID()`.
   **Warm:** seq ID but state unusable (`ErrCachedStateTooOld` after 24 h) → `ReloadIndex`, save `DumpState()`.
   **Cold:** no seq ID → `LoadIndex`, save state.
4. Store a fresh mailbox, **then** `Connect`: the pump stores `Backend.Snapshot` (`LoadIndex`'s mailbox on a cold start, otherwise `GetMailbox`) before it asks for a connection, so live events always apply on top of it. Don't refresh the mailbox in the background after `Connected`: it would race the replay. After `Connected`, gap-fill the open thread (the UI reloads its newest page and marks the other threads' stale).
5. `ErrPleaseReloadPage` → delete saved state, cold start.

`Restore` does steps 2 and the `LoadState` half of 3 without touching the network; `Snapshot` makes the calls. A cold start's seq ID waits until `Connect`, since `Snapshot` never sends events.
6. Force a full reconnect every 20 h (config `force_refresh_interval`, 0 disables), never more than once an hour. Like upstream's `FullReconnect(false, true)`, it drops the saved state and keeps the seq ID: `Connect` cancels the client's `Connect`, sends `ResyncNeeded` and returns `nil`, and the next `Snapshot` is a warm start on the same client. A cancelled `Client.Connect` can be called again; `Disconnect` can't. `ForceReconnect()` on detected sleep/wake (wall-clock jump > 2 min past a 30 s tick, read with `Round(0)`: the monotonic clock stops while a Mac sleeps).
7. Reconnect backoff when the library gives up: 1 s doubling to 60 s, with jitter. The pump retries a failed `Snapshot` or `Connect` this way, except on the auth errors of §6.6, and starts the backoff again after a connection that lasted over a minute.

### 6.6 Errors
| Error | Handling |
|---|---|
| `httpclient.ErrRateLimited` | "Instagram is rate-limiting this account…"; back off |
| `ErrChallengeRequired`, `ErrCheckpointRequired` | "Instagram wants to verify this login. Complete the check in the app or website, then `meow login`." |
| `ErrConsentRequired` | "Accept Instagram's pending terms, then retry." |
| `ErrAccountSuspended` | "This account is suspended." |
| `ErrTokenInvalidated`, `types.ErrorResponse` code `1357053`, `AuthError` event | `AuthFailed` → "Session expired. Run `meow login`." |
| `ErrCachedStateTooOld`, `ErrPleaseReloadPage` | silent: drop state, cold start |
| `ErrThreadNotFound` (new DM) | not an error: show empty chat |
| anything else | generic message + log path; wrap with `%w` |

Check with `errors.Is` / `errors.As`, never by string matching.

### 6.7 Upstream code to read before implementing
| Area | File(s) |
|---|---|
| Cookie login | `pkg/igconnector/login.go` (`loginWithCookies`, `submitInstagramCookies`) |
| Password login | `pkg/igconnector/login_native.go`; `pkg/instameow/login_web*.go`, `login_account_manager.go` |
| Connect / resume / refresh | `pkg/igconnector/client.go` |
| Inbox processing | `pkg/igconnector/chatsync.go`, `client.go` (`processMailbox`) |
| Deltas, ID resolution | `pkg/igconnector/handlemeta.go` (`ensurePortal`, `getAndResyncThread`) |
| Sending, IDs, mark read | `pkg/igconnector/handlematrix.go`, `startchat.go` |
| History | `pkg/igconnector/backfill.go` |
| Content types | `pkg/instameow/slidetypes/messagecontent.go`, `pkg/msgconv/` |
| Log redaction | `pkg/instameow/redactlog.go`, `slidetypes.SensitiveString` |

Read upstream to learn; never copy `igconnector` wholesale and never import it.

---

## 7. Feature implementation notes

### 7.1 Login (`meow login`)
- **Cookies (primary):** required `sessionid`, `csrftoken`, `ds_user_id`; optional `rur`, `shbid`, `shbts`, `mid`, `ig_did`.
- Accept: a JSON object, a "Copy as cURL" command (parse its `Cookie:` header), or guided prompts with echo off.
- Instructions shown to the user: use a Chromium-based browser (the library sends a fixed desktop-Chrome user agent), private window, copy cookies from DevTools, **close the window without logging out** (logging out kills `sessionid`).
- Validate with `GetMissingCookieNames`, then `LoadIndex`. Save nothing on failure. On success, call `store.SetMe`, which clears another account's cache (`meow` shows the cache before it syncs), **then** save the session, so a new session never shows the old account's cache. Only the cookies are saved, so the first `meow` after a login is a cold start.
- `cmd/meow/login.go` drives any `core.Authenticator`, so the fake one tests it. Answers are read in raw mode with bracketed paste: the terminal's line editor caps a line (1024 bytes on macOS), shorter than a cURL command. Enter inside a paste or after a `\` is a newline; Ctrl+C or Ctrl+D on an empty answer cancels.
- `Start` asks for the method (a field with `Options`); the cookie path is above, the password path is `password.go`.
- **Password (best effort):** a step machine mirroring the web half of `login_native.go`: credentials (→ cookie consent) → a 2FA code, or the auth platform (method choice, code, approval in the app) → account picker → cookies → `whoAmI`. Upstream's `*bridgev2.LoginStep`s are converted in the adapter: user input → fields (`select` → `Options`; `CanCancel` → an empty answer sends `back`); display-and-wait with a code or nothing → a step with no fields, which `meow login` submits at once and which returns when the wait ends. Captcha, QR, cookie or any other browser-only step → end with "choose cookies". Never store the password; only cookies are saved, so the next `meow` is a cold start. Challenge URLs carry tokens: never log them or put them in errors.
- Password shortcuts: a challenge or checkpoint after the password ends the login with `ErrVerificationRequired` (upstream tries its CAA login next), and "try another way" isn't offered during an approval wait.

### 7.2 Session storage (`secrets`)
- One file `session.enc` (cookies, `DumpState()` blob, seq ID + timestamp) in the data dir, mode `0600`, written atomically (temp + rename; the rename isn't synced, so a power cut can undo the last save but never leaves a mix).
- Format: a 46-byte header (`MEOW`, version 1, key kind, 16-byte salt, 24-byte nonce), then XChaCha20-Poly1305 over a JSON payload. The header is the additional data, so it's authenticated too.
- Key: random 256-bit, base64 in the OS keyring (`zalando/go-keyring`; service `termeownal`, account = the file's path). If the keyring fails → Argon2id (3 passes, 64 MiB, 4 lanes) from a passphrase read with echo off, typed twice when new.
- `Load` at start (`ErrNoSession`, `ErrUnreadable`, `ErrWrongPassphrase`); `Create` after login, with a new key; `Save` reuses the key from `Load` or `Create`; `Delete` for logout.
- It protects copies of the file, such as backups, not against programs running as the same user: they can read the keyring (on macOS the item trusts `/usr/bin/security`).
- Known limits (`shortcut:`s in the package doc): a locked Linux keyring waits for its prompt with no timeout.
- Every command except `--backend=fake` holds `secrets.Lock` (`meow.lock` in the data dir), so a second `meow` fails with `ErrLocked` instead of racing for the file.
- Cookies change in place (`Cookies.UpdateFromResponse`) with no callback. The adapter compares `GetCookies()` and the client state with what it last exported when `Connect` starts, on `Connected` and on each `SeqIDUpdate`, and sends `SessionChanged` if they differ; the pump also saves on exit. Idle means no HTTP, so no cookie changes to miss.
- `meow logout` deletes the file and keyring key; `--purge` also deletes the cache.

### 7.3 Sending
1. Generate OTID (`methods.GenerateEpochID()`); insert local message `send_state=pending`, `otid=OTID`.
2. `SendMessage` with the **short ID** (the long ID also works; short matches upstream and the delta path).
3. Reconcile when a `NewMessageEvent` arrives with matching `OfflineThreadingID`: replace with real ID, `send_state=sent`.
4. Error or no echo 30 s after `Send` succeeds → `failed`; `r` in the messages pane retries the thread's failed messages with the **same OTIDs** (prevents duplicates). The UI sends one message at a time, in order. While running, the UI tracks send states; the cache keeps the local copy (written before `Send`), and the pump marks any still `sending` as `failed` at start.
5. New DM: `FetchThreadID(ctx, userFBID)`; `ErrThreadNotFound` → empty chat with `GetUserForNewDM` info. The first send uses `RecipientIGIDs: []string{userFBID}` and no thread ID. Its echo arrives with a new short ID; call `FetchThreadID(ctx, userFBID)` again to store the IDs (key = `userFBID`).

### 7.4 Rendering
- Phase 1: `TextBody`; otherwise `[ContentType]` (`IGDSnippet` is empty in history). Skip `REACTION_LOG_XMAT` entries; they aren't chat lines. Log every `ContentType` seen at `debug` to build the Phase 3 mapping.
- Phase 3+: `[photo]`, `[video 0:12]`, `[voice 0:08]`, `[gif]`, `[sticker]`, `[reel by @x]`, `[post by @y]`, `[story]`, `[profile @x]`, `↪ forwarded`, quoted reply line, `(edited)`, `message unsent`, `[view-once photo]` (never download), `[unsupported: <type>]`.
- End-to-end encrypted chats are **not supported** by upstream, and Phase 0 couldn't create one to see how they appear. Label threads with an unfamiliar subtype or `input_mode` instead of failing silently.

### 7.5 Storage (`store`)
SQLite via `modernc.org/sqlite`, numbered migrations, `cache.db` in the data dir from `adrg/xdg` (macOS: `~/Library/Application Support/termeownal`).

| Table | Columns |
|---|---|
| `account` | `fbid` PK, `igid`, `username`, `display_name` |
| `users` | `fbid` PK, `igid`, `username`, `full_name`, `avatar_url`, `updated_at` |
| `threads` | `thread_key` PK, `title`, `is_group`, `last_activity_ms`, `unread`, `marked_unread`, `muted`, `pinned`, `folder`, `image_url`, `history_complete` |
| `thread_members` | `thread_key`, `user_fbid`, `nickname`, `is_admin`, `read_up_to_ms` (the member's read watermark; only ever raised) |
| `messages` | `id` PK, `thread_key`, `sender_fbid`, `ts_ms`, `kind`, `content_type`, `text`, `snippet`, `reply_to_id`, `reply_sender_fbid`, `reply_text`, `reply_content_type` (a copy of the quoted message, which may be older than the cache), `edited_at`, `deleted`, `forwarded`, `otid`, `send_state` |
| `reactions` | `message_id`, `user_fbid`, `emoji`, `log_message_id` (removals can arrive with sender 0; `docs/ids.md` §4) |

Indexes: `messages(thread_key, ts_ms, id)`, `messages(otid)`, `threads(last_activity_ms)`. Migration 1 creates only the columns Phase 1 uses; each later phase adds its columns, and the `reactions` table, in a new migration. A thread's cached messages are kept contiguous (a newest page that doesn't reach the cache drops the older cached ones), so the oldest cached message is the paging cursor. Secrets never go in SQLite. Message text in the cache is plaintext; that's documented, not a bug.

### 7.6 TUI
- Layout: thread list (left), chat view (right), composer (bottom right), status bar.
- Components: `bubbles/list` (threads), `bubbles/viewport` + custom wrapping (chat), `bubbles/textarea` (composer — **remap** Enter to send, Alt+Enter newline; the default inserts a newline on Enter).
- Keys: `Tab`/`Shift+Tab` focus · `j`/`k`/arrows move (in the messages pane, select a message) · `Enter` open/send · `g`/`G` top/bottom · `PgUp` at top loads older · `/` filter · `Ctrl+N` new DM · `r` reply (or retry failed) · `e` react · `o` open media · `Esc` cancel · `?` help · `Ctrl+C` quit.
- Emoji shortcodes in the composer, as in Discord: `:heart:` becomes ❤️ when the closing `:` is typed; after `:` and two letters, up to 5 matches show above the text (`↑`/`↓` move, `Tab`/`Enter` pick, `Esc` closes). The colon must start a word (`10:30` isn't one). Names come from `internal/ui/emoji/table.go`, generated from GitHub's gemoji (MIT) with `go generate ./internal/ui/emoji`.
- Mark read only when the thread is visible and the terminal is focused (`tea.WithReportFocus`); no focus reporting → assume focused.
- Unread: bold + `●`. Muted: dimmed. Day separators; group consecutive messages by sender.
- "<name> is typing…" (or "A, B are typing…") at the bottom of the chat view; each start shows it for 6 s, and a stop or the member's message clears it. While the user types, the open thread is told at most every 4 s, and told they stopped on send, on clearing the text, on opening another thread or 5 s after the last edit.
- "Seen" (1:1), "Seen by" + names or "Seen by everyone" (group) under the user's message only when it's the newest in the thread; a reply already shows the other side read on. The UI keeps the highest watermark per member, like the store.
- Respect `NO_COLOR`; handle resize; "terminal too small" below 60×15; width-aware wrapping for emoji/CJK.
- Status bar reflects `StatusChanged`; `AuthFailed` shows a persistent banner.

### 7.7 Config and logs (`config`, `app`)
- Paths come from `adrg/xdg`: `termeownal/config.toml` in the config dir, the data dir for the cache and `session.enc`, `termeownal/meow.log` in the state dir. On macOS all three are under `~/Library/Application Support/termeownal`. `XDG_*` variables override them.
- The config file is optional; a missing file means defaults. Unknown keys and bad values are errors that name the file.
- Keys: `log_level` (`debug`, `info`, `warn` or `error`; default `info`; `trace` is refused because it would log raw payloads), `force_refresh_interval` (default `"20h"`, `0` disables, otherwise at least `1h`) and `send_typing` (default `true`; `false` stops telling others you're typing, while theirs still shows). The once-an-hour full-reconnect floor is a constant, not a setting.
- `app.OpenLog` appends JSON lines to `meow.log` (file `0600`, directory `0700`). No rotation yet.
- Add a key only when a feature needs it, and add it here.

---

## 8. Security checklist (apply to every change)
- [ ] No secrets or message text in logs, errors shown to the user, panics or test output.
- [ ] Secrets only via `internal/secrets`; nothing secret in SQLite, config, flags or env.
- [ ] Downloaded media goes to a `0700` temp dir, deleted on exit, never auto-opened.
- [ ] Fixtures are scrubbed: no real IDs, usernames, names or message text.
- [ ] Any new outbound network call lives in `backend/instagram`.

---

## 9. Code style
- `gofmt` + `goimports`; `golangci-lint` clean.
- `context.Context` is the first parameter of anything that blocks or does I/O.
- Wrap errors with context: `fmt.Errorf("load mailbox: %w", err)`. Sentinel errors for conditions callers branch on.
- No panics outside `main` startup. No `log.Fatal` in library code.
- Logging with `zerolog` to a file in the state dir (never to the terminal while the TUI is running).
- Small interfaces defined where they're consumed. Exported identifiers get doc comments.
- Each source file starts with the AGPL-3.0 license header.
- Prefer clear code over clever code; this is also a portfolio project people will read.

---

## 10. Testing

| What | How |
|---|---|
| Adapter conversion | scrubbed JSON in `backend/instagram/testdata/` → assert `core` values |
| ID mapping | table tests covering every case in `docs/ids.md`, including unknown short ID in a delta |
| Durability | pump test: seq ID never persisted before its preceding deltas; full channel blocks instead of dropping; a replayed delta applied twice changes nothing |
| Store | in-memory SQLite; migrate from empty and from each prior version |
| Send reconciliation | echo on time / late / never / duplicate |
| Resume paths | fast / warm / cold with a fake upstream |
| UI | `teatest` against `backend/fake`, golden snapshots |
| Secrets | encrypt/decrypt round trip; wrong passphrase fails cleanly; atomic write |
| Log safety | run fake login + send; assert no secret values or message text in log output |
| Import boundary | test fails if any package outside `backend/instagram` imports `go.mau.fi/mautrix-meta` or `maunium.net/go/mautrix` |

New behaviour needs a test. Bug fixes start with a failing test.

**Manual smoke test (human, test account, before releases and upstream bumps):** each login input form · restart resumes (all three paths) · send/receive 1:1 and group · reply · reaction, edit and unsend from phone · reading on phone clears unread · Wi-Fi off/on · sleep/wake · logout removes session file and keyring key.

---

## 11. Upgrading `mautrix-meta`
Only when the task asks for it.
1. Read upstream `CHANGELOG.md` and commits touching `pkg/instameow`, `pkg/messagix`, `pkg/igconnector` since the current pin.
2. Bump the pin in `go.mod`; copy any `replace` lines from upstream's `go.mod` (they aren't inherited; today `imroc/req/v3` → `beeper/req/v3`); `go mod tidy`; `go build ./...`.
3. Fix compile errors **only** in `backend/instagram`. If a fix would leak into other packages, stop and ask.
4. Update §6 of this file and `docs/ids.md` for anything that changed.
5. Run all tests; list the manual smoke test for the human in your summary.

---

## 12. Phases
Work in order. Tick boxes in `ROADMAP.md` §14 as items land. Don't start a later phase's features early.

**Phase 0 — Spike (human runs it; agents may write the spike code).** Code in `spike/`; cookies read from a path **outside the repo** given at runtime.
- [x] Go 1.26+, pin `mautrix-meta`, confirm `CGO_ENABLED=0` build, note binary size
- [x] `LoadIndex` → print each thread's `ThreadKey`, `ID`, `ThreadID`, title
- [x] `PaginateMessages` with short ID; print 20 messages + `ContentType`
- [x] `SendMessage` (short and long ID both work; we use short); confirm echo carries the same OTID
- [x] `Connect` and log every event type while messaging from a phone; note IDs in deltas
- [x] `MarkRead` + `MarkReadValidation` clears unread on the phone
- [x] Fast / warm / cold resume all work
- [x] New DM via `FetchThreadID(userFBID)`, including someone with no thread yet
- [x] See how an end-to-end encrypted chat appears (couldn't create one; see `docs/ids.md` §9)
- [x] Write `docs/ids.md`

**Phase 1 — MVP.** Skeleton + CI + license · config + logging · `core` · `backend/fake` · full UI on the fake · `store` + pump + cache-first startup · `secrets` · `meow login` (cookies) / `logout` · `backend/instagram` (resume, snapshot, history, send, mark read, events, IDs) · optimistic send + retry · unread + focus-aware mark read · reconnect, status bar, auth banner.
Done when the human uses termeownal for a day of normal DMs and restart is instant.

**Phase 2 — Password login + first release.** Password login (best effort, §7.1) · `meow --version` · GoReleaser builds for macOS and Linux (amd64, arm64) on tag push · Homebrew tap · v0.1.0 (§14.1).
Done when v0.1.0 installs with Homebrew and logs in with a password or cookies.

**Phase 3 — Messenger feel.** Replies · typing in/out (configurable) · read receipts · reactions · edits/unsends (show + do) · new DM search · message requests · filter, members, mute · content-type mapping.

**Phase 4 — Media.** Placeholders with metadata · `o` to download and open · inline images on kitty / iTerm2 / sixel with fallback · send images.

**Phase 5 — Polish.** Notifications · themes + keybinding config · chat layout with your messages on the right · settings panel.

**Stretch.** Multiple accounts · encrypted cache · `backend/matrix`.

---

## 13. Definition of done (per change)
- Builds; `go vet`, `go test -race ./...`, `golangci-lint run` pass.
- Tests added or updated (§10).
- Security checklist (§8) holds.
- Import boundary holds.
- Docs updated: this file if a rule, command or upstream detail changed; `ROADMAP.md` checkboxes; `docs/ids.md` if ID behaviour changed.
- Summary to the human states what changed, how it was tested, and any manual checks they need to run against Instagram.

### End of every session
Write `sessions/YYYY-MM-DD-<short-slug>.md` (git-ignored, local only) with:
- **Accomplished:** what landed, with branch and commit hashes.
- **Findings:** anything learned that isn't in the docs yet.
- **Open questions:** decisions waiting on the human.
- **Next session:** the concrete first steps, and the suggested effort level (§16).

At the start of a session, read the newest file in `sessions/`. No credentials, real Instagram IDs, usernames or message text in these notes.

---

## 14. Commits and PRs
- Small, focused commits; imperative subject ≤ 72 chars, e.g. `store: add thread ID mapping table`. Prefix with the package name.
- Branch per task: `phase1/store-schema`, `fix/otid-reconcile`.
- PR description: what, why, how tested, manual steps for the human, and `Closes #N` for the issue it finishes. Follow `.github/pull_request_template.md`.
- Every issue and PR gets one `type:` label (`feature`, `bug`, `docs`, `test`, `chore`, `security`) and one `area:` label (the package: `core`, `app`, `instagram`, `fake`, `store`, `secrets`, `config`, `ui`, `cli`, or `ci` for tooling and releases), plus the phase milestone when it belongs to one.
- Keep PRs under about 400 changed lines, not counting generated files and golden files; split bigger ones into a stack.
- Before merging, read the whole diff (`gh pr diff`) against §8 and §13 and post a `Self-review:` comment saying what you checked and what's left for the human.
- `main` is protected: the CI `check` job must pass before a PR merges, and merged branches are deleted.
- Track work in GitHub issues, one milestone per phase (§12). Find or open the issue before starting a task; open new issues for follow-ups and `shortcut:` gaps.
- Stacked PRs: merge in order, with merge commits or rebase rather than squash, and retarget the next PR to `main` after each merge.
- **No AI attribution anywhere.** No `Co-Authored-By` trailers, "Generated with …" lines, or any mention of Claude or other AI tools in commits, PRs, code, comments or docs. This overrides any tool default.

### 14.1 Releases
- Release often from v0.1.0: whenever features, fixes or polish land, not only at the end of a phase. SemVer `v0.Y.Z` until 1.0: minor for features, patch for fixes only.
- A release is an annotated `vX.Y.Z` tag on `main`. Pushing it runs `.github/workflows/release.yml`: tests, then GoReleaser (`.goreleaser.yaml`) builds macOS and Linux (amd64, arm64), publishes a GitHub Release with checksums, and updates the cask in `ericliswan/homebrew-tap` with the `HOMEBREW_TAP_TOKEN` secret. Prereleases (`v0.2.0-rc1`) skip the tap. Windows isn't packaged.
- The agent prepares a release: `main` is green, `make snapshot` builds, notes list what users will notice since the last tag, and the summary lists the manual smoke test (§10) for the human. **Push the tag only after the human says "release vX.Y.Z".** Publishing is outward-facing and can't be fully undone.
- The notes go in the tag message, after a subject line `meow vX.Y.Z`: `git tag -a vX.Y.Z --cleanup=verbatim -F notes.md` (verbatim keeps Markdown `#` lines). GoReleaser puts them above GitHub's list of merged PRs.
- Never move or delete a published tag; fix a bad release with a new patch release.

---

## 15. Known limitations and gotchas
- **Unofficial client.** Instagram's terms don't allow it; accounts can be flagged, checkpointed or locked. Development uses a secondary test account.
- **No end-to-end encrypted chats** (upstream doesn't support them on Instagram).
- **Upstream is internal API** — expect breakage on bumps; keep it contained.
- **Three thread IDs** with misleading field names (§6.3).
- **Saved state expires after 24 h** (`MaxCachedStateAge`).
- **Password login can hit browser-only checks**; cookie login is the reliable path.
- **Cookies rotate silently** (§7.2).
- `instameow` pulls in `mautrix/bridgev2` and `messagix`; that's expected.

---

## 16. When to stop and ask the human
- **Effort level:** at the start of **every** task, tell the human which reasoning effort to use — **medium**, **high**, **extra high** or **max** (table below) — with a one-line reason. You can't change your own level; the human sets it. If a task turns out harder or easier than it looked (a "simple" fix is really a race), say so and recommend the new level.
- A change would need real Instagram credentials or a live call to verify.
- Upstream behaviour contradicts §6 or `docs/ids.md`.
- An upgrade would spread upstream types beyond `backend/instagram`.
- You want to add a dependency, change the license, or change a hard rule.
- You'd push a release tag the human hasn't asked for by version (§14.1).
- The task is ambiguous about scope, or touches something hard to undo (deleting user data, migrations that drop columns).

| Level | Use for | Examples |
|---|---|---|
| **medium** | Routine, mechanical, easy to check | boilerplate, Makefile/CI, UI layout and styling, golden-file updates, renames, doc edits, ROADMAP ticks, spike scripts, release tooling (GoReleaser, workflows, tap) |
| **high** | Normal feature work with some design choices | UI components with state, config, store queries, `convert.go`, login input parsing, content-type mapping, `backend/fake` features, bug fixes with a known cause |
| **extra high** | Design other code builds on; mistakes spread | `core` interfaces and events, store schema and migrations, `ids.go` and `docs/ids.md`, send reconciliation, resume/reconnect, the lifecycle state machine, the password login step machine |
| **max** | Subtle correctness or security; hard to test, costly to get wrong | the event pump and durability, concurrency bugs and races, `secrets` crypto, upstream upgrades (§11), pre-release security review |

Torn between two levels: pick the higher one if the task touches a hard rule (§2) or the security checklist (§8), otherwise the lower one.

---

## 17. References
- mautrix/meta: https://github.com/mautrix/meta (AGPL-3.0)
- Bridge docs: https://docs.mau.fi/bridges/go/meta/index.html
- Bubble Tea / Bubbles / Lip Gloss / teatest / vhs: https://github.com/charmbracelet
- go-keyring: https://github.com/zalando/go-keyring
- modernc SQLite: https://pkg.go.dev/modernc.org/sqlite
- adrg/xdg: https://github.com/adrg/xdg
- Bridge community room: `#meta:maunium.net`