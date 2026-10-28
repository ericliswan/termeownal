<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner-dark.svg">
    <img alt="termeownal: Instagram DMs, in your terminal" src="assets/banner-light.svg" width="100%">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/ericliswan/termeownal/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/ericliswan/termeownal/actions/workflows/ci.yml/badge.svg"></a>
  <img alt="Go 1.26+" src="https://img.shields.io/badge/go-1.26%2B-F5F5F5?logo=go&logoColor=white&labelColor=0A0A0A">
  <a href="LICENSE"><img alt="License: AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-F5F5F5?labelColor=0A0A0A"></a>
  <a href="ROADMAP.md#14-milestones"><img alt="Status: early development" src="https://img.shields.io/badge/status-early%20development-F5F5F5?labelColor=0A0A0A"></a>
</p>

<p align="center">
  <a href="#preview">Preview</a> ·
  <a href="#planned-features">Features</a> ·
  <a href="#building">Building</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="ROADMAP.md">Roadmap</a>
</p>

Instagram DMs in your terminal. Log in once, see your threads, read history, send messages and get new ones live, all from one Go binary called **`meow`**.

termeownal talks to Instagram through [`instameow`](https://github.com/mautrix/meta/tree/main/pkg/instameow), the Instagram client library inside the [mautrix/meta](https://github.com/mautrix/meta) bridge. It doesn't run the bridge, Matrix or any server.

> **Status: early development.** termeownal can't send or receive messages yet. The proof-of-concept against Instagram is done (see [`docs/ids.md`](docs/ids.md)), and the MVP is being built now. Progress is tracked in [`ROADMAP.md`](ROADMAP.md#14-milestones).

## Preview

<p align="center">
  <img alt="termeownal with a group chat open: the thread list on the left, messages on the right and the composer below" src="assets/preview.svg" width="880">
</p>
<p align="center"><sub>The UI so far, running with <code>--backend=fake</code>: canned chats, no network.</sub></p>

<p align="center">
  <img alt="termeownal's thread list, with the chat pane waiting for a thread to be opened" src="assets/screenshots/threads.svg" width="49%">
  <img alt="termeownal's help overlay listing the keyboard shortcuts" src="assets/screenshots/help.svg" width="49%">
</p>
<p align="center"><sub>The thread list, and the key help you get with <code>?</code></sub></p>

<!--
  Demo GIF: once the app is further along, run `make demo` (needs vhs) to
  record docs/demo.tape into assets/demo.gif, then swap the image above for:

  <img alt="termeownal demo" src="assets/demo.gif" width="880">
-->

<p align="center"><img alt="" src="assets/divider.svg" width="100%"></p>

## ⚠️ Read this first

- **This is an unofficial client.** Instagram's terms don't allow third-party clients. Using one can get your account flagged, checkpointed or locked. Try it with a secondary account first, at your own risk.
- **End-to-end encrypted chats aren't supported.** The upstream library can't read them.
- termeownal isn't affiliated with or endorsed by Instagram or Meta.

## Planned features

**MVP (in progress)**
- A thread list, a chat view and a composer, with Vim-style keys
- Live incoming messages, unread markers, and mark-as-read when you open a thread
- Sending that shows your message right away and offers a retry if it fails
- Instant startup from a local cache, with automatic reconnects
- Cookie login (`meow login`) and `meow logout`

**Later:** replies, reactions, edits and unsends, typing indicators, read receipts, new DMs and message requests, media (opened externally or shown inline on kitty, iTerm2 or sixel), notifications, themes, and an optional password login.

## Building

You need Go 1.26 or newer. termeownal is pure Go, so cgo isn't needed.

```sh
git clone https://github.com/ericliswan/termeownal.git
cd termeownal
make static        # builds bin/meow
```

There's no Instagram login yet, but you can try the UI with canned chats and no network:

```sh
./bin/meow --backend=fake
```

Prebuilt releases and a Homebrew tap are planned for later.

## Where your data lives

| What | Where (macOS) | Notes |
|---|---|---|
| Settings | `~/Library/Application Support/termeownal/config.toml` | Optional |
| Log | `~/Library/Application Support/termeownal/meow.log` | Readable only by you; never holds passwords, cookies or message text |
| Session | `session.enc` in the same folder | Encrypted; the key is kept in your OS keyring |
| Message cache | the same folder | **Plaintext** on disk, so it can be deleted and fetched again |

On Linux these follow the XDG base directories (`~/.config`, `~/.local/share`, `~/.local/state`). You can move them with the `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME` environment variables.

termeownal never takes passwords or cookies from command-line flags or environment variables. It reads them from the terminal with echo turned off.

## Configuration

Every setting is optional. An unknown key or a bad value stops `meow` with an error that names the file.

```toml
# debug, info, warn or error
log_level = "info"

# How often to do a full reconnect. "0" turns it off; otherwise at least "1h".
force_refresh_interval = "20h"
```

## Development

| Task | Command |
|---|---|
| Every check run before a commit (gofmt, vet, race tests, lint) | `make check` |
| Run the tests | `go test ./...` |
| Lint | `golangci-lint run` |
| Record the README demo GIF (needs [vhs](https://github.com/charmbracelet/vhs)) | `make demo` |

The tests never contact Instagram. The architecture and contributor rules are in [`AGENTS.md`](AGENTS.md); the full plan and the reasoning behind it are in [`ROADMAP.md`](ROADMAP.md).

<p align="center"><img alt="" src="assets/divider.svg" width="100%"></p>

## Credits

termeownal is built on [mautrix/meta](https://github.com/mautrix/meta) by Tulir Asokan and its contributors. The terminal UI will use [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## License

[AGPL-3.0](LICENSE). termeownal imports AGPL-licensed code from mautrix/meta, so it uses the same license.

<p align="center"><img alt="" src="assets/logo.svg" width="72"></p>
