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
	"errors"
	"fmt"
	"maps"

	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/mautrix-meta/pkg/messagix/dgw"

	"termeownal/internal/core"
)

// handle is upstream's event handler. It turns events into core events on
// the Events channel, blocking while it's full (AGENTS.md §6.4). Some events
// arrive on the caller's goroutine during an HTTP call, so those never
// block. An error makes upstream reconnect and deliver the batch again.
func (b *Backend) handle(ctx context.Context, ev slidetypes.ClientEvent) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errClosed
	}
	b.wg.Add(1)
	b.mu.Unlock()
	defer b.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(b.ctx, cancel)()

	switch ev := ev.(type) {
	case *slidetypes.Delta:
		return b.handleDelta(ctx, ev)
	case *slidetypes.SeqIDUpdate:
		if err := b.send(ctx, core.SeqAdvanced{SeqID: ev.SeqID, At: ev.Timestamp}); err != nil {
			return err
		}
		return b.checkSession(ctx)
	case *slidetypes.Connected:
		if err := b.send(ctx, core.StatusChanged{Status: core.Connected}); err != nil {
			return err
		}
		return b.checkSession(ctx)
	case *slidetypes.Disconnected:
		if dgw.IsUnauthorized(ev.Error) || errors.Is(ev.Error, instameow.ErrUserIDMissing) {
			b.log.Warn().Err(ev.Error).Msg("Instagram refused the connection")
			err := fmt.Errorf("%w: %w", core.ErrSessionExpired, ev.Error)
			b.fail(err)
			return err // stops upstream reconnecting
		}
		b.log.Warn().Err(ev.Error).Int("failures", ev.FailureCount).Msg("disconnected")
		return b.send(ctx, core.StatusChanged{Status: core.Reconnecting})
	case *slidetypes.ResnapshotRequired:
		// Connect returns after this, and sends ResyncNeeded.
		b.log.Info().Msg("Instagram asked for a new snapshot")
		b.mu.Lock()
		b.resync = true
		b.mu.Unlock()
	case *slidetypes.ReconnectionStateUpdate:
		b.mu.Lock()
		b.state, b.stateDirty = ev.State, true
		b.mu.Unlock()
	case *slidetypes.AuthError:
		b.log.Warn().Err(ev.Error).Msg("Instagram rejected the login")
		b.fail(coreError(ev.Error))
	default:
		b.log.Debug().Str("event", fmt.Sprintf("%T", ev)).Msg("event ignored")
	}
	return nil
}

// fail records that the login stopped working and ends Connect.
func (b *Backend) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.authErr == nil {
		b.authErr = err
	}
	if b.stopConnect != nil {
		b.stopConnect()
	}
}

// handleDelta applies one delta. A message in a thread not seen yet, or a
// rename, fetches the thread first, so the thread is stored before its
// message.
func (b *Backend) handleDelta(ctx context.Context, d *slidetypes.Delta) error {
	var msg *slidetypes.Message
	switch data := d.Data.(type) {
	case *slidetypes.NewMessageEvent:
		msg = data.Message
	case *slidetypes.AdminMessageEvent:
		msg = data.Message
	}
	short := d.ThreadIGID
	if short == "" && msg != nil {
		short = msg.ThreadFBID
	}
	if short == "" {
		b.log.Debug().Str("type", d.TypeName).Msg("delta without a thread ignored")
		return nil
	}

	key, known := b.ids.key(short)
	_, renamed := d.Data.(*slidetypes.UpdateThreadNameEvent)
	if (msg != nil && !known) || renamed {
		client, err := b.current()
		if err != nil {
			return err
		}
		t, err := b.fetchThread(ctx, client, short)
		if err != nil {
			return err
		}
		if t == nil {
			b.log.Debug().Str("type", d.TypeName).Msg("delta for a thread termeownal can't show")
			return nil
		}
		key, known = core.ThreadID(t.ThreadKey), true
		thread := convertThread(t, core.UserID(client.GetOwnFBID()), b.log)
		if err := b.send(ctx, core.ThreadUpdated{Thread: thread}); err != nil {
			return err
		}
	}

	switch data := d.Data.(type) {
	case *slidetypes.NewMessageEvent, *slidetypes.AdminMessageEvent:
		if m, ok := convertMessage(msg, key, b.log); ok {
			return b.send(ctx, core.MessageAdded{Message: m})
		}
	case *slidetypes.MarkReadEvent:
		if known {
			return b.send(ctx, core.ThreadRead{Thread: key, Until: data.ReadTimestampMS.Time})
		}
	default:
		b.log.Debug().Str("type", d.TypeName).Msg("delta ignored")
	}
	return nil
}

// checkSession sends SessionChanged if the cookies or client state changed
// since ExportAuth. Upstream changes cookies without saying so.
func (b *Backend) checkSession(ctx context.Context) error {
	b.mu.Lock()
	changed := b.stateDirty || (b.client != nil && !maps.Equal(b.client.GetCookies().GetAll(), b.exported))
	b.mu.Unlock()
	if !changed {
		return nil
	}
	return b.send(ctx, core.SessionChanged{})
}

// send queues events in order, blocking until each is queued or ctx is
// done. It never drops one.
func (b *Backend) send(ctx context.Context, evs ...core.Event) error {
	for _, ev := range evs {
		select {
		case b.events <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
