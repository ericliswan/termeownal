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

	// saveSeq saves the sync position; nil saves nothing.
	saveSeq func(context.Context, core.SeqAdvanced) error
	// connect asks connectLoop to connect. sync sends on it once a snapshot
	// is stored, so the events that follow apply on top of the snapshot.
	connect chan struct{}
	// failed is set once a write fails. No seq ID is saved after that, so the
	// next start replays what the cache missed.
	failed bool
}

func newPump(b core.Backend, st *store.Store, to uiSender, log zerolog.Logger) *pump {
	return &pump{b: b, st: st, ui: to, log: log, connect: make(chan struct{}, 1)}
}

// run shows the cache, then stores and shows the backend's snapshot, then
// applies events until the backend closes its Events channel.
func (p *pump) run(ctx context.Context) {
	// The cache belongs to this account: meow login clears another
	// account's with store.SetMe.
	if me, threads, err := p.st.Snapshot(ctx, snapshotMessages); err != nil {
		p.report(ctx, err)
	} else {
		p.ui.Send(ui.SnapshotMsg{Me: me, Threads: threads})
	}
	p.sync(ctx)
	for ev := range p.b.Events() {
		p.apply(ctx, ev)
		p.ui.Send(ev)
	}
}

// sync stores and shows a snapshot from the backend, then asks for a
// connection, which delivers every event since the saved seq ID.
func (p *pump) sync(ctx context.Context) {
	me, threads, err := p.b.Snapshot(ctx)
	if err != nil {
		// shortcut: a failed snapshot isn't retried, so the app stays
		// offline; retry with backoff in the reconnect item.
		p.report(ctx, fmt.Errorf("load threads: %w", err))
		return
	}
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

// apply stores ev. A seq ID is saved only if every event before it was
// stored.
func (p *pump) apply(ctx context.Context, ev core.Event) {
	var err error
	switch ev := ev.(type) {
	case core.MessageAdded:
		err = p.st.AddMessage(ctx, ev.Message)
	case core.ThreadUpdated:
		err = p.st.PutThreads(ctx, []core.Thread{ev.Thread})
	case core.ThreadRead:
		err = p.st.MarkRead(ctx, ev.Thread, ev.Until)
	case core.SeqAdvanced:
		if !p.failed && p.saveSeq != nil {
			if err := p.saveSeq(ctx, ev); err != nil {
				p.report(ctx, fmt.Errorf("save sync position: %w", err))
			}
		}
	case core.ResyncNeeded:
		p.sync(ctx)
	}
	if err != nil {
		p.writeFailed(ctx, err)
	}
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
	for _, auth := range []error{core.ErrSessionExpired, core.ErrVerificationRequired, core.ErrConsentRequired, core.ErrAccountSuspended} {
		if errors.Is(err, auth) {
			p.ui.Send(core.AuthFailed{Err: err})
			return
		}
	}
	p.ui.Send(ui.ErrorMsg{Err: err})
}

// connectLoop runs the live connection each time sync asks for one, until
// ctx is done.
func (p *pump) connectLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.connect:
		}
		if err := p.b.Connect(ctx); err != nil {
			// shortcut: a dropped connection isn't retried; retry with
			// backoff in the reconnect item.
			p.report(ctx, fmt.Errorf("connection: %w", err))
		}
	}
}
