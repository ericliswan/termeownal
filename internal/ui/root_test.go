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

package ui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"termeownal/internal/backend/fake"
	"termeownal/internal/core"
)

var testNow = time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)

func clock() time.Time { return testNow }

// backendActions runs Actions synchronously against a backend, like app does.
type backendActions struct{ b core.Backend }

func (a backendActions) Snapshot() tea.Cmd {
	return func() tea.Msg {
		me, threads, err := a.b.Snapshot(context.Background())
		return SnapshotMsg{Me: me, Threads: threads, Err: err}
	}
}

func (a backendActions) History(thread core.ThreadID, before core.MessageID, n int) tea.Cmd {
	return func() tea.Msg {
		msgs, more, err := a.b.History(context.Background(), thread, before, n)
		return HistoryMsg{Thread: thread, Messages: msgs, More: more, Err: err}
	}
}

func (a backendActions) Send(thread core.ThreadID, text string) tea.Cmd {
	return func() tea.Msg {
		if err := a.b.Send(context.Background(), thread, text, a.b.NewOTID()); err != nil {
			return ErrorMsg{Err: err}
		}
		return nil
	}
}

func newFake(t *testing.T) *fake.Backend {
	t.Helper()
	b := fake.New(fake.Options{Now: clock})
	step, err := fake.Authenticator{}.Submit(context.Background(), map[string]string{"username": "u", "password": "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Restore(context.Background(), core.Session{Auth: step.Result.Auth}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

// harness drives a Model by hand, running each returned command to
// completion before the next message.
type harness struct {
	t *testing.T
	b *fake.Backend
	m Model
}

func newHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	b := newFake(t)
	hs := &harness{t: t, b: b, m: New(backendActions{b}, clock)}
	hs.do(tea.WindowSizeMsg{Width: w, Height: h})
	hs.run(hs.m.Init())
	return hs
}

func (h *harness) do(msg tea.Msg) {
	h.t.Helper()
	m, cmd := h.m.Update(msg)
	h.m = m.(Model)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		h.do(msg)
	}
}

func (h *harness) press(keys ...string) {
	h.t.Helper()
	special := map[string]tea.KeyPressMsg{
		"enter":     {Code: tea.KeyEnter},
		"esc":       {Code: tea.KeyEscape},
		"tab":       {Code: tea.KeyTab},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
		"pgup":      {Code: tea.KeyPgUp},
	}
	for _, k := range keys {
		if msg, ok := special[k]; ok {
			h.do(msg)
			continue
		}
		for _, r := range k {
			h.do(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

func (h *harness) screen() string { return ansi.Strip(h.m.View().Content) }

// nextEvent returns the next live event that isn't a SeqAdvanced.
func (h *harness) nextEvent() core.Event {
	h.t.Helper()
	for {
		select {
		case ev := <-h.b.Events():
			if _, ok := ev.(core.SeqAdvanced); !ok {
				return ev
			}
		case <-time.After(5 * time.Second):
			h.t.Fatal("no event from the backend")
		}
	}
}

func TestScreens(t *testing.T) {
	t.Run("threads", func(t *testing.T) {
		h := newHarness(t, 100, 30)
		h.do(core.StatusChanged{Status: core.Connected})
		golden.RequireEqual(t, h.screen())
	})
	t.Run("open thread", func(t *testing.T) {
		h := newHarness(t, 100, 30)
		h.press("j", "enter")
		golden.RequireEqual(t, h.screen())
	})
	t.Run("help", func(t *testing.T) {
		h := newHarness(t, 100, 30)
		h.press("?")
		golden.RequireEqual(t, h.screen())
	})
	t.Run("too small", func(t *testing.T) {
		h := newHarness(t, 59, 20)
		golden.RequireEqual(t, h.screen())
	})
}

func TestSendShowsEcho(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "hello from the test", "enter")
	if strings.Contains(h.screen(), "hello from the test") {
		t.Fatal("message shown before its echo")
	}
	h.do(h.nextEvent())
	if !strings.Contains(h.screen(), "hello from the test") {
		t.Fatalf("echo not shown:\n%s", h.screen())
	}
}

func TestMessageAddedTwiceChangesNothing(t *testing.T) {
	h := newHarness(t, 100, 30)
	id, _ := h.m.list.Selected()
	msg := core.MessageAdded{Message: core.Message{ID: "mid.twice", Thread: id, Sender: 101, At: testNow, Text: "hi"}}
	h.do(msg)
	n := len(h.m.threads[id].Messages)
	h.do(msg)
	if got := len(h.m.threads[id].Messages); got != n {
		t.Fatalf("messages = %d after replay, want %d", got, n)
	}
}

func TestUnreadOnlyWhenNotOpen(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	open, _ := h.m.chat.Thread()
	h.m.threads[open].Unread = false
	var other core.ThreadID
	for id, st := range h.m.threads {
		if id != open && !st.Unread {
			other = id
		}
	}
	h.do(core.MessageAdded{Message: core.Message{ID: "mid.a", Thread: open, Sender: 101, At: testNow, Text: "a"}})
	h.do(core.MessageAdded{Message: core.Message{ID: "mid.b", Thread: other, Sender: 101, At: testNow, Text: "b"}})
	h.do(core.MessageAdded{Message: core.Message{ID: "mid.c", Thread: other, Sender: h.m.me.ID, At: testNow, Text: "c"}})
	if h.m.threads[open].Unread {
		t.Error("open thread marked unread")
	}
	if !h.m.threads[other].Unread {
		t.Error("other thread not marked unread")
	}
}

func TestPgUpAtTopLoadsOlder(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	id, _ := h.m.chat.Thread()
	before := len(h.m.threads[id].Messages)
	h.press("esc", "tab", "g", "pgup")
	if got := len(h.m.threads[id].Messages); got != before+historyPage {
		t.Fatalf("messages = %d, want %d", got, before+historyPage)
	}
}

func TestAuthBanner(t *testing.T) {
	cases := map[error]string{
		core.ErrSessionExpired:       "Session expired. Run `meow login`.",
		core.ErrVerificationRequired: "Instagram wants to verify this login.",
		core.ErrConsentRequired:      "Accept Instagram's pending terms",
		core.ErrAccountSuspended:     "This account is suspended.",
	}
	for err, want := range cases {
		h := newHarness(t, 100, 30)
		h.do(core.AuthFailed{Err: fmt.Errorf("restore: %w", err)})
		h.press("j")
		if !strings.Contains(h.screen(), want) {
			t.Errorf("%v: banner missing %q", err, want)
		}
	}
}

func TestErrorClearsOnKey(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.do(ErrorMsg{Err: fmt.Errorf("send: %w", core.ErrRateLimited)})
	if !strings.Contains(h.screen(), "rate-limiting") {
		t.Fatalf("rate-limit message missing:\n%s", h.screen())
	}
	h.press("j")
	if strings.Contains(h.screen(), "rate-limiting") {
		t.Fatal("error still shown after a key")
	}
}

func TestIncomingTextCantInjectEscapes(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	id, _ := h.m.chat.Thread()
	h.do(core.MessageAdded{Message: core.Message{ID: "mid.esc", Thread: id, Sender: 101, At: testNow,
		Text: "a\x1b]0;pwned\x07b\x1b[2Jc\u009bd\te\xff"}})
	got := h.m.threads[id].Messages[len(h.m.threads[id].Messages)-1].Text
	if got != "a]0;pwnedb[2Jcd e\uFFFD" {
		t.Fatalf("text = %q", got)
	}
}

// TestRunsInProgram checks the model in a real Bubble Tea program loop.
func TestRunsInProgram(t *testing.T) {
	b := newFake(t)
	tm := teatest.NewTestModel(t, New(backendActions{b}, clock), teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("Cat Café Crew")) },
		teatest.WithDuration(5*time.Second))
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
