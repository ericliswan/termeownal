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
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"termeownal/internal/core"
	"termeownal/internal/ui"
)

// Run shows the UI for a restored backend until the user quits, then closes
// the backend.
func Run(ctx context.Context, b core.Backend, log zerolog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := tea.NewProgram(ui.New(actions{ctx: ctx, b: b}, time.Now), tea.WithContext(ctx))

	// shortcut: the pump only forwards events; storing them, saving the seq
	// ID and handling ResyncNeeded land with the store.
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		for ev := range b.Events() {
			p.Send(ev)
		}
	}()
	go func() {
		if err := b.Connect(ctx); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("connection ended")
			p.Send(ui.ErrorMsg{Err: fmt.Errorf("connection: %w", err)})
		}
	}()

	_, err := p.Run()
	cancel()
	b.Close()
	<-pumped
	return err
}

// actions runs UI requests against a backend.
type actions struct {
	ctx context.Context
	b   core.Backend
}

func (a actions) Snapshot() tea.Cmd {
	return func() tea.Msg {
		me, threads, err := a.b.Snapshot(a.ctx)
		if err != nil {
			err = fmt.Errorf("load threads: %w", err)
		}
		return ui.SnapshotMsg{Me: me, Threads: threads, Err: err}
	}
}

func (a actions) History(thread core.ThreadID, before core.MessageID, n int) tea.Cmd {
	return func() tea.Msg {
		msgs, more, err := a.b.History(a.ctx, thread, before, n)
		if err != nil {
			err = fmt.Errorf("load messages: %w", err)
		}
		return ui.HistoryMsg{Thread: thread, Messages: msgs, More: more, Err: err}
	}
}

func (a actions) Send(thread core.ThreadID, text string) tea.Cmd {
	return func() tea.Msg {
		if err := a.b.Send(a.ctx, thread, text, a.b.NewOTID()); err != nil {
			return ui.ErrorMsg{Err: fmt.Errorf("send: %w", err)}
		}
		return nil
	}
}
