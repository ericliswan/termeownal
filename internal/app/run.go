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
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"termeownal/internal/core"
	"termeownal/internal/store"
	"termeownal/internal/ui"
)

// Run shows the UI until the user quits, keeping the cache st in sync with
// the backend b, restored from session s, then closes b. save, if not nil,
// saves the session as it changes: the seq ID once the events before it are
// stored, and the login data on SessionChanged and at exit.
func Run(ctx context.Context, b core.Backend, st *store.Store, s core.Session, save func(core.Session) error, log zerolog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	prog := tea.NewProgram(ui.New(actions{ctx: ctx, b: b, st: st}, time.Now), tea.WithContext(ctx))

	p := newPump(b, st, prog, log)
	p.session, p.save = s, save
	var wg sync.WaitGroup
	wg.Go(func() { p.run(ctx) })
	wg.Go(func() { p.connectLoop(ctx) })

	_, err := prog.Run()
	cancel()
	b.Close() // closes Events, which ends the pump
	wg.Wait()
	return err
}

// actions runs UI requests against the cache and the backend.
type actions struct {
	ctx context.Context
	b   core.Backend
	st  *store.Store
}

func (a actions) History(thread core.ThreadID, before core.MessageID, n int) tea.Cmd {
	return func() tea.Msg {
		msgs, more, err := a.history(thread, before, n)
		if err != nil {
			err = fmt.Errorf("load messages: %w", err)
		}
		return ui.HistoryMsg{Thread: thread, Messages: msgs, More: more, Err: err}
	}
}

// history returns up to n messages older than before from the cache,
// fetching what it lacks from the backend. The newest page always comes
// from the backend, so opening a thread fills any gap since the last run.
func (a actions) history(thread core.ThreadID, before core.MessageID, n int) ([]core.Message, bool, error) {
	from := before
	if before != "" {
		msgs, more, err := a.st.History(a.ctx, thread, before, n)
		if err != nil || len(msgs) == n || !more {
			return msgs, more, err
		}
		// Fetch what's older than the oldest cached message the server knows.
		if i := slices.IndexFunc(msgs, func(m core.Message) bool { return m.State == core.Sent }); i >= 0 {
			from = msgs[i].ID
		}
	}
	page, more, err := a.b.History(a.ctx, thread, from, n)
	if err == nil {
		err = a.st.PutHistory(a.ctx, thread, from, page, more)
	}
	if err != nil {
		return nil, false, err
	}
	return a.st.History(a.ctx, thread, before, n)
}

func (a actions) Send(thread core.ThreadID, text string) tea.Cmd {
	return func() tea.Msg {
		if err := a.b.Send(a.ctx, thread, text, a.b.NewOTID()); err != nil {
			return ui.ErrorMsg{Err: fmt.Errorf("send: %w", err)}
		}
		return nil
	}
}
