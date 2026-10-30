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

// Package fake is a deterministic core.Backend with canned threads, for
// running and testing termeownal without Instagram.
package fake

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"termeownal/internal/core"
)

// queueSize matches the buffered event queue of the real backend.
const queueSize = 64

// snapshotMessages is how many recent messages Snapshot returns per thread.
const snapshotMessages = 20

// errClosed is returned by calls made after Close.
var errClosed = errors.New("fake: backend closed")

// Options configures a Backend. The zero value is valid.
type Options struct {
	// Now is the clock. Canned history ends at Now() when the Backend is
	// created. Defaults to time.Now; set it for reproducible timestamps.
	Now func() time.Time
	// IncomingEvery is how often, while connected, someone sends a message.
	// Zero disables incoming messages.
	IncomingEvery time.Duration
	// EchoDelay is how long the echo of a sent message takes to arrive.
	EchoDelay time.Duration
}

// Backend is a fake core.Backend. It is safe for concurrent use.
type Backend struct {
	opts   Options
	events chan core.Event

	// ctx is cancelled by Close; wg tracks every goroutine that emits events
	// so Close can wait for them before closing the channel.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// emitMu serialises event batches so a SeqAdvanced always follows its
	// own delta and sequence IDs reach the channel in increasing order.
	emitMu sync.Mutex
	seq    int64

	mu       sync.Mutex
	closed   bool
	auth     core.AuthData // nil until Restore
	threads  []*seedThread
	sentOTID map[string]bool
	nextOTID int64
	nextMID  int64
	rng      *rand.Rand
}

var _ core.Backend = (*Backend)(nil)

// New returns a Backend loaded with the canned threads.
func New(opts Options) *Backend {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Backend{
		opts:     opts,
		events:   make(chan core.Event, queueSize),
		ctx:      ctx,
		cancel:   cancel,
		threads:  seedData(opts.Now()),
		sentOTID: make(map[string]bool),
		rng:      rand.New(rand.NewPCG(3, 4)),
	}
}

// Restore accepts a session whose auth came from Authenticator.
func (b *Backend) Restore(_ context.Context, s core.Session) error {
	if !strings.HasPrefix(string(s.Auth), authPrefix) {
		return fmt.Errorf("fake: restore: %w", core.ErrSessionExpired)
	}
	b.mu.Lock()
	b.auth = slices.Clone(s.Auth)
	b.mu.Unlock()
	b.emitMu.Lock()
	b.seq = s.SeqID
	b.emitMu.Unlock()
	return nil
}

// Snapshot returns the logged-in user and every thread, most recent first,
// each with its newest messages.
func (b *Backend) Snapshot(context.Context) (core.User, []core.Thread, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.readyLocked(); err != nil {
		return core.User{}, nil, err
	}
	threads := make([]core.Thread, len(b.threads))
	for i, t := range b.threads {
		threads[i] = t.Thread
		threads[i].Members = slices.Clone(t.Members)
		threads[i].Messages = slices.Clone(t.history[max(0, len(t.history)-snapshotMessages):])
	}
	slices.SortStableFunc(threads, func(a, b core.Thread) int { return b.LastActivity.Compare(a.LastActivity) })
	return me, threads, nil
}

// History pages backwards through a thread's messages.
func (b *Backend) History(_ context.Context, thread core.ThreadID, before core.MessageID, n int) ([]core.Message, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := b.threadLocked(thread)
	if err != nil {
		return nil, false, err
	}
	end := len(t.history)
	if before != "" {
		end = slices.IndexFunc(t.history, func(m core.Message) bool { return m.ID == before })
		if end < 0 {
			return nil, false, fmt.Errorf("fake: history: unknown message %q", before)
		}
	}
	start := max(0, end-n)
	return slices.Clone(t.history[start:end]), start > 0, nil
}

// NewOTID returns a fresh offline threading ID.
func (b *Backend) NewOTID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextOTID++
	return fmt.Sprintf("fake-otid-%d", b.nextOTID)
}

// Send queues the echo of text after EchoDelay. A repeated OTID is accepted
// but not sent again.
func (b *Backend) Send(_ context.Context, thread core.ThreadID, text, otid string) error {
	if text == "" || otid == "" {
		return errors.New("fake: send: empty text or OTID")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.threadLocked(thread); err != nil {
		return err
	}
	if b.sentOTID[otid] {
		return nil
	}
	b.sentOTID[otid] = true
	return b.goLocked(func() {
		select {
		case <-time.After(b.opts.EchoDelay):
		case <-b.ctx.Done():
			return
		}
		if m, err := b.addMessage(thread, me.ID, text, otid); err == nil {
			_ = b.emitDelta(b.ctx, core.MessageAdded{Message: m})
		}
	})
}

// MarkRead clears the thread's unread flag.
func (b *Backend) MarkRead(_ context.Context, upTo core.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := b.threadLocked(upTo.Thread)
	if err != nil {
		return err
	}
	t.Unread = false
	return nil
}

// Connect reports Connected, then delivers incoming messages every
// IncomingEvery until ctx is cancelled or Close is called.
func (b *Backend) Connect(ctx context.Context) error {
	b.mu.Lock()
	err := b.readyLocked()
	if err == nil {
		b.wg.Add(1)
	}
	b.mu.Unlock()
	if err != nil {
		return err
	}
	defer b.wg.Done()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(b.ctx, cancel)()

	if err := b.emit(ctx, core.StatusChanged{Status: core.Connected}); err != nil {
		return b.connectErr(ctx)
	}
	if b.opts.IncomingEvery <= 0 {
		<-ctx.Done()
		return b.connectErr(ctx)
	}
	tick := time.NewTicker(b.opts.IncomingEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			m, err := b.incoming()
			if err == nil {
				err = b.emitDelta(ctx, core.MessageAdded{Message: m})
			}
			if err != nil {
				return b.connectErr(ctx)
			}
		case <-ctx.Done():
			return b.connectErr(ctx)
		}
	}
}

// connectErr is Connect's result once ctx is done: nil after Close.
func (b *Backend) connectErr(ctx context.Context) error {
	if b.ctx.Err() != nil {
		return nil
	}
	return ctx.Err()
}

// Events returns the event channel. Close closes it.
func (b *Backend) Events() <-chan core.Event { return b.events }

// ExportAuth returns the auth passed to Restore, even after Close.
func (b *Backend) ExportAuth() (core.AuthData, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.auth == nil {
		return nil, fmt.Errorf("fake: not restored: %w", core.ErrSessionExpired)
	}
	return slices.Clone(b.auth), nil
}

// Close stops all activity, waits for it, and closes the Events channel.
func (b *Backend) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	b.cancel()
	b.wg.Wait()
	close(b.events)
}

// readyLocked reports why the backend can't be used, if it can't.
func (b *Backend) readyLocked() error {
	switch {
	case b.closed:
		return errClosed
	case b.auth == nil:
		return fmt.Errorf("fake: not restored: %w", core.ErrSessionExpired)
	}
	return nil
}

// threadLocked finds a thread, checking that the backend is ready first.
func (b *Backend) threadLocked(id core.ThreadID) (*seedThread, error) {
	if err := b.readyLocked(); err != nil {
		return nil, err
	}
	for _, t := range b.threads {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("fake: unknown thread %d", id)
}

// goLocked runs f in a goroutine that Close waits for.
func (b *Backend) goLocked(f func()) error {
	if b.closed {
		return errClosed
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
	return nil
}

// incoming adds a message from someone else to a random thread.
func (b *Backend) incoming() (core.Message, error) {
	b.mu.Lock()
	t := b.threads[b.rng.IntN(len(b.threads))]
	sender := t.Members[b.rng.IntN(len(t.Members))].ID
	text := lines[b.rng.IntN(len(lines))]
	b.mu.Unlock()
	return b.addMessage(t.ID, sender, text, "")
}

// addMessage appends a new message to a thread's history.
func (b *Backend) addMessage(thread core.ThreadID, sender core.UserID, text, otid string) (core.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := b.threadLocked(thread)
	if err != nil {
		return core.Message{}, err
	}
	b.nextMID++
	m := core.Message{
		ID:          core.MessageID(fmt.Sprintf("mid.$fakelive.%d", b.nextMID)),
		Thread:      thread,
		Sender:      sender,
		At:          b.opts.Now(),
		Text:        text,
		ContentType: contentText,
		OTID:        otid,
	}
	t.history = append(t.history, m)
	t.LastActivity = m.At
	if sender != me.ID {
		t.Unread = true
	}
	return m, nil
}

// emitDelta sends ev followed by its SeqAdvanced, as upstream does for a
// one-delta batch.
func (b *Backend) emitDelta(ctx context.Context, ev core.Event) error {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	if err := b.sendLocked(ctx, ev); err != nil {
		return err
	}
	b.seq++
	return b.sendLocked(ctx, core.SeqAdvanced{SeqID: b.seq, At: b.opts.Now()})
}

// emit sends one event that has no sequence ID.
func (b *Backend) emit(ctx context.Context, ev core.Event) error {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()
	return b.sendLocked(ctx, ev)
}

// sendLocked blocks until ev is queued or ctx is done; it never drops ev.
func (b *Backend) sendLocked(ctx context.Context, ev core.Event) error {
	select {
	case b.events <- ev:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
