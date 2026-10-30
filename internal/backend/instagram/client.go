// termeownal — a terminal client for Instagram DMs.
// Copyright (C) 2026 ericliswan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/util/exhttp"
	"go.mau.fi/util/jsontime"

	"termeownal/internal/core"
)

// queueSize is how many events wait for the app before the event handler
// blocks.
const queueSize = 64

// errClosed is returned by calls made after Close.
var errClosed = errors.New("instagram: backend closed")

// upstream is the part of instameow.Client the backend uses, so tests can
// stand in for Instagram.
type upstream interface {
	LoadState(json.RawMessage) error
	HasSeqID() bool
	LoadIndex(context.Context) (*types.PolarisViewer, *slidetypes.Mailbox, error)
	ReloadIndex(context.Context) (bool, error)
	GetMailbox(context.Context) (*slidetypes.MailboxResponse, error)
	GetOwnFBID() int64
	GetCookies() *cookies.Cookies
	GetThread(context.Context, *slidetypes.GetThreadInfoRequest) (*slidetypes.ThreadInfoResponse, error)
	FetchThreadID(context.Context, int64) (*instameow.ThreadIGIDs, error)
	PaginateMessages(context.Context, *slidetypes.PaginateMessagesRequest) (*slidetypes.PaginateMessagesResponse, error)
	SendMessage(context.Context, *slidetypes.SendTextRequest) (*slidetypes.SendTextResponse, error)
	MarkRead(context.Context, *slidetypes.MarkReadRequest) (*slidetypes.MarkReadResponse, error)
	MarkReadValidation(context.Context, *slidetypes.MarkReadRequest) (*slidetypes.MarkReadValidationResponse, error)
	Connect(context.Context)
	Disconnect()
}

var _ upstream = (*instameow.Client)(nil)

// Backend is the Instagram core.Backend.
type Backend struct {
	log       zerolog.Logger
	newClient func(instameow.ClientParams) upstream
	ids       idMap
	events    chan core.Event

	// ctx is cancelled by Close; wg tracks Connect and event handler calls
	// so Close can wait for them before closing events.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	client upstream // nil until Restore
	// state is the client's saved configs, nil if it has none to resume
	// from; stateDirty means it changed since ExportAuth.
	state      json.RawMessage
	stateDirty bool
	// exported holds the cookies ExportAuth last returned.
	exported map[cookies.MetaCookieName]string
	// pending holds events from Snapshot, which can't send them itself;
	// Connect sends them first.
	pending []core.Event
	// resync is set when Instagram asks for a new snapshot, authErr once the
	// login stops working. stopConnect ends the running Connect.
	resync      bool
	authErr     error
	stopConnect context.CancelFunc
}

var _ core.Backend = (*Backend)(nil)

// New returns a Backend that logs to log. Call Restore before using it.
func New(log zerolog.Logger) *Backend {
	ctx, cancel := context.WithCancel(context.Background())
	return &Backend{
		log:       log,
		newClient: func(p instameow.ClientParams) upstream { return instameow.NewClient(p) },
		events:    make(chan core.Event, queueSize),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Restore builds the client from a saved session. It makes no network calls;
// Snapshot does, so the cache shows at once.
func (b *Backend) Restore(_ context.Context, s core.Session) error {
	var auth authData
	if json.Unmarshal(s.Auth, &auth) != nil {
		return fmt.Errorf("restore: unreadable login: %w", core.ErrSessionExpired)
	}
	c := &cookies.Cookies{Platform: types.Instagram}
	c.UpdateValues(auth.Cookies)
	if !c.IsLoggedIn() {
		return fmt.Errorf("restore: no session cookie: %w", core.ErrSessionExpired)
	}
	client := b.build(c, s.SeqID, s.SeqAt)
	var state json.RawMessage
	if len(auth.State) > 0 {
		if err := client.LoadState(auth.State); err != nil {
			// Usually over a day old; Snapshot reloads it.
			b.log.Info().Bool("too_old", errors.Is(err, instameow.ErrCachedStateTooOld)).Msg("saved client state not used")
		} else {
			state = auth.State
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errClosed
	}
	b.client, b.state, b.exported = client, state, auth.Cookies
	return nil
}

// build returns a client that resumes events after seq ID seq.
func (b *Backend) build(c *cookies.Cookies, seq int64, at time.Time) upstream {
	return b.newClient(instameow.ClientParams{
		Cookies:      c,
		Log:          b.log,
		Settings:     exhttp.SensibleClientSettings,
		SeqID:        seq,
		SeqIDTS:      at,
		EventHandler: b.handle,
		// Typing arrives in Phase 2.
		DisableTyping: true,
	})
}

// Snapshot loads the inbox, resuming as the session allows (AGENTS.md
// §6.5). If Instagram rejects the saved state, it starts cold.
func (b *Backend) Snapshot(ctx context.Context) (core.User, []core.Thread, error) {
	me, threads, err := b.snapshot(ctx)
	if errors.Is(err, types.ErrPleaseReloadPage) {
		b.log.Info().Msg("saved state rejected; starting cold")
		if err = b.reset(); err == nil {
			me, threads, err = b.snapshot(ctx)
		}
	}
	if err != nil {
		return core.User{}, nil, fmt.Errorf("load inbox: %w", coreError(err))
	}
	return me, threads, nil
}

// snapshot loads the inbox: fast with the saved state, warm by reloading
// the state, or cold, which also resets the seq ID to the inbox's.
func (b *Backend) snapshot(ctx context.Context) (core.User, []core.Thread, error) {
	client, err := b.current()
	if err != nil {
		return core.User{}, nil, err
	}
	b.mu.Lock()
	hasState := b.state != nil
	b.mu.Unlock()

	var me core.User
	var mailbox *slidetypes.Mailbox
	if !client.HasSeqID() {
		b.log.Info().Msg("cold start")
		viewer, mb, err := client.LoadIndex(ctx)
		if err != nil {
			return core.User{}, nil, err
		}
		me.Username, me.Name, mailbox = viewer.GetUsername(), viewer.GetName(), mb
		// Live events continue from the inbox's seq ID; Connect sends this
		// once the snapshot is stored.
		b.mu.Lock()
		b.pending = append(b.pending, core.SeqAdvanced{SeqID: mb.UQSeqID, At: time.Now()})
		b.mu.Unlock()
	} else {
		if !hasState {
			b.log.Info().Msg("warm start")
			if _, err := client.ReloadIndex(ctx); err != nil {
				return core.User{}, nil, err
			}
		}
		resp, err := client.GetMailbox(ctx)
		if err != nil {
			return core.User{}, nil, err
		}
		if mailbox = resp.GetMailbox(); mailbox == nil {
			return core.User{}, nil, instameow.ErrMailboxMissing
		}
	}

	me.ID = core.UserID(client.GetOwnFBID())
	if me.ID == 0 {
		return core.User{}, nil, errors.New("no account ID from Instagram")
	}
	var threads []core.Thread
	for _, e := range mailbox.ThreadsByFolder.Edges {
		t := e.Node.AsIGDirectThread
		if t == nil {
			continue
		}
		b.ids.add(core.ThreadID(t.ThreadKey), idsOf(t))
		if me.Username == "" && t.Viewer != nil {
			me.Username, me.Name = t.Viewer.Username, t.Viewer.FullName
		}
		threads = append(threads, convertThread(t, me.ID, b.log))
	}
	// shortcut: a warm or fast start with no threads has no username, so the
	// cached one is blanked; keep the stored one if anyone has an empty inbox.
	return me, threads, nil
}

// reset replaces the client with one without saved state or seq ID, so the
// next snapshot starts cold.
func (b *Backend) reset() error {
	b.mu.Lock()
	if err := b.readyLocked(); err != nil {
		b.mu.Unlock()
		return err
	}
	old := b.client
	b.client = b.build(old.GetCookies(), 0, time.Time{})
	b.state = nil
	b.mu.Unlock()
	old.Disconnect()
	return nil
}

// History returns up to n messages older than before, oldest first.
func (b *Backend) History(ctx context.Context, thread core.ThreadID, before core.MessageID, n int) ([]core.Message, bool, error) {
	client, err := b.current()
	if err != nil {
		return nil, false, err
	}
	ids, err := b.idsFor(ctx, client, thread)
	if err != nil {
		return nil, false, err
	}
	req := &slidetypes.PaginateMessagesRequest{ThreadID: ids.short, FirstN: n, InitialMessagePageCount: n}
	if before != "" {
		req.OlderThanMessageID = new(string(before))
	}
	resp, err := client.PaginateMessages(ctx, req)
	if err != nil {
		return nil, false, fmt.Errorf("load messages: %w", coreError(err))
	}
	t := resp.ThreadInfo.AsIGDirectThread
	if t == nil || t.Messages == nil {
		return nil, false, nil
	}
	// shortcut: a page of only reaction log entries comes back empty with
	// more set, so paging stops there; page past them if that shows up.
	return convertMessages(t.Messages, thread, b.log), t.Messages.PageInfo.HasNextPage, nil
}

// NewOTID returns a fresh offline threading ID.
func (*Backend) NewOTID() string {
	return strconv.FormatInt(methods.GenerateEpochID(), 10)
}

// Send sends text to a thread. Its echo arrives as a MessageAdded.
func (b *Backend) Send(ctx context.Context, thread core.ThreadID, text, otid string) error {
	client, err := b.current()
	if err != nil {
		return err
	}
	ids, err := b.idsFor(ctx, client, thread)
	if err != nil {
		return err
	}
	_, err = client.SendMessage(ctx, &slidetypes.SendTextRequest{
		IGThreadIGID:       &ids.short,
		OfflineThreadingID: otid,
		Text:               slidetypes.SensitiveString{Value: text},
		SendAttribution:    new("igd_web_chat_tab:in_thread"),
	})
	if err != nil {
		return fmt.Errorf("send: %w", coreError(err))
	}
	return nil
}

// MarkRead marks upTo's thread read up to upTo, in the two steps the web
// client uses.
func (b *Backend) MarkRead(ctx context.Context, upTo core.Message) error {
	client, err := b.current()
	if err != nil {
		return err
	}
	ids, err := b.idsFor(ctx, client, upTo.Thread)
	if err != nil {
		return err
	}
	meta := slidetypes.MarkReadMetadata{IGThreadIGID: ids.long}
	if _, err := client.MarkRead(ctx, &slidetypes.MarkReadRequest{
		Metadata: meta,
		Data:     slidetypes.MarkReadData{MessageID: string(upTo.ID), ItemID: new("")},
	}); err != nil {
		return fmt.Errorf("mark read: %w", coreError(err))
	}
	if _, err := client.MarkReadValidation(ctx, &slidetypes.MarkReadRequest{
		Metadata: meta,
		Data:     slidetypes.MarkReadData{MessageID: string(upTo.ID), MessageTimestampMS: jsontime.UnixMilliString{Time: upTo.At}},
	}); err != nil {
		return fmt.Errorf("mark read: %w", coreError(err))
	}
	return nil
}

// idsFor returns a thread's IDs, looking them up for a thread not seen since
// the start (docs/ids.md): a 1:1's key is the other user's FBID, a group's
// is its short ID.
func (b *Backend) idsFor(ctx context.Context, client upstream, key core.ThreadID) (threadIDs, error) {
	if ids, ok := b.ids.get(key); ok {
		return ids, nil
	}
	got, err := client.FetchThreadID(ctx, int64(key))
	if err == nil {
		b.ids.add(key, threadIDs{short: got.ShortID, long: got.LongID})
	} else if ctx.Err() == nil {
		if t, gerr := b.fetchThread(ctx, client, strconv.FormatInt(int64(key), 10)); gerr != nil || t == nil {
			b.log.Debug().AnErr("group_lookup", gerr).Msg("no group with that key either")
		}
	}
	if ids, ok := b.ids.get(key); ok {
		return ids, nil
	}
	if err == nil {
		err = errors.New("no thread IDs from Instagram")
	}
	return threadIDs{}, fmt.Errorf("look up thread %d: %w", key, coreError(err))
}

// fetchThread fetches a thread by short ID and records its IDs. It returns
// nil if the result isn't a thread termeownal can show.
func (b *Backend) fetchThread(ctx context.Context, client upstream, short string) (*slidetypes.ThreadInfo, error) {
	resp, err := client.GetThread(ctx, slidetypes.MakeGetThreadInfoRequest(short))
	if err != nil {
		return nil, fmt.Errorf("get thread: %w", coreError(err))
	}
	t := resp.ThreadInfo.AsIGDirectThread
	if t != nil {
		b.ids.add(core.ThreadID(t.ThreadKey), idsOf(t))
	}
	return t, nil
}

// Connect runs the live connection; upstream reconnects on its own. It
// returns when ctx is done, Close is called, the login stops working, or
// Instagram asks for a new snapshot, after sending ResyncNeeded.
func (b *Backend) Connect(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b.mu.Lock()
	err := b.readyLocked()
	if err == nil {
		err = b.authErr
	}
	client, pending := b.client, b.pending
	if err == nil {
		b.wg.Add(1)
		b.pending, b.resync, b.stopConnect = nil, false, cancel
	}
	b.mu.Unlock()
	if err != nil {
		return err
	}
	defer b.wg.Done()
	defer context.AfterFunc(b.ctx, cancel)()

	if b.send(ctx, pending...) == nil && b.checkSession(ctx) == nil {
		client.Connect(ctx)
	}
	return b.stopped(ctx)
}

// stopped says why Connect stopped, sending ResyncNeeded if that's why.
func (b *Backend) stopped(ctx context.Context) error {
	b.mu.Lock()
	closed, authErr, resync := b.closed, b.authErr, b.resync
	b.stopConnect, b.resync = nil, false
	b.mu.Unlock()
	switch {
	case closed:
		return nil
	case authErr != nil:
		return authErr
	case ctx.Err() != nil:
		return ctx.Err()
	case resync:
		return b.send(ctx, core.ResyncNeeded{})
	}
	return errors.New("connection closed")
}

// Events returns the event channel. Close closes it.
func (b *Backend) Events() <-chan core.Event { return b.events }

// ExportAuth returns the cookies and client state to save, even after Close.
func (b *Backend) ExportAuth() (core.AuthData, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		return nil, fmt.Errorf("instagram: not restored: %w", core.ErrSessionExpired)
	}
	values := b.client.GetCookies().GetAll()
	auth, err := json.Marshal(authData{Cookies: values, State: b.state})
	if err != nil {
		return nil, fmt.Errorf("encode login: %w", err)
	}
	b.exported, b.stateDirty = values, false
	return auth, nil
}

// Close disconnects, waits for the event handler, and closes Events.
func (b *Backend) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	client := b.client
	b.mu.Unlock()

	b.cancel()
	if client != nil {
		client.Disconnect()
	}
	b.wg.Wait()
	close(b.events)
}

// current returns the client, or why it can't be used.
func (b *Backend) current() (upstream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.readyLocked(); err != nil {
		return nil, err
	}
	return b.client, nil
}

// readyLocked reports why the backend can't be used, if it can't.
func (b *Backend) readyLocked() error {
	switch {
	case b.closed:
		return errClosed
	case b.client == nil:
		return fmt.Errorf("instagram: not restored: %w", core.ErrSessionExpired)
	}
	return nil
}
