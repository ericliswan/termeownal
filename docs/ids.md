# Instagram IDs — verified behaviour

What each `instameow` call takes and returns. This was checked in the Phase 0 spike against a test account on 2026-10-28, with `mautrix-meta` pinned at `v0.2610.1-0.20261027123832-e82caa96720b`. Where this file and `AGENTS.md` §6 or `ROADMAP.md` §4.5 disagree, this file wins; then fix the others.

All IDs below are placeholders. `<fbid>` is 17 digits, `<igid>` 11, `<short>` 16 and `<long>` 39.

## 1. Thread IDs

A thread has three IDs.

| Name | `ThreadInfo` field | JSON | Shape | 1:1 | Group |
|---|---|---|---|---|---|
| **key** | `ThreadKey` (int64) | `thread_key` (string-encoded) | 16–17 digits | the other user's FBID | **equal to the short ID** |
| **short** | `ID` (string) | `id` | 16 digits | own value | own value |
| **long** | `ThreadID` (string) | `thread_id` | 39 digits | own value | own value |

- `ThreadInfo.ThreadFBID` (`thread_fbid`) and the wrapper ID always equal the short ID.
- The long ID is a 128-bit number. It doesn't fit in `int64` or `uint64`, so keep it as a string. Keep the short ID as a string too, like upstream does.
- Subtypes seen: `IG_ONLY_ONE_TO_ONE` (`is_group=false`) and `IGD_GROUP` (`is_group=true`).

The same ID goes by different names in different places. Trust this table, not Go field names.

| ID | Appears as |
|---|---|
| short | `ThreadInfo.ID` (`id`), `ThreadInfo.ThreadFBID`, `Delta.ThreadIGID` (JSON `thread_fbid`; the Go name is wrong), `Message.ThreadFBID`, `ThreadIGIDs.ShortID` (`thread_fbid`) |
| long | `ThreadInfo.ThreadID` (`thread_id`), `ThreadIGIDs.LongID` (`thread_igid`), `MarkReadMetadata.IGThreadIGID` (`ig_thread_igid`), `TypingNotification.ThreadID` (parsed from the MQTT topic) |
| either | `SendTextRequest.IGThreadIGID` (`ig_thread_igid`) accepts both (§3) |

## 2. User IDs

| ID | Shape | Appears as |
|---|---|---|
| **FBID** | 17 digits | `fbid` on users, `viewer.fbid`, `GetOwnFBID()`, `Message.SenderFBID`, `Reaction.SenderFBID`, `ReadReceipt.ParticipantFBID`, 1:1 thread key |
| **IGID** | 11 digits | `igid` on users, `viewer.igid`, `TypingNotification.SenderID` |

- `core.UserID` is the FBID.
- Typing notifications identify the sender by **IGID**. Build an IGID → FBID map from the viewer and every thread's user list.
- A thread's `users` list **excludes the viewer**: a 1:1 lists only the other person. The viewer comes from `LoadIndex`.

## 3. Which ID each call takes

| Call | Takes | Returns | Verified |
|---|---|---|---|
| `LoadIndex`, `GetMailbox` | — | viewer + all three thread IDs per thread | yes |
| `GetThread(MakeGetThreadInfoRequest(…))` | short | all three IDs; `folder` is `""` (§7) | yes |
| `PaginateMessages` (`ThreadID`) | short | messages, **newest first** | yes |
| `SendMessage` (`IGThreadIGID`) | **short** (long also works) | message ID | yes, 1:1 and group |
| `SendMessage` to a new user | `RecipientIGIDs = [<fbid>]`, `IGThreadIGID` nil | message ID | yes (§6) |
| `MarkRead` + `MarkReadValidation` | long in `Metadata.IGThreadIGID`, newest message ID in `Data.MessageID` | — | yes, cleared on the phone |
| `FetchThreadID(ctx, int64)` | the other user's FBID (= 1:1 key) | `{LongID, ShortID}` or `ErrThreadNotFound` | yes for 1:1; not tried with a group key |
| `GetUserForNewDM(ctx, fbid)` | FBID | user info | yes |
| `SetTyping(ctx, id, bool)` | short (per upstream) | — | no (Phase 2) |
| `SendReaction`, `EditMessage`, `UnsendMessage` | — | — | no (Phase 2) |

**We send with the short ID.** Upstream does the same, and the delta and history paths already use the short ID. The long ID is needed only for `MarkRead` and to recognise incoming typing.

`RecipientIGIDs` takes an FBID despite its name.

## 4. Which ID each event carries

Every delta has `__typename`, `uq_seq_id` and `thread_fbid` (the short ID). In a `NewMessage`, the envelope `thread_fbid` and `Message.ThreadFBID` were always equal. Fall back to the message's value if the envelope is empty.

| Event | Thread | Other fields |
|---|---|---|
| `SlideUQPPNewMessage` | short | `id` = `message_id`; `sender_fbid`; `offline_threading_id` matches the OTID we sent; `read_effect` is `MARK_READ` on own messages and `MARK_UNREAD` on others' |
| `SlideUQPPCreateReaction` | short | `message_id`; `reaction.sender_fbid` = FBID |
| `SlideUQPPDeleteReaction` | short | `message_id`; **`reaction.sender_fbid` is 0**. Upstream then looks up the reaction by `reaction.log_message_id`, saved when the `CreateReaction` arrived. Store it with each reaction |
| `SlideUQPPEditMessage` | short | `message_id`, new text |
| `SlideUQPPDeleteMessage` | short | `message_id` |
| `SlideUQPPMarkRead` | short | `read_timestamp_ms` (the viewer read the thread on another device) |
| `SlideUQPPReadReceipt` | short | `participant_fbid` = FBID; `watermark_timestamp_ms` |
| `SlideUQPPThreadName` | short | `UpdateThreadNameEvent` |
| `SlideUQPPCreate1To1Thread`, `SlideUQPPUserReachabilityStatus`, `SlideUQPPNotYetImplementedMutation` | **empty** | `IgnoredEvent`, no IDs |
| `TypingNotification` (not a delta) | **long** | `sender_id` = **IGID**; `activity_status` 1 = typing, 0 = stopped |
| `Connected` (not a delta) | — | `subscribed_seq`, `latest_seq` |

- Edit, delete and read receipts were seen only in a group. 1:1s use the same delta types.
- A thread rename produced `ThreadName` followed by a `NotYetImplementedMutation`; the second one's meaning is unknown.

## 5. Sequence IDs and delivery

- Every delta carries a `uq_seq_id`. The seqs were contiguous; ignored deltas use up seqs too.
- **`SeqIDUpdate` follows each batch, not each delta.** Live, a batch was usually one delta. On connect, the catch-up of six missed deltas arrived as one batch with one `SeqIDUpdate`. A rename came as two deltas and one `SeqIDUpdate`.
- **Catch-up works.** If `Connected` has `subscribed_seq` < `latest_seq`, the missed deltas arrive first.
- **Delivery is at-least-once.** A client that exits before saving the `SeqIDUpdate` gets those deltas again on the next connect. The server also repeats some events: during catch-up each `ReadReceipt` arrived twice (same participant, same watermark), and `MarkRead` arrived twice. **Applying any delta twice must be harmless**: upsert by message ID, keep the highest watermark, match echoes by OTID.

## 6. New DM to someone with no 1:1 thread

This was verified with a user who shares a group with the test account.

1. `FetchThreadID(<fbid>)` → `ErrThreadNotFound`. This is not an error: show an empty chat.
2. `GetUserForNewDM(<fbid>)` gives the header info.
3. `SendMessage` with `RecipientIGIDs = [<fbid>]`, no `IGThreadIGID`, and a fresh OTID. The message arrived.
4. Deltas: `Create1To1Thread` (ignored, no IDs), then `UserReachabilityStatus` (ignored), then `NewMessage` with a **short ID we haven't seen before**.
5. `FetchThreadID(<fbid>)` now returns the long ID and that short ID. The key is `<fbid>`.

For a new DM, call `FetchThreadID(<fbid>)` after the first send to learn the thread's IDs. For any other delta with an unknown short ID, call `GetThread(short)` before applying it (AGENTS §6.3).

## 7. Messages, folders and other details

- Message IDs look like `mid.$<base64-ish>`. `id` and `message_id` are equal. The `SendMessage` response ID equals the echo's ID.
- OTIDs are 19-digit numeric strings (`methods.GenerateEpochID()`).
- **History contains `REACTION_LOG_XMAT` entries.** They have no text, `otid=262` and IDs that end in a run of `A`s. Don't render them as chat lines. Marking one as read still worked.
- **`IGDSnippet` is filled only on live deltas** (`"You: <text>"` or `"<name>: <text>"`). It is empty in history, so the fallback for non-text messages must be `[ContentType]`.
- Content types seen: `TEXT`, `REACTION_LOG_XMAT`, `CUTOUT_STICKER_XMA`.
- Live messages have an empty GraphQL `__typename`.
- Folders: the mailbox gives `folder="PRIMARY"` and `system_folder="INBOX"`, but `GetThread` gives `folder=""`. Don't overwrite a stored folder with `""` from `GetThread`.

## 8. Resume

All three paths in AGENTS §6.5 worked:
- **fast:** saved state and seq ID, then `Connect`;
- **warm:** seq ID only, then `ReloadIndex`;
- **cold:** nothing saved, then `LoadIndex`.

## 9. Not verified

- **End-to-end encrypted chats:** the test phone offered no way to start one. Upstream doesn't support them (AGENTS §15). If a thread with an unfamiliar subtype or `input_mode` shows up, label it instead of failing.
- **Message requests:** `PaginateMailbox` for folder `PENDING` returned nothing because no request existed. Revisit in Phase 2 with an account the test account doesn't follow.
- Outgoing typing, reactions, edits and unsends (Phase 2).
- Edits, unsends and read receipts in a 1:1.
- `FetchThreadID` with a group key.
- Instagram's message length limit (ROADMAP §18).

## 10. What this means for `ids.go`

- `core.ThreadID` is the key (int64): the other user's FBID for a 1:1, or the short ID parsed as int64 for a group.
- Keep `key ↔ short ↔ long` for every thread from mailbox, thread and `FetchThreadID` responses. Typing needs the **long → key** direction.
- Keep **IGID → FBID** for users (typing).
- A delta with an empty `thread_fbid` is either ignored or carries no thread. Don't look it up.
