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
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"termeownal/internal/backend/fake"
	"termeownal/internal/core"
	"termeownal/internal/ui/composer"
)

var testNow = time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)

func clock() time.Time { return testNow }

// backendActions runs Actions synchronously against a backend, like app does.
// Each History call is counted in loads, each Send recorded in sent, and each
// MarkRead in read; while sendErr is set, Send fails with it.
type backendActions struct {
	b       core.Backend
	sendErr error
	loads   int
	sent    []core.Message
	read    []core.Message
}

func (a *backendActions) History(thread core.ThreadID, before core.MessageID, n int) tea.Cmd {
	return func() tea.Msg {
		a.loads++
		msgs, more, err := a.b.History(context.Background(), thread, before, n)
		return HistoryMsg{Thread: thread, Before: before, Messages: msgs, More: more, Err: err}
	}
}

func (a *backendActions) NewOTID() string { return a.b.NewOTID() }

func (a *backendActions) Send(m core.Message) tea.Cmd {
	return func() tea.Msg {
		a.sent = append(a.sent, m)
		err := a.sendErr
		if err == nil {
			err = a.b.Send(context.Background(), m.Thread, m.Text, m.OTID, m.ReplyTo.ID)
		}
		return SentMsg{Message: m, Err: err}
	}
}

func (a *backendActions) MarkRead(upTo core.Message) tea.Cmd {
	return func() tea.Msg {
		a.read = append(a.read, upTo)
		if err := a.b.MarkRead(context.Background(), upTo); err != nil {
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

// snapshot is the SnapshotMsg app sends once the backend's snapshot is stored.
func snapshot(t *testing.T, b core.Backend) SnapshotMsg {
	t.Helper()
	me, threads, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return SnapshotMsg{Me: me, Threads: threads}
}

// harness drives a Model by hand, running each returned command to
// completion before the next message. Echo timeouts are held in timeouts
// until expire delivers them.
type harness struct {
	t        *testing.T
	b        *fake.Backend
	act      *backendActions
	m        Model
	timeouts []tea.Msg
}

func newHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	b := newFake(t)
	act := &backendActions{b: b}
	hs := &harness{t: t, b: b, act: act, m: New(act, clock)}
	hs.m.echoTimeout = 0
	hs.do(tea.WindowSizeMsg{Width: w, Height: h})
	hs.do(snapshot(t, b))
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
	case echoTimeoutMsg:
		h.timeouts = append(h.timeouts, msg)
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

// expire delivers the held echo timeouts, as if echoTimeout had passed.
func (h *harness) expire() {
	h.t.Helper()
	msgs := h.timeouts
	h.timeouts = nil
	for _, msg := range msgs {
		h.do(msg)
	}
}

// openMessages returns the open thread's messages with text.
func (h *harness) openMessages(text string) []core.Message {
	h.t.Helper()
	id, ok := h.m.chat.Thread()
	if !ok {
		h.t.Fatal("no thread open")
	}
	var out []core.Message
	for _, msg := range h.m.threads[id].Messages {
		if msg.Text == text {
			out = append(out, msg)
		}
	}
	return out
}

// wantOne fails unless the open thread has one message with text, in state.
func (h *harness) wantOne(text string, state core.SendState) core.Message {
	h.t.Helper()
	msgs := h.openMessages(text)
	if len(msgs) != 1 || msgs[0].State != state {
		h.t.Fatalf("messages %q = %+v, want one in state %d\n%s", text, msgs, state, h.screen())
	}
	return msgs[0]
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

func TestSendShowsLocalCopyUntilEcho(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "hello", "enter")
	local := h.wantOne("hello", core.Sending)
	if !strings.Contains(h.screen(), "sending…") {
		t.Fatalf("local copy not shown as sending:\n%s", h.screen())
	}
	echo := h.nextEvent().(core.MessageAdded)
	if echo.Message.OTID != local.OTID {
		t.Fatalf("echo OTID = %q, want %q", echo.Message.OTID, local.OTID)
	}
	h.do(echo)
	h.wantOne("hello", core.Sent)
	h.expire()
	h.wantOne("hello", core.Sent)
	if strings.Contains(h.screen(), "sending…") || strings.Contains(h.screen(), "not sent") {
		t.Fatalf("echoed message still shows a send state:\n%s", h.screen())
	}
}

func TestSeenFollowsReceipts(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "read me", "enter")
	echo := h.nextEvent().(core.MessageAdded)
	h.do(echo)
	if strings.Contains(h.screen(), "Seen") {
		t.Fatalf("Seen before any receipt:\n%s", h.screen())
	}
	st := h.m.threads[echo.Message.Thread]
	reader := st.Members[0].ID
	h.do(core.ReadReceipt{Thread: st.ID, User: reader, UpTo: echo.Message.At})
	if !strings.Contains(h.screen(), "Seen") {
		t.Fatalf("no Seen after a receipt:\n%s", h.screen())
	}

	// A late receipt, or a thread with older watermarks, doesn't undo it.
	h.do(core.ReadReceipt{Thread: st.ID, User: reader, UpTo: echo.Message.At.Add(-time.Hour)})
	stale := st.Thread
	stale.Messages, stale.ReadUpTo = nil, nil
	h.do(core.ThreadUpdated{Thread: stale})
	if !strings.Contains(h.screen(), "Seen") {
		t.Fatalf("Seen lost to an older watermark:\n%s", h.screen())
	}

	// A newer message from the user hasn't been seen yet. (The fake's clock
	// is fixed, so it's added by hand.)
	later := echo.Message
	later.ID, later.OTID, later.Text, later.At = "later", "", "again", later.At.Add(time.Second)
	h.do(core.MessageAdded{Message: later})
	if strings.Contains(h.screen(), "Seen") {
		t.Fatalf("Seen under an unread message:\n%s", h.screen())
	}
}

func TestEchoAfterTimeoutReplacesFailedCopy(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "late", "enter")
	h.expire()
	h.wantOne("late", core.SendFailed)
	if !strings.Contains(h.screen(), "✗ not sent · r to retry") {
		t.Fatalf("failed message not marked:\n%s", h.screen())
	}
	h.do(h.nextEvent())
	h.wantOne("late", core.Sent)
}

func TestRetryReusesOTID(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "never", "enter")
	echo := h.nextEvent() // held back: as far as the UI knows, it never came
	h.expire()
	h.wantOne("never", core.SendFailed)

	h.press("shift+tab", "r")
	h.wantOne("never", core.Sending)
	if len(h.act.sent) != 2 || h.act.sent[1].OTID != h.act.sent[0].OTID {
		t.Fatalf("sends = %+v, want the same OTID twice", h.act.sent)
	}
	h.do(echo)
	h.wantOne("never", core.Sent)
}

func TestDuplicateEchoChangesNothing(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "twice", "enter")
	echo := h.nextEvent()
	h.do(echo)
	h.do(echo)
	h.wantOne("twice", core.Sent)
}

func TestSendErrorMarksFailed(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.act.sendErr = errors.New("boom")
	h.press("enter", "oops", "enter")
	h.wantOne("oops", core.SendFailed)
	if !strings.Contains(h.screen(), "boom") {
		t.Fatalf("send error not shown:\n%s", h.screen())
	}
	if len(h.timeouts) != 0 {
		t.Fatal("echo timeout started for a failed send")
	}

	h.act.sendErr = nil
	h.press("shift+tab", "r")
	h.do(h.nextEvent())
	h.wantOne("oops", core.Sent)
}

func TestSendsOneAtATimeInOrder(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	m, first := h.m.Update(composer.SubmitMsg{Text: "one"})
	m, second := m.(Model).Update(composer.SubmitMsg{Text: "two"})
	h.m = m.(Model)
	if second != nil {
		t.Fatal("second send started while the first was in flight")
	}
	h.run(first)
	if len(h.act.sent) != 2 || h.act.sent[0].Text != "one" || h.act.sent[1].Text != "two" {
		t.Fatalf("sends = %+v, want one then two", h.act.sent)
	}
	if len(h.m.outbox) != 0 {
		t.Fatalf("outbox = %+v after both were sent", h.m.outbox)
	}
}

func TestCachedLocalCopyDoesntOverride(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "cached", "enter")
	id, _ := h.m.chat.Thread()
	stale := h.wantOne("cached", core.Sending)
	h.expire()

	// History read the local copy from the cache before the UI failed it.
	page := slices.Clone(h.m.threads[id].Messages)
	stale.State = core.Sending
	h.do(HistoryMsg{Thread: id, Messages: append(page, stale), More: true})
	h.wantOne("cached", core.SendFailed)

	// Or before the echo was stored.
	h.do(h.nextEvent())
	h.do(HistoryMsg{Thread: id, Messages: append(page, stale), More: true})
	h.wantOne("cached", core.Sent)
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

func TestSnapshotAcrossAGapDropsOlderMessages(t *testing.T) {
	h := newHarness(t, 100, 30)
	id, _ := h.m.list.Selected()
	n := len(h.m.threads[id].Messages)
	h.do(snapshot(t, h.b))
	if got := len(h.m.threads[id].Messages); got != n {
		t.Fatalf("messages = %d after the same snapshot again, want %d", got, n)
	}

	h.m.threads[id].more = false
	th := h.m.threads[id].Thread
	th.Messages = []core.Message{{ID: "mid.after-gap", Thread: id, Sender: 101, At: testNow.Add(time.Hour), Text: "new"}}
	for range 2 {
		h.do(SnapshotMsg{Me: h.m.me, Threads: []core.Thread{th}})
		if msgs := h.m.threads[id].Messages; len(msgs) != 1 || msgs[0].ID != "mid.after-gap" {
			t.Fatalf("%d messages after a snapshot across a gap, want only the new one", len(msgs))
		}
	}
	if !h.m.threads[id].more {
		t.Error("dropped messages can't be loaded again")
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

func TestMarkReadOnlyWhenSeen(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.do(tea.BlurMsg{})
	h.press("enter")
	id, _ := h.m.chat.Thread()
	incoming := func(mid core.MessageID) {
		h.t.Helper()
		h.do(core.MessageAdded{Message: core.Message{ID: mid, Thread: id, Sender: 101, At: testNow, Text: string(mid)}})
	}
	wantRead := func(step string, unread bool, ids ...core.MessageID) {
		t.Helper()
		var got []core.MessageID
		for _, m := range h.act.read {
			got = append(got, m.ID)
		}
		if h.m.threads[id].Unread != unread || !slices.Equal(got, ids) {
			t.Fatalf("%s: unread = %v, MarkRead calls = %v; want %v, %v", step, h.m.threads[id].Unread, got, unread, ids)
		}
	}
	incoming("mid.a")
	wantRead("blurred", true)
	h.do(tea.FocusMsg{})
	wantRead("focused", false, "mid.a")
	incoming("mid.b")
	wantRead("message while focused", false, "mid.a", "mid.b")
	h.press("esc", "?") // ? in the composer is text
	incoming("mid.c")
	wantRead("help open", true, "mid.a", "mid.b")
	h.press("esc")
	wantRead("help closed", false, "mid.a", "mid.b", "mid.c")
	h.do(tea.WindowSizeMsg{Width: 50, Height: 30})
	incoming("mid.d")
	wantRead("too small", true, "mid.a", "mid.b", "mid.c")
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

func TestConnectedReloadsTheOpenThread(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	first, _ := h.m.chat.Thread()
	h.press("esc", "j", "enter")
	if open, _ := h.m.chat.Thread(); open == first {
		t.Fatal("second thread not open")
	}
	loads := h.act.loads
	h.do(core.StatusChanged{Status: core.Connected})
	if h.act.loads != loads+1 {
		t.Fatalf("History calls = %d, want %d", h.act.loads, loads+1)
	}
	if h.m.threads[first].fetched {
		t.Error("first thread not marked stale")
	}
}

func TestConnectedWhileLoadingOlderReloadsAfter(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	id, _ := h.m.chat.Thread()
	st := h.m.threads[id]
	// PgUp's request, still running when the connection comes up.
	older := h.act.History(id, st.Messages[0].ID, historyPage)
	st.loading = true
	loads := h.act.loads
	h.do(core.StatusChanged{Status: core.Connected})
	if h.act.loads != loads {
		t.Fatalf("History calls = %d while loading, want %d", h.act.loads, loads)
	}
	h.run(older)
	if h.act.loads != loads+2 || !st.fetched {
		t.Errorf("History calls = %d, newest page loaded %v; want %d and true", h.act.loads, st.fetched, loads+2)
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
	tm := teatest.NewTestModel(t, New(&backendActions{b: b}, clock), teatest.WithInitialTermSize(100, 30))
	tm.Send(snapshot(t, b))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("COMP2003 project")) },
		teatest.WithDuration(5*time.Second))
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
