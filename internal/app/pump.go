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

package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"termeownal/internal/core"
	"termeownal/internal/store"
	"termeownal/internal/ui"
)

// snapshotMessages is how many of each thread's newest messages the cache
// shows at start.
const snapshotMessages = 20

// uiSender delivers messages to the UI: a *tea.Program, or a recorder in
// tests.
type uiSender interface{ Send(tea.Msg) }

// pump is the only writer of live events. It stores each event, then hands
// it to the UI, in the order the backend sent them.
type pump struct {
	b   core.Backend
	st  *store.Store
	ui  uiSender
	log zerolog.Logger

	// session is the session to save: the restored one, then updated by
	// SeqAdvanced and SessionChanged. save saves it; nil saves nothing.
	session core.Session
	save    func(core.Session) error
	// connect asks connectLoop to connect. sync sends on it once a snapshot
	// is stored, so the events that follow apply on top of the snapshot.
	connect chan struct{}
	// failed is set once a write fails. No seq ID is saved after that, so the
	// next start replays what the cache missed.
	failed bool
	// wait is how long to wait before retry n, from 0.
	wait func(n int) time.Duration
	// me is the account as last cached or snapshotted, for a snapshot that
	// doesn't know the username.
	me core.User
}

func newPump(b core.Backend, st *store.Store, to uiSender, log zerolog.Logger) *pump {
	return &pump{b: b, st: st, ui: to, log: log, connect: make(chan struct{}, 1), wait: backoff}
}

// maxBackoff is the longest wait between retries.
const maxBackoff = time.Minute

// backoff is the wait before retry n: 1 s doubling to a minute, less up to a
// fifth at random so many clients don't retry in step.
func backoff(n int) time.Duration {
	d := min(time.Second<<min(n, 6), maxBackoff)
	return d - rand.N(d/5)
}

// sleep waits for d, or returns false once ctx is done.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// run shows the cache, then stores and shows the backend's snapshot, then
// applies events until the backend closes its Events channel.
func (p *pump) run(ctx context.Context) {
	// Nothing can be sending yet, so a message still Sending was cut off by
	// the last exit. The UI's first send waits for the snapshot below.
	if err := p.st.FailUnsent(ctx); err != nil {
		p.report(ctx, err)
	}
	// The cache belongs to this account: meow login clears another
	// account's with store.SetMe.
	if me, threads, err := p.st.Snapshot(ctx, snapshotMessages); err != nil {
		p.report(ctx, err)
	} else {
		p.me = me
		p.ui.Send(ui.SnapshotMsg{Me: me, Threads: threads})
	}
	p.sync(ctx)
	for ev := range p.b.Events() {
		if ev := p.apply(ctx, ev); ev != nil {
			p.ui.Send(ev)
		}
	}
	// Login data can change without a SessionChanged, so save it once more.
	if err := p.saveAuth(); err != nil {
		p.log.Warn().Err(err).Msg("final session save failed")
	}
}

// sync stores and shows a snapshot from the backend, then asks for a
// connection, which delivers every event since the saved seq ID. A failed
// snapshot is retried with backoff until ctx is done, unless logging in
// again is the fix.
func (p *pump) sync(ctx context.Context) {
	var me core.User
	var threads []core.Thread
	for n := 0; ; n++ {
		var err error
		if me, threads, err = p.b.Snapshot(ctx); err == nil {
			break
		}
		p.report(ctx, fmt.Errorf("load threads: %w", err))
		if authError(err) || !sleep(ctx, p.wait(n)) {
			return
		}
	}
	if me.Username == "" && me.ID == p.me.ID {
		me = p.me
	}
	p.me = me
	if err := p.st.SetMe(ctx, me); err != nil {
		p.writeFailed(ctx, err)
	} else if err := p.st.PutThreads(ctx, threads); err != nil {
		p.writeFailed(ctx, err)
	}
	p.ui.Send(ui.SnapshotMsg{Me: me, Threads: threads})
	select {
	case p.connect <- struct{}{}:
	default: // a connection is already pending
	}
}

// apply stores ev and returns what the UI should see: ev, ev with what the
// store resolved, or nil. A seq ID is saved only if every event before it
// was stored.
func (p *pump) apply(ctx context.Context, ev core.Event) core.Event {
	var err error
	switch e := ev.(type) {
	case core.MessageAdded:
		err = p.st.AddMessage(ctx, e.Message)
	case core.ThreadUpdated:
		err = p.st.PutThreads(ctx, []core.Thread{e.Thread})
	case core.ThreadRead:
		err = p.st.MarkRead(ctx, e.Thread, e.Until)
	case core.ReadReceipt:
		err = p.st.PutReadReceipt(ctx, e.Thread, e.User, e.UpTo)
	case core.ReactionAdded:
		err = p.st.PutReaction(ctx, e.Message, e.Reaction)
	case core.ReactionRemoved:
		e.Message, e.User, err = p.st.RemoveReaction(ctx, e.Message, e.User, e.ID)
		ev = e
		if e.User == 0 {
			p.log.Debug().Msg("removal of an unknown reaction dropped")
			ev = nil
		}
	case core.SeqAdvanced:
		if !p.failed {
			p.session.SeqID, p.session.SeqAt = e.SeqID, e.At
			if err := p.saveSession(); err != nil {
				p.report(ctx, err)
			}
		}
	case core.SessionChanged:
		// Saved even after a failed write: it keeps the last good seq ID.
		if err := p.saveAuth(); err != nil {
			p.report(ctx, err)
		}
	case core.ResyncNeeded:
		// The connection has ended; it's back once Connect reports it.
		p.ui.Send(core.StatusChanged{Status: core.Connecting})
		p.sync(ctx)
	}
	if err != nil {
		p.writeFailed(ctx, err)
	}
	return ev
}

// saveAuth saves the backend's current login data with the seq ID.
func (p *pump) saveAuth() error {
	if p.save == nil {
		return nil
	}
	auth, err := p.b.ExportAuth()
	if err != nil {
		return fmt.Errorf("export session: %w", err)
	}
	p.session.Auth = auth
	return p.saveSession()
}

func (p *pump) saveSession() error {
	if p.save == nil {
		return nil
	}
	if err := p.save(p.session); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// writeFailed stops seq IDs being saved and reports the first failure.
func (p *pump) writeFailed(ctx context.Context, err error) {
	if !p.failed {
		p.report(ctx, err)
	}
	p.failed = true
}

// report logs err and shows it: as the banner if logging in again is the
// fix, otherwise in the status bar. Nothing is reported once ctx is done,
// as that means the app is closing.
func (p *pump) report(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	p.log.Error().Err(err).Msg("sync failed")
	if authError(err) {
		p.ui.Send(core.AuthFailed{Err: err})
		return
	}
	p.ui.Send(ui.ErrorMsg{Err: err})
}

// authError reports whether logging in again is the fix for err, so
// retrying is pointless.
func authError(err error) bool {
	for _, auth := range []error{core.ErrSessionExpired, core.ErrVerificationRequired, core.ErrConsentRequired, core.ErrAccountSuspended} {
		if errors.Is(err, auth) {
			return true
		}
	}
	return false
}

// connectLoop runs the live connection each time sync asks for one, until
// ctx is done. Connect retries on its own, so an error means it gave up: it
// is called again with backoff, unless logging in again is the fix.
func (p *pump) connectLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.connect:
		}
		for n := 0; ; n++ {
			began := time.Now()
			err := p.b.Connect(ctx)
			if err == nil || ctx.Err() != nil {
				break
			}
			p.report(ctx, fmt.Errorf("connection: %w", err))
			if authError(err) {
				break
			}
			if time.Since(began) > maxBackoff {
				n = 0 // it worked for a while, so start the backoff again
			}
			if !sleep(ctx, p.wait(n)) {
				return
			}
		}
	}
}
