# termeownal — terminal Instagram DMs (Option A plan)

> **termeownal** (repo) / **`meow`** (command). A single Go binary that logs in to your Instagram account and lets you read and send DMs from the terminal. It embeds `instameow`, the Instagram client library inside [mautrix/meta](https://github.com/mautrix/meta), and puts a Bubble Tea TUI on top. No Matrix, no servers.

_Plan only — no code yet. Written against `mautrix/meta` `main` as of 27 Oct 2026. Function and type names below are from that commit; re-check them when you pin a version (§13)._

---

## Contents

1. [Decisions](#1-decisions)
2. [Goals and non-goals](#2-goals-and-non-goals)
3. [How it works](#3-how-it-works)
4. [What we use from upstream](#4-what-we-use-from-upstream)
5. [Architecture](#5-architecture)
6. [Login](#6-login)
7. [Sync: snapshot, live events, reconnects](#7-sync-snapshot-live-events-reconnects)
8. [Sending](#8-sending)
9. [Rendering messages](#9-rendering-messages)
10. [Local storage](#10-local-storage)
11. [TUI](#11-tui)
12. [Security](#12-security)
13. [Managing the upstream dependency](#13-managing-the-upstream-dependency)
14. [Milestones](#14-milestones)
15. [Project layout](#15-project-layout)
16. [Testing](#16-testing)
17. [Risks](#17-risks)
18. [Open questions](#18-open-questions)
19. [References](#19-references)

---

## 1. Decisions

| Decision | Choice | Notes |
|---|---|---|
| Architecture | **Option A**: embed `go.mau.fi/mautrix-meta/pkg/instameow` | Matrix stack (Option B) stays possible later via a second backend (§5) |
| Language | Go | Required by the library |
| TUI | Bubble Tea + Bubbles + Lip Gloss | |
| Storage | SQLite via `modernc.org/sqlite` (pure Go, no cgo) | Cache only; secrets go elsewhere |
| Secrets | One encrypted session file; its key in the OS keyring (`zalando/go-keyring`), passphrase fallback | §12 |
| First login method | Browser cookies | Password login in a later phase |
| License | **AGPL-3.0** | Required: importing an AGPL package makes termeownal a derivative work |
| Accounts | One | Multi-account is a stretch goal |

---

## 2. Goals and non-goals

### v1 (MVP)
- `meow login` connects one Instagram account; `meow logout` removes it.
- Thread list (1:1 and groups), newest first, with unread markers.
- Open a thread, read history, scroll back for older messages.
- Send text, including replies.
- New messages arrive live.
- Start a new DM by searching for a username.
- Stays logged in between runs; starts instantly from cache.

### Later
- Typing indicators, read receipts, reactions, edits, unsends.
- Media: placeholders → open in external viewer → inline images on supporting terminals → sending images.
- Message requests, desktop notifications, password login, packaging.

### Non-goals
- Feed, stories, reels, posting, profile editing.
- Other networks (that would be Option B or new adapters).

### Known limitation
- **End-to-end encrypted Instagram chats are not supported.** The bridge's feature list only covers encryption for Messenger; the Instagram connector has no E2EE code. Chats a user has switched to end-to-end encryption will be missing or unreadable. Phase 0 couldn't create one to test (`docs/ids.md` §9), so the UI labels any thread with an unfamiliar subtype or `input_mode` instead of failing silently.

---

## 3. How it works

The bridge has two halves: an Instagram half (`instameow`) and a Matrix half (`igconnector` + bridgev2). termeownal keeps the Instagram half and replaces the Matrix half with a terminal UI.

```
┌────────────────────────── termeownal (one binary) ───────────────────────────┐
│                                                                              │
│  ┌──────────┐  tea.Msg   ┌──────────┐  core.Event  ┌───────────────────────┐ │
│  │   ui     │◄───────────│   app    │◄─────────────│ backend/instagram     │ │
│  │ (Bubble  │            │ (pump,   │              │  (adapter)            │ │
│  │  Tea)    │──tea.Cmd──►│  wiring) │──calls──────►│   └─ instameow ───────┼─┼──► Instagram
│  └──────────┘            └────┬─────┘              └───────────────────────┘ │
│                               │                                              │
│                        ┌──────▼──────┐   ┌──────────┐                        │
│                        │ store       │   │ secrets  │                        │
│                        │ (SQLite)    │   │ (keyring)│                        │
│                        └─────────────┘   └──────────┘                        │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Rule:** only `internal/backend/instagram` imports anything from `go.mau.fi/mautrix-meta` **or** `maunium.net/go/mautrix`. (`instameow` itself imports `mautrix/bridgev2` — some login functions return `*bridgev2.LoginStep` — so bridge framework types will show up in the adapter.) Everything else uses termeownal's own `core` types. This is what keeps upstream changes from rippling through the codebase.

`igconnector` is not imported, but it's the best **reference implementation** — it already solves every problem in this plan. Read it, don't depend on it.

---

## 4. What we use from upstream

### 4.1 Client construction
`instameow.NewClient(instameow.ClientParams{...})`. Fields that matter:

| Field | What we pass |
|---|---|
| `Cookies` | `*cookies.Cookies` with `Platform: types.Instagram` (from `pkg/messagix/cookies`) |
| `Log` | a `zerolog.Logger` writing to termeownal's log file |
| `Settings` | HTTP client settings (see how `igconnector.getInstaClient` fills it) |
| `EventHandler` | the adapter's handler (§7) |
| `SeqID`, `SeqIDTS` | last saved values, so the socket resumes instead of starting over |
| `DisableTyping` | from config |
| `NativeSession` | only for the Android login flow (later) |

Also: `LoadState(raw)` / `DumpState()` to save and restore reconnection state, `SetSeqID`, `GetOwnFBID`, `GetCookies`, `IsAuthenticated`, `SetLogger`, `SetEventHandler`.

### 4.2 Calls

| termeownal need | `instameow` call | Phase |
|---|---|---|
| Verify login + first snapshot | `LoadIndex(ctx)` → `(*types.PolarisViewer, *slidetypes.Mailbox, error)` | 0 |
| Refresh tokens without reprocessing the inbox | `ReloadIndex(ctx)` | 1 |
| Thread list / more threads | `GetMailbox`, `PaginateMailbox` | 1 |
| Thread details | `GetThread(ctx, slidetypes.MakeGetThreadInfoRequest(id))` | 1 |
| History | `PaginateMessages(&PaginateMessagesRequest{ThreadID, OlderThanMessageID, FirstN, …})` — `ThreadID` is the thread's **`ThreadInfo.ID`** (§4.5) | 1 |
| Send text | `SendMessage(&SendTextRequest{IGThreadIGID, Text, OfflineThreadingID, ReplyToMessageID, …})` — `IGThreadIGID` is the short ID **`ThreadInfo.ID`** (the long ID also works); first message to a new user: `RecipientIGIDs: []string{userFBID}` and no `IGThreadIGID` | 1 |
| Mark read | `MarkRead(&MarkReadRequest{Metadata{IGThreadIGID}, Data{MessageID, …}})`, then `MarkReadValidation` with the same request (upstream calls both) | 1 |
| Typing out | `SetTyping(ctx, shortID, typing)` — `ThreadInfo.ID` | 2 |
| Search people | `SearchUsers(ctx, query)` | 2 |
| New DM target | `GetUserForNewDM(ctx, fbid)`, `GetProfile(ctx, igid)` | 2 |
| Reactions | `SendReaction(&CreateReactionRequest{…})` | 2 |
| Edit / unsend | `EditMessage`, `UnsendMessage` | 2 |
| Message requests | `AcceptMessageRequest` | 2 |
| Media download | `FetchMedia(ctx, mediaID, shortcode)` | 3 |
| Media send | upload via `GetHTTP().SendMercuryUploadRequest(...)`, then `SendMedia(&SendMediaRequest{…})` — follow `igconnector/handlematrix.go` (`SendMedia` call) and `msgconv` | 3 |
| ID lookup | `FetchThreadID(ctx, threadKey)` → `ThreadIGIDs{LongID, ShortID}` | 1 |
| Live updates | `Connect(ctx)`, `Disconnect()`, `ForceReconnect()` | 1 |
| Password login | `CreateInstagramWebSession` → `CompleteInstagramWebSessionTwoFactor`, plus consent / captcha / auth-platform / account-picker steps (§6.2) | 4 |

### 4.3 Events delivered to the handler (`slidetypes.ClientEvent`)

| Event | Meaning | termeownal reaction |
|---|---|---|
| `Connected` | socket up | status bar "connected" |
| `Disconnected` | socket down | status "reconnecting…" |
| `ReconnectionStateUpdate` | new resumable state | save `DumpState()` |
| `SeqIDUpdate` | new sequence ID | save seq ID + timestamp |
| `ResnapshotRequired` | server says local state is stale | re-run `GetMailbox`, refresh open thread |
| `AuthError` | session invalid | stop, show "session expired — run `meow login`" |
| `TypingNotification` | someone typing | typing line in chat view |
| `Delta` → `DeltaEvent` | content changes (below) | update store + UI |

`DeltaEvent` types: `NewMessageEvent`, `AdminMessageEvent`, `EditMessageEvent`, `DeleteMessageEvent`, `CreateReactionEvent`, `DeleteReactionEvent`, `ReadReceiptEvent`, `MarkReadEvent`, `MarkUnreadEvent`, `DeleteThreadEvent`, `UpdateThreadFolderEvent`, `UpdateThreadNameEvent`, `UpdateThreadImageEvent`, `ParticipantJoinEvent`, `ParticipantLeaveEvent`, `AdminChangeEvent`, `MuteThreadEvent`, `PinThreadEvent`, `PinMessageEvent`, plus `UnknownEvent` / `IgnoredEvent`.

### 4.4 Errors to handle
- From `pkg/messagix/httpclient`: `ErrRateLimited`, `ErrChallengeRequired`, `ErrCheckpointRequired`, `ErrConsentRequired`, `ErrAccountSuspended`, `ErrTokenInvalidated`, `ErrUnexpectedError`.
- From `pkg/messagix/types`: `ErrPleaseReloadPage` (upstream deletes the saved reconnection state and starts fresh), `*types.ErrorResponse` (error code `1357053` means bad credentials), `*types.GraphQLError`.
- From `instameow`: `ErrCachedStateTooOld` (saved state older than 24 h — fall back to `LoadIndex`), `ErrThreadNotFound`, `ErrClientIsNil`, `ErrMailboxMissing`.

Each gets a human-readable message (§6.4).

### 4.5 ⚠️ The ID model (biggest gotcha)
A thread has **three IDs that matter**, and each call wants a specific one. From `igconnector` (`chatinfo.go`, `handlematrix.go`, `backfill.go`):

| `ThreadInfo` field | Type | Upstream name | Used for |
|---|---|---|---|
| `ThreadKey` | int64 | FB thread key — the bridge's **primary key** for a chat | `FetchThreadID(ctx, threadKey)`; for 1:1 chats the key is the **other user's FBID**; for groups it **equals the short ID** |
| `ID` | string | "short" ID (JSON `id`, equal to `thread_fbid`; `ThreadIGIDs.ShortID`) | `PaginateMessagesRequest.ThreadID`, `GetThread`, `SendTextRequest.IGThreadIGID`, `SetTyping`; **live deltas identify the thread by this** — the delta field is confusingly named `ThreadIGID` but its JSON is `thread_fbid`, with `Message.ThreadFBID` as fallback |
| `ThreadID` | string | "long" IG thread ID (JSON `thread_id`, 39 digits, keep as a string; `ThreadIGIDs.LongID`) | `MarkReadMetadata.IGThreadIGID`; incoming `TypingNotification.ThreadID` |

Plan:
- termeownal's `core.ThreadID` = **`ThreadKey`** (stable int64, matches upstream's choice).
- The adapter keeps a table `ThreadKey ↔ ID ↔ ThreadID` in memory, filled from every mailbox/thread response. Nothing outside the adapter sees short or long IDs.
- A delta for an unknown short ID → `GetThread` to learn its other IDs, store, then apply the delta (upstream: `ensurePortal` / `getAndResyncThread` in `handlemeta.go`, lookup via `GetFBIDForIGChat`).
- **Never trust field names** here; trust the JSON tag and the table above.
- Missing long/short ID for a known key → `FetchThreadID(ctx, threadKey)`.
- Users: `core.UserID` = FBID (`SenderFBID`, `GetOwnFBID`), with IGID + username stored alongside (upstream: `PutFBIDForIGUser`). Typing notifications carry the long ID and the sender's IGID, so keep long → key and IGID → FBID maps too.
- Phase 0 verified all of this against real responses; `docs/ids.md` is the source of truth.

### 4.6 Reference code to read before writing each part

| termeownal part | Read in upstream |
|---|---|
| Login | `pkg/igconnector/login.go` (`loginWithCookies`, `submitInstagramCookies`), `login_native.go` |
| Connect / reconnect | `pkg/igconnector/client.go` (`Connect`, `connectWithRetry`, `schedulePeriodicReconnect`, `FullReconnect`) |
| Initial inbox processing | `pkg/igconnector/chatsync.go`, `client.go` (`processMailbox`) |
| Handling deltas | `pkg/igconnector/handlemeta.go` |
| Sending | `pkg/igconnector/handlematrix.go` (`getOTID`, send path) |
| History | `pkg/igconnector/backfill.go` |
| Content types | `pkg/instameow/slidetypes/messagecontent.go`, `pkg/msgconv/` |
| Log redaction | `pkg/instameow/redactlog.go` |

---

## 5. Architecture

### 5.1 Packages

| Package | Responsibility | Imports upstream? |
|---|---|---|
| `cmd/meow` | CLI entry: `meow`, `meow login`, `meow logout`, flags | no |
| `internal/app` | Wires everything; owns lifecycle and the event pump | no |
| `internal/core` | Domain types (`Thread`, `Message`, `User`, …), `Backend` interface, `Event` types | no |
| `internal/backend/instagram` | Adapter: `instameow` ↔ `core` | **yes (only here)** — both `go.mau.fi/mautrix-meta` and `maunium.net/go/mautrix` |
| `internal/backend/fake` | Deterministic fake backend with canned chats | no |
| `internal/store` | SQLite schema, migrations, queries | no |
| `internal/secrets` | Keyring + encrypted-file fallback | no |
| `internal/config` | TOML config, XDG paths | no |
| `internal/ui/...` | Bubble Tea models | no |

### 5.2 `core.Backend` interface (Phase 1)

| Method | Returns | Notes |
|---|---|---|
| `Restore(ctx, Session)` | error | Builds client from saved cookies/state and seq ID |
| `Snapshot(ctx)` | `me User`, `[]Thread`, error | Wraps `LoadIndex` / `GetMailbox`; threads carry their newest messages |
| `History(ctx, thread, before, n)` | `[]Message` (oldest first), more bool, error | `PaginateMessages` |
| `NewOTID()` | string | `methods.GenerateEpochID()`; the app needs the OTID before sending (§8) |
| `Send(ctx, thread, text, otid)` | error | `SendMessage` |
| `MarkRead(ctx, thread, upTo)` | error | `MarkRead` + `MarkReadValidation` |
| `Connect(ctx)` | error | blocks while upstream's socket loop reconnects on its own |
| `Events()` | `<-chan core.Event` | normalized events; closed by `Close` |
| `ExportAuth()` | `AuthData`, error | cookies + `DumpState()`, for saving after `SessionChanged` |
| `Close()` | | |

`core.ThreadID` is the thread key; short and long IDs stay in the adapter. The seq ID isn't in `ExportAuth`: the pump saves it from `SeqAdvanced`, so fresh cookies can never be saved with a seq ID ahead of the store. Phase 2 adds `SearchUsers`, `StartDM` and parameters for replies; `MoreThreads` (`PaginateMailbox`) comes when the thread list needs it.

Login lives in a separate `core.Authenticator` interface because it's a multi-step, interactive flow (§6): `Start`, then `Submit` each `LoginStep`'s fields until a step carries a `LoginResult`.

### 5.3 `core.Event` types
Phase 1: `StatusChanged{Connecting|Connected|Reconnecting}`, `AuthFailed{Err}`, `ResyncNeeded`, `MessageAdded{Message}`, `ThreadRead{Thread, Until}` (read on another device), `ThreadUpdated{Thread}` (name, members, unread; also announces a thread the backend hadn't reported yet), `SeqAdvanced{SeqID, At}` (persisted by the pump after the deltas before it), `SessionChanged` (state or cookies changed → `ExportAuth` and persist).

Phase 2 adds `MessageEdited{ID, Text, At}`, `MessageDeleted{ID}`, `ReactionAdded/Removed{Msg, User, Emoji}`, `ReadReceipt{Thread, User, UpTo}`, `ThreadRemoved{ID}`, `Typing{Thread, User, Active}`.

### 5.4 Concurrency
```
instameow goroutine ──handler──► adapter: translate ──► buffered chan core.Event
                                                              │
                                              app pump goroutine (single)
                                               1. write to store
                                               2. program.Send(uiMsg)
                                                              │
                                                    Bubble Tea update loop
```
How upstream calls the handler (from `instameow/socket.go`): for each batch it calls the handler once per delta, **then** emits one `SeqIDUpdate` with the new sequence ID. Live batches are usually one delta; the catch-up after connect is one large batch. If the handler returns an error, the socket treats it as a failure and reconnects.

So:
- The handler translates and pushes onto the channel. If the channel is full it **blocks** (respecting `ctx`) — never drop events. Blocking just slows the socket down, which is safe.
- `SeqIDUpdate` goes through the **same channel** as the deltas, and the pump saves it only after the deltas before it are written. A crash then can never save a seq ID that is ahead of the stored messages; on restart the server replays from the last saved seq ID.
- Delivery is therefore **at-least-once**, and the server also repeats some events (each `ReadReceipt` arrived twice during catch-up). Applying a delta twice must be harmless: upsert by message ID, keep the highest watermark, match echoes by OTID.
- The handler returns an error only for real failures (e.g. `ctx` cancelled), not for unknown event types.
- The pump is the **only writer** to the store for live events, so ordering is preserved.
- UI actions (send, load history, search) run as `tea.Cmd` (off the UI goroutine) and return result messages.

### 5.5 App lifecycle (state machine)
```
          no session
 Start ─────────────► NeedsLogin ──(meow login ok)────┐
   │                                                  ▼
   │ session found                               Restoring
   └─────────────────────────────────────────────►    │ LoadIndex ok
                                                      ▼
            ResnapshotRequired ┌──────────────── Syncing (apply snapshot)
                    ┌──────────┘                      │ Connect
                    ▼                                 ▼
              Resyncing ◄───────────────────────── Live ◄──── Reconnecting
                                                      │  ▲  Disconnected / ok
                                    AuthError         ▼  │
                                  ────────────► AuthFailed (banner, stop)
```

---

## 6. Login

### 6.1 Cookie login (Phase 1)
Instagram cookies needed (from `cookies.IGRequiredCookies` / `IGOptionalCookies`):

| Cookie | Required |
|---|---|
| `sessionid` | ✅ |
| `csrftoken` | ✅ |
| `ds_user_id` | ✅ |
| `rur`, `shbid`, `shbts`, `mid`, `ig_did` | optional, improves stability |

**User flow for `meow login`:**
1. Print short instructions: in a **Chromium-based browser** (the library sends a fixed desktop-Chrome-on-Linux user agent, so cookies from the same browser family look most consistent), open a private window, log in at instagram.com, open DevTools → Application → Cookies → `https://www.instagram.com`. After copying, **close the window without logging out** — logging out on the website invalidates `sessionid`.
2. Accept any of three input forms (the bridge accepts the first two too):
   - a JSON object `{"sessionid": "...", ...}`
   - a **"Copy as cURL"** command from the Network tab (parse the `Cookie:` header)
   - guided prompts, one cookie at a time, **with echo off**
3. Check required cookies present (the cookies package can report missing ones).
4. Build `cookies.Cookies{Platform: types.Instagram}`, create client, call `LoadIndex`.
5. On success: show "Logged in as *name* (@username)", save session (§6.3), seed cache from the returned mailbox.
6. On failure: map the error (§6.4), don't save anything.

Input is read from the terminal with no echo; never accept cookies via command-line flags or env vars by default.

### 6.2 Password login (Phase 4)
Upstream supports "Native Web" and "Native Android". Native Web is **not** just password + 2FA: depending on what Instagram asks for, it can go through cookie consent (`ContinueInstagramWebSessionAfterCookieConsent`), captcha (`HasInstagramWebCaptcha` / `SubmitInstagramWebCaptcha` — the captcha is solved by JavaScript in a browser, see `login_web_captcha.js`), an auth-platform challenge (`DoInstagramWebAuthPlatformSteps`), and an account picker (`login_account_manager.go`). Several of these return `*bridgev2.LoginStep`.

Plan:
- Treat password login as **best effort**. Implement it as a step machine in the adapter that converts each upstream step into an termeownal prompt (text input, code, choice).
- Mirror `igconnector/login_native.go` step by step.
- A captcha (or any step termeownal can't show in a terminal) ends the flow with: "Instagram wants a browser check — use `meow login --cookies` instead."
- Never store the password; only the resulting session.

### 6.3 What gets saved
| Item | Where |
|---|---|
| Cookies, `DumpState()` blob, seq ID + timestamp | one **encrypted session file** (`session.enc`, §12) |
| The key for that file | OS keyring (random 256-bit key) — or derived from a passphrase when no keyring exists |
| Username, display name, FBID, IGID | SQLite `account` table (not secret) |

Why not put everything in the keyring: `DumpState()` holds the web page config blob, which can be several KB, and some keyrings have small size limits (Windows Credential Manager ≈ 2.5 KB). A small key in the keyring + one encrypted file works everywhere.

Cookies rotate over time (`rur`, `shbid`, …). The library updates its `*cookies.Cookies` **in place** from HTTP responses (`UpdateFromResponse`) and has **no change callback**. So: every few minutes, on `Connected`, after login, and on exit, snapshot `GetCookies()` and rewrite the session file if it changed.

### 6.4 Error messages
| Upstream error | termeownal says |
|---|---|
| `ErrRateLimited` | "Instagram is rate-limiting this account. Wait a while and try again." |
| `ErrChallengeRequired` / `ErrCheckpointRequired` | "Instagram wants to verify this login. Open the app or instagram.com, complete the check, then log in again." |
| `ErrConsentRequired` | "Accept Instagram's pending terms in the app/website, then retry." |
| `ErrAccountSuspended` | "This account is suspended." |
| `ErrTokenInvalidated` | "Session expired. Run `meow login`." |
| `ErrCachedStateTooOld`, `ErrPleaseReloadPage` | nothing — silently drop the saved state and do a full `LoadIndex` |
| `ErrorResponse` code `1357053` | same as `ErrTokenInvalidated` |
| missing cookies (`GetMissingCookieNames`) | lists which ones |
| anything else | generic message + "see log at <path>" |

---

## 7. Sync: snapshot, live events, reconnects

### 7.1 Startup
Mirror upstream's three-tier resume (`igconnector/client.go`, `connectWithRetry`):

1. Render threads from SQLite immediately (works offline).
2. Create the client with saved cookies, then `SetSeqID(savedSeq, savedTS)` if one exists.
3. Pick a path:
   - **Fast resume** — saved state exists, `LoadState` succeeds (it rejects state older than 24 h with `ErrCachedStateTooOld`) and `HasSeqID()` → go straight to `Connect`. The server replays everything missed since the seq ID.
   - **Warm resume** — seq ID but no usable state → `ReloadIndex`, save the new `DumpState()`, then `Connect`.
   - **Cold start** — no seq ID (first run, or after a reset) → `LoadIndex`, apply the mailbox snapshot (threads, latest messages, users), save state, then `Connect`.
4. After `Connected`, refresh the mailbox in the background with `GetMailbox` so titles and unread counts reconcile with anything the delta replay didn't cover. (Upstream skips this; it's cheap for us and keeps the list honest.)
5. For the thread currently open: fetch the newest page with `PaginateMessages` to fill any gap.

On `ErrPleaseReloadPage`, delete the saved state and fall back to cold start.

### 7.2 Applying live deltas
| Delta | Store update | UI |
|---|---|---|
| `NewMessageEvent` | insert message (or reconcile with pending send by OTID, §8) ; bump thread `last_activity`; `unread++` unless thread is open/focused or sender is me | add line; reorder list |
| `AdminMessageEvent` | insert as `kind=admin` | dim system line |
| `EditMessageEvent` | update text, set `edited_at` | "(edited)" |
| `DeleteMessageEvent` | `deleted=1`, clear text | "message unsent" |
| `Create/DeleteReactionEvent` | upsert/delete reaction | reaction row under message |
| `ReadReceiptEvent` | update member's read pointer | "Seen" / ✓✓ |
| `MarkReadEvent` / `MarkUnreadEvent` | set unread=0 / mark unread | marker |
| `UpdateThreadName/Image`, `Participant*`, `MuteThreadEvent`, `UpdateThreadFolderEvent` | update thread | refresh header/list |
| `DeleteThreadEvent` | remove thread | remove from list |
| `UnknownEvent` | log at debug | — |

### 7.3 Reconnects and resyncs
- On `Disconnected`: status "reconnecting"; rely on the library's reconnect, plus our own backoff (1 s → 2 → 4 … max 60 s, with jitter) if it gives up.
- After reconnect: refresh mailbox, gap-fill the open thread.
- On `ResnapshotRequired`: full mailbox refresh; reset seq ID as upstream does (see `FullReconnect`).
- **Periodic refresh:** upstream forces a full reconnect every **20 hours** by default (`force_refresh_interval_seconds: 72000`), and never does full reconnects more often than once an hour (`min_full_reconnect_interval_seconds: 3600`). The saved state also expires after 24 h. termeownal copies these defaults: `force_refresh_interval` is a setting (0 disables), the one-hour floor is a constant.
- Detect system sleep/wake (wall clock jump > 2 min between ticks) → `ForceReconnect()`.
- Always persist `SeqIDUpdate` / `ReconnectionStateUpdate` (through the pump, in order — §5.4) so restarts resume cheaply.

---

## 8. Sending

1. User presses Enter. App generates an **OTID** (offline threading ID) — upstream uses `methods.GenerateEpochID()` from `pkg/messagix/methods`; reuse it inside the adapter.
2. Insert a local message: `send_state=pending`, `otid=OTID`. UI shows it dimmed with "sending…".
3. `tea.Cmd` calls `Backend.Send` → `SendMessage` with `IGThreadIGID` set to the short ID (from the ID map, §4.5), `Text`, `OfflineThreadingID`, and reply fields if replying.
4. Success: keep pending until the echo arrives.
5. Echo: `NewMessageEvent` whose `Message.OfflineThreadingID` == OTID → replace local row with the real message ID, `send_state=sent`.
6. Failure, or no echo within ~30 s: `send_state=failed`; UI shows "failed — `r` to retry". Retry reuses the **same OTID** so a late echo still matches and no duplicate appears.
7. New DM: upstream keys a 1:1 chat by the other user's FBID. Look up the existing DM with `FetchThreadID(ctx, userFBID)`; if it returns `ErrThreadNotFound`, show an empty chat (user info from `GetUserForNewDM`) and create the thread on first send: `SendMessage` with `RecipientIGIDs: []string{userFBID}` and no `IGThreadIGID`. The echo carries a short ID we haven't seen; call `FetchThreadID(ctx, userFBID)` again to learn the thread's IDs. Verified in Phase 0 (`docs/ids.md` §6).

Text limits: strip trailing whitespace; reject empty; warn above Instagram's message length limit (still unknown; find it in Phase 1).

---

## 9. Rendering messages

`slidetypes.Message` has `ContentType`, `Content` (`MessageContentWrapper`), `TextBody`, `IGDSnippet`, `RepliedToMessage`, `Reactions`, `SlideEditHistory`, `IGDIsForwarded`, `ExpirationTimestampMS`, etc.

Plan:
- Phase 1: render `TextBody`; for anything else render a bracketed label from `ContentType` (`IGDSnippet` is empty in history). Skip `REACTION_LOG_XMAT` entries; they are reaction log lines, not messages. Collect every `ContentType` value you see in a debug log to build the mapping.
- Phase 2+: proper mapping table, e.g.

| Content | Render |
|---|---|
| text | text, with `@mentions` highlighted |
| image / video / GIF / sticker | `[photo]`, `[video 0:12]`, `[gif]`, `[sticker]` — `o` opens |
| voice | `[voice 0:08]` — `o` plays with system player |
| shared post / reel / story / profile | `[reel by @user]`, `[post by @user]`, `[story]`, `[profile @user]` — `o` opens the URL |
| forwarded | prefix `↪ forwarded` |
| reply | quoted first line of `RepliedToMessage` above |
| expiring / view-once | `[view-once photo]` (don't download) |
| unknown | `[unsupported: <type>]` |

Use `pkg/msgconv` as the reference for how each content type is unpacked.

---

## 10. Local storage

SQLite file `cache.db` in termeownal's data directory. Paths: use `adrg/xdg` (or `os.UserConfigDir` / `os.UserCacheDir`) so macOS gets `~/Library/Application Support/termeownal` and Linux gets `$XDG_DATA_HOME/termeownal`. Schema versioned with simple numbered migrations.

| Table | Columns |
|---|---|
| `account` | `fbid` PK, `igid`, `username`, `display_name` (seq ID lives in the session file, §6.3) |
| `users` | `fbid` PK, `igid`, `username`, `full_name`, `avatar_url`, `updated_at` |
| `threads` | `thread_key` PK, `title`, `is_group`, `last_activity_ms`, `unread`, `marked_unread`, `muted`, `pinned`, `folder`, `image_url`, `history_complete` |
| `thread_members` | `thread_key`, `user_fbid`, `nickname`, `is_admin`, `read_up_to_msg_id` |
| `messages` | `id` PK, `thread_key`, `sender_fbid`, `ts_ms`, `kind`, `content_type`, `text`, `snippet`, `reply_to_id`, `edited_at`, `deleted`, `forwarded`, `otid`, `send_state` |
| `reactions` | `message_id`, `user_fbid`, `emoji`, `log_message_id` (removals can arrive with sender 0; `docs/ids.md` §4) |

Indexes: `messages(thread_key, ts_ms, id)`, `messages(otid)`, `threads(last_activity_ms)`. Migration 1 creates only the columns Phase 1 uses; each later phase adds its columns, and the `reactions` table, in a new migration. A thread's cached messages are kept contiguous (a newest page that doesn't reach the cache drops the older cached ones), so the oldest cached message is the paging cursor. Short and long thread IDs aren't stored: the adapter keeps them in memory (§4.5).

The cache is **disposable** (`meow --reset-cache`). Secrets never go in it.

---

## 11. TUI

### Layout
```
┌ Chats ─────────────────────┐┌ alex.k ──────────────────────────────────────┐
│ ● alex.k             2m    ││                                  Yesterday   │
│   sure, see you then       ││ alex.k  hey are you coming tonight?   21:04  │
│   study group        1h    ││ you     yeah, 8pm?                    21:06 ✓│
│   maria: lol               ││ alex.k  ┃ yeah, 8pm?                         │
│   jordan_            Mon   ││         sure, see you then            21:07  │
│   [reel] shared            ││                                              │
│                            ││                               alex.k typing… │
│ /filter                    │├──────────────────────────────────────────────┤
│                            ││ > message                                    │
└────────────────────────────┘└──────────────────────────────────────────────┘
 ● connected · @you · Tab: switch · Ctrl+N: new DM · ?: help
```

### Components
| Component | Built from | Notes |
|---|---|---|
| Thread list | `bubbles/list` | unread bold + `●`; muted dimmed; `/` filter |
| Chat view | `bubbles/viewport` + custom wrapping | day separators; grouping consecutive messages by sender; load older at top |
| Composer | `bubbles/textarea` | Enter sends, Alt+Enter newline (remap: the textarea's default keymap inserts a newline on Enter); reply banner when replying |
| Status bar | Lip Gloss | connection state, account, hints |
| Overlays | custom | help, new-DM search, errors, confirm dialogs |

### Keys
| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | cycle focus: list → chat → composer |
| `j`/`k`, `↑`/`↓` | move selection |
| `Enter` | open thread / send |
| `Alt+Enter` | newline |
| `g` / `G` | top / bottom of chat |
| `PgUp` at top | load older |
| `/` | filter threads |
| `Ctrl+N` | new DM |
| `r` | reply to selected message (or retry if failed) |
| `e` | react (Phase 2) |
| `o` | open media/link |
| `Esc` | cancel reply / close overlay |
| `?` | help |
| `Ctrl+C` | quit |

### Rules
- Mark a thread read only when the chat view is visible **and** the terminal is focused (Bubble Tea focus reporting, `tea.WithReportFocus`). Terminals that don't report focus: treat as always focused.
- Respect `NO_COLOR`; handle resize; show "terminal too small" under 60×15.
- Width-aware wrapping that handles emoji and CJK (`mattn/go-runewidth`, already pulled in by Lip Gloss).

---

## 12. Security

Session cookies are equivalent to your password and skip 2FA.

- **Storage:** all session secrets (cookies, saved state, seq ID) live in one file, `session.enc` in termeownal's data directory, mode `0600`, encrypted with XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`). The 256-bit key is random and stored in the OS keyring. Fallback when there's no keyring (e.g. headless Linux without Secret Service): derive the key from a passphrase with Argon2id and prompt for it on start. Write the file atomically (temp file + rename) so a crash can't corrupt it.
- **Logs:** default level `info`; never log cookies, tokens or message text. Reuse upstream's redaction (`redactlog.go`, `SensitiveString`). Add a test that runs the fake login and greps the log for cookie names/values.
- **Input:** cookies and passwords read with echo off (`golang.org/x/term`); never from flags.
- **Logout:** deletes the session file and its keyring key; `--purge` also deletes the cache.
- **Cache:** plaintext message history on disk — say so in the README. Optional later: encrypt the DB.
- **Media downloads:** save to a temp dir with `0700`, delete on exit; never auto-open downloads.
- **Debug dumps:** any "record raw events" mode (for tests) must be opt-in and scrub IDs/text before writing.

---

## 13. Managing the upstream dependency

`instameow` is internal to the bridge and can change in any commit.

- **Pin** `go.mau.fi/mautrix-meta` to a specific commit (pseudo-version) in `go.mod`.
- **Toolchain:** upstream's `go.mod` says `go 1.26.0`, so termeownal needs **Go 1.26 or newer** (Go raises termeownal's own `go` line to match when you add the dependency). Upstream's `toolchain go1.27.2` line does **not** apply to you — `toolchain` lines in dependencies are ignored.
- **Isolation:** only `internal/backend/instagram` imports `go.mau.fi/mautrix-meta/...` or `maunium.net/go/mautrix/...` (enforce with a `depguard` lint rule or a small test that scans imports).
- **Dependency weight:** `instameow` pulls in `mautrix/bridgev2` and much of `messagix`. Phase 0 confirmed `CGO_ENABLED=0 go build` works; the spike binary was 22 MB (15 MB stripped) on darwin/arm64.
- **Upgrade checklist** (do it deliberately, e.g. monthly or when something breaks):
  1. Read the bridge's `CHANGELOG.md` and recent commits touching `pkg/instameow` and `pkg/igconnector`.
  2. Bump the pin; `go build ./...`.
  3. Fix compile errors in the adapter only.
  4. Run adapter tests with recorded fixtures.
  5. Manual smoke test with the test account (§16).
- If upstream breaks badly, stay on the old pin; Instagram-side breakage is usually fixed upstream within days, so watch `#meta:maunium.net`.
- Contribute fixes upstream when you find bugs — they're the ones maintaining the protocol work.

---

## 14. Milestones

### Phase 0 — Spike · 1–2 days
Throwaway code in `spike/`. Goal: prove every core call works and learn the ID model.
- [x] Go 1.26+; new module; pin `mautrix-meta`; confirm `CGO_ENABLED=0 go build` works and note binary size. *(Pinned `v0.2610.1-0.20261027123832-e82caa96720b`; needs upstream's `imroc/req` → `beeper/req` replace. Spike binary: 22 MB, 15 MB with `-ldflags="-s -w"`, darwin/arm64.)*
- [x] Load cookies from a local JSON file (gitignored) → `NewClient` → `LoadIndex` → print viewer name and every thread's `ThreadKey`, `ID`, `ThreadID`, title.
- [x] `PaginateMessages` (with `ThreadInfo.ID`) on one thread → print last 20 messages with `ContentType`.
- [x] `SendMessage` to a test thread; confirm the echo arrives with the same `OfflineThreadingID`. *(Short and long ID both work, 1:1 and group; we send with the short ID.)*
- [x] `Connect` → log every event type for a few minutes while you message from your phone (text, reaction, edit, unsend, typing, read). Note which thread ID each delta carries.
- [x] `MarkRead` + `MarkReadValidation`; confirm it clears on the phone.
- [x] Resume paths: restart with saved state + seq ID (fast), seq ID only (`ReloadIndex`), and nothing (`LoadIndex`).
- [x] Find a new-DM target with `FetchThreadID(userFBID)`; send a first message to someone with no existing thread.
- [x] Check how an end-to-end encrypted chat (if the test account has one) appears in the mailbox and deltas. *(Couldn't create one; see `docs/ids.md` §9.)*
- [x] Write `docs/ids.md`: which ID each call takes and returns.

**Done when:** list, read, send, receive and resume all work from the spike. If something fundamental fails here, reconsider Option B before building more.

### Phase 1 — MVP · 1–2 weeks
- [x] Repo skeleton (§15), AGPL-3.0 license, CI (build + test + lint). *(Package directories are created as each package lands.)*
- [x] `config` + XDG paths + file logging.
- [x] `core` types, `Backend`, `Authenticator`, `Event`.
- [x] `backend/fake` with ~10 threads, groups, long histories, and a timer that injects incoming messages.
- [x] **UI against the fake first:** thread list, chat view, composer, status bar, help overlay.
- [ ] `store` with migrations; app pump; cache-first startup.
- [ ] `secrets` (keyring + encrypted fallback).
- [ ] `meow login` (cookie flow, all three input forms) and `meow logout`.
- [ ] `backend/instagram`: restore, snapshot, history, send, mark read, live events, ID map.
- [ ] Optimistic send + echo reconciliation + retry.
- [ ] Unread counts; mark read on focus.
- [ ] Reconnect handling, status bar states, auth-expired banner.

**Done when:** you use termeownal for a full day of normal DMs without opening the app, and restart is instant.

### Phase 2 — Real messenger feel · 1–2 weeks
- [ ] Replies (render + `r`).
- [ ] Typing in/out (configurable).
- [ ] Read receipts per member; "Seen".
- [ ] Reactions shown + send with `e` (small emoji picker).
- [ ] Edits and unsends shown; edit/unsend your own (`EditMessage`, `UnsendMessage`).
- [ ] New DM with `SearchUsers` (`Ctrl+N`).
- [ ] Message requests folder: view + accept.
- [ ] Thread filter, group member list, mute indicator.
- [ ] Content-type mapping table (§9) from Phase 0/1 logs.

### Phase 3 — Media · ~1 week
- [ ] Media placeholders with duration/size where available.
- [ ] `o` → `FetchMedia` → temp file → `xdg-open` / `open`.
- [ ] Inline images on kitty / iTerm2 / sixel terminals (detect support, fall back to placeholder).
- [ ] Send image from path (`/upload ~/pic.jpg` or `Ctrl+U` file prompt) via `SendMedia`.

### Phase 4 — Polish & release
- [ ] Password login (Native Web) with 2FA step.
- [ ] Desktop notifications when the thread isn't focused (respect mute).
- [ ] Themes + keybinding config.
- [ ] GoReleaser builds (macOS, Linux), Homebrew tap, README with demo GIF (recorded with `vhs` against the fake backend so no real data is shown).

### Stretch
- [ ] Multiple accounts.
- [ ] Encrypted cache.
- [ ] `backend/matrix` (Option B) to show any mautrix bridge.

---

## 15. Project layout

```
termeownal/
├── cmd/meow/main.go
├── internal/
│   ├── app/                 # lifecycle, event pump, wiring
│   ├── core/                # types, Backend, Authenticator, Event
│   ├── backend/
│   │   ├── instagram/       # ONLY importer of mautrix-meta
│   │   │   ├── client.go    # construction, restore, connect
│   │   │   ├── login.go     # cookie + (later) password flows
│   │   │   ├── convert.go   # slidetypes → core
│   │   │   ├── events.go    # ClientEvent/Delta → core.Event
│   │   │   ├── ids.go       # FBID ↔ IGID mapping
│   │   │   └── testdata/    # scrubbed recorded payloads
│   │   └── fake/
│   ├── store/               # schema, migrations, queries
│   ├── secrets/             # keyring, encrypted file
│   ├── config/
│   └── ui/
│       ├── root.go          # top-level model, focus, layout
│       ├── threadlist/
│       ├── chat/
│       ├── composer/
│       ├── overlay/         # help, new DM, errors, emoji picker
│       └── theme/
├── docs/
│   ├── ids.md
│   └── architecture.md
├── LICENSE                  # AGPL-3.0
├── README.md
├── ROADMAP.md
├── go.mod
├── Makefile
├── imports_test.go          # import boundary test
└── .github/workflows/ci.yml
```

---

## 16. Testing

| Layer | How |
|---|---|
| Adapter conversion | Feed scrubbed recorded `slidetypes` JSON from `testdata/` → assert `core` types/events |
| ID mapping | Table tests for every case in `docs/ids.md`, including an unknown short ID in a delta |
| Store | In-memory SQLite; migration up from empty and from each prior version |
| Send reconciliation | Fake backend: echo arrives / arrives late / never arrives / duplicate echo |
| Durability | Seq ID never persisted before its preceding deltas; a full event channel blocks instead of dropping; a replayed delta applied twice changes nothing |
| Resume paths | Fast / warm / cold start against a fake upstream |
| UI | `teatest` driving keys against `backend/fake`, golden snapshots of the screen |
| Secrets | Fallback encrypt/decrypt round-trip; wrong passphrase fails cleanly |
| Log safety | Run login + send with fake backend; assert no secrets in log output |
| Upstream isolation | Test that fails if any package other than `backend/instagram` imports `go.mau.fi/mautrix-meta` |

**Manual smoke test** (test account, before every release and upstream bump): login (each input form) · restart resumes · send/receive 1:1 · group chat · reply · reaction from phone · edit + unsend from phone · read on phone clears unread · Wi-Fi off/on reconnects · laptop sleep/wake · logout removes keyring entry.

**No live Instagram calls in CI.**

---

## 17. Risks

| Risk | Likelihood / impact | Mitigation |
|---|---|---|
| Instagram Terms of Use forbid unofficial clients; account flagged, checkpointed or locked | Medium / High | Develop on a **secondary test account**; cookie login; rely on socket instead of polling; modest history paging |
| Upstream `instameow` API changes | High / Medium | Pin; single adapter package; upgrade checklist (§13) |
| Instagram changes its private API | Medium / Medium | Wait for upstream fix; bump pin |
| ID confusion causes sends to wrong thread | Low / High | Phase 0 ID study; ID map tests; show thread name in composer |
| End-to-end encrypted Instagram chats can't be read (bridge doesn't support them) | Certain / Medium | Document it; label threads with an unfamiliar subtype or `input_mode` (Phase 0 couldn't create one to test) |
| Password login hits captcha / browser-only checks | High / Low | Cookie login is the primary path; password login is best effort |
| Saving a seq ID ahead of stored data loses messages after a crash | Low / Medium | Seq ID goes through the same ordered queue as deltas (§5.4) |
| Long sessions drift / miss events | Medium / Low | Resnapshot handling; gap-fill on reconnect; optional periodic refresh |
| Rate limits | Low / Medium | Cache; on-demand paging; backoff on `ErrRateLimited` |
| AGPL obligations | Certain / Low | License as AGPL-3.0 from the first commit |

---

## 18. Open questions

- [ ] Final name?
- [ ] Should message requests show in the main list (dimmed) or a separate tab?
- [ ] Typing indicators on or off by default (privacy)?
- [ ] Mark-as-read behavior: on open, or only after scrolling to bottom?
- [ ] Encrypt the cache in v1, or document and defer?
- [ ] Which terminals to officially support for inline images?
- [ ] Instagram's message length limit (not found in Phase 0; check in Phase 1).

---

## 19. References

- mautrix/meta: https://github.com/mautrix/meta (AGPL-3.0)
- Bridge docs / authentication: https://docs.mau.fi/bridges/go/meta/index.html
- Upstream files to study: listed in §4.6
- Bubble Tea, Bubbles, Lip Gloss, teatest, vhs: https://github.com/charmbracelet
- go-keyring: https://github.com/zalando/go-keyring
- modernc SQLite: https://pkg.go.dev/modernc.org/sqlite
- Bridge community room: `#meta:maunium.net`