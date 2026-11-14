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
// Each History call is counted in loads, each Send recorded in sent, each
// MarkRead in read and each Typing in typing; while sendErr is set, Send
// fails with it.
type backendActions struct {
	b       core.Backend
	sendErr error
	// reactErr, while set, fails React.
	reactErr error
	loads    int
	sent     []core.Message
	read     []core.Message
	typing   []core.Typing
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

func (a *backendActions) Typing(thread core.ThreadID, active bool) tea.Cmd {
	return func() tea.Msg {
		a.typing = append(a.typing, core.Typing{Thread: thread, Active: active})
		return nil
	}
}

func (a *backendActions) React(thread core.ThreadID, msg core.MessageID, emoji string, remove bool) tea.Cmd {
	return func() tea.Msg {
		err := a.reactErr
		if err == nil {
			err = a.b.React(context.Background(), thread, msg, emoji, remove)
		}
		if err != nil {
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
// completion before the next message. Timers are held in timeouts until
// expire delivers them.
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
	hs.m.tick = func(_ time.Duration, fire func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return timer{fire(testNow)} }
	}
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
	case timer:
		h.timeouts = append(h.timeouts, msg.msg)
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
		"backspace": {Code: tea.KeyBackspace},
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

// timer wraps a timer's message, so run holds it.
type timer struct{ msg tea.Msg }

// expire delivers the held timers' messages, as if their time had passed.
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
	t.Run("reply", func(t *testing.T) {
		h := newHarness(t, 100, 30)
		h.press("j", "enter", "shift+tab", "k", "r")
		golden.RequireEqual(t, h.screen())
	})
	t.Run("emoji", func(t *testing.T) {
		h := newHarness(t, 100, 30)
		h.press("j", "enter", "love :hea")
		golden.RequireEqual(t, h.screen())
	})
	t.Run("react", func(t *testing.T) {
		h := newHarness(t, 100, 30)
		h.press("j", "enter", "shift+tab", "k", "e")
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

func TestReplyToSelectedMessage(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "k")
	target, ok := h.m.chat.Selected()
	if !ok || target.State != core.Sent {
		t.Fatalf("selected = %+v, %v; want a sent message", target, ok)
	}
	h.press("r")
	if h.m.focus != paneComposer || h.m.reply.ID != target.ID {
		t.Fatalf("after r: focus %d, reply %+v; want the composer replying to %s", h.m.focus, h.m.reply, target.ID)
	}
	if !strings.Contains(h.screen(), "↪ Replying to ") {
		t.Fatalf("no reply line above the composer:\n%s", h.screen())
	}
	if newest := h.m.threads[target.Thread].Messages; !strings.Contains(h.screen(), newest[len(newest)-1].Text) {
		t.Fatalf("the taller composer hid the newest message:\n%s", h.screen())
	}

	h.press("answer", "enter")
	if h.m.reply.ID != "" || strings.Contains(h.screen(), "Replying to") {
		t.Fatalf("reply not cleared after sending:\n%s", h.screen())
	}
	if got := h.act.sent[len(h.act.sent)-1].ReplyTo.ID; got != target.ID {
		t.Fatalf("sent reply to %q, want %q", got, target.ID)
	}
	h.wantOne("answer", core.Sending)
	h.do(h.nextEvent())
	if msg := h.wantOne("answer", core.Sent); msg.ReplyTo.ID != target.ID {
		t.Fatalf("echo replies to %q, want %q", msg.ReplyTo.ID, target.ID)
	}
	if !strings.Contains(h.screen(), "↪ ") {
		t.Fatalf("reply shown without its quote:\n%s", h.screen())
	}
}

func TestEmojiSuggestionsTakeTabAndEsc(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "love :hea")
	if !strings.Contains(h.screen(), ":heart:") {
		t.Fatalf("no emoji suggestions:\n%s", h.screen())
	}
	id, _ := h.m.chat.Thread()
	if msgs := h.m.threads[id].Messages; !strings.Contains(h.screen(), msgs[len(msgs)-1].Text) {
		t.Fatalf("the taller composer hid the newest message:\n%s", h.screen())
	}
	h.press("esc")
	if h.m.focus != paneComposer || strings.Contains(h.screen(), ":heart:") {
		t.Fatalf("esc: focus %d; want the composer, suggestions closed\n%s", h.m.focus, h.screen())
	}
	h.press("r", "tab")
	if h.m.focus != paneComposer {
		t.Fatalf("tab with suggestions moved focus to %d", h.m.focus)
	}
	h.press("enter")
	if got := h.act.sent[len(h.act.sent)-1].Text; got != "love ❤️" {
		t.Fatalf("sent %q, want %q", got, "love ❤️")
	}
	h.press("tab")
	if h.m.focus == paneComposer {
		t.Fatal("tab without suggestions kept the focus")
	}
}

// selectedReactions returns the reactions to the selected message.
func (h *harness) selectedReactions() []core.Reaction {
	h.t.Helper()
	sel, ok := h.m.chat.Selected()
	if !ok {
		h.t.Fatal("no message selected")
	}
	for _, msg := range h.m.threads[sel.Thread].Messages {
		if msg.ID == sel.ID {
			return msg.Reactions
		}
	}
	h.t.Fatalf("selected message %s not loaded", sel.ID)
	return nil
}

// own is the user's reaction in rs, or "".
func (h *harness) own(rs []core.Reaction) string {
	for _, r := range rs {
		if r.User == h.m.me.ID {
			return r.Emoji
		}
	}
	return ""
}

func TestReactToSelectedMessage(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "k", "e", "enter")
	if got := h.own(h.selectedReactions()); got != "❤️" {
		t.Fatalf("own reaction = %q before the echo, want ❤️", got)
	}
	if strings.Contains(h.screen(), "Esc cancel") {
		t.Fatalf("picker still open:\n%s", h.screen())
	}
	added := h.nextEvent().(core.ReactionAdded)
	h.do(added)
	h.do(added) // delivered twice
	if rs := h.selectedReactions(); h.own(rs) != "❤️" || len(slices.DeleteFunc(slices.Clone(rs), func(r core.Reaction) bool { return r.User != h.m.me.ID })) != 1 {
		t.Fatalf("reactions after the echo = %+v, want one ❤️ of the user's", rs)
	}

	h.press("e", "joy", "enter")
	if got := h.own(h.selectedReactions()); got != "😂" {
		t.Fatalf("own reaction = %q, want 😂 replacing ❤️", got)
	}
	h.do(h.nextEvent())

	h.press("e", "joy", "enter") // the same again removes it
	if got := h.own(h.selectedReactions()); got != "" {
		t.Fatalf("own reaction = %q, want it removed", got)
	}
	if ev, ok := h.nextEvent().(core.ReactionRemoved); !ok || ev.User != h.m.me.ID {
		t.Fatalf("event = %+v, want the user's removal", ev)
	}
}

func TestReactEscCancels(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "k")
	before := h.selectedReactions()
	h.press("e", "esc")
	if strings.Contains(h.screen(), "Esc cancel") || len(h.selectedReactions()) != len(before) {
		t.Fatalf("Esc didn't just close the picker:\n%s", h.screen())
	}
	if h.m.focus != paneChat {
		t.Fatalf("focus = %d after Esc in the picker, want the messages pane", h.m.focus)
	}
}

func TestReactErrorRestores(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "k", "e", "enter")
	h.do(h.nextEvent())
	h.act.reactErr = errors.New("react: boom")
	h.press("e", "joy", "enter")
	if got := h.own(h.selectedReactions()); got != "❤️" {
		t.Fatalf("own reaction = %q after a failure, want ❤️ back", got)
	}
	if !strings.Contains(h.screen(), "boom") {
		t.Fatalf("error not shown:\n%s", h.screen())
	}
}

func TestOthersReactions(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "k")
	sel, _ := h.m.chat.Selected()
	other := h.m.threads[sel.Thread].Members[0].ID
	add := func(e string) {
		h.do(core.ReactionAdded{Thread: sel.Thread, Message: sel.ID, Reaction: core.Reaction{User: other, Emoji: e, ID: "r" + e}})
	}
	theirs := func() string {
		for _, r := range h.selectedReactions() {
			if r.User == other {
				return r.Emoji
			}
		}
		return ""
	}
	add("😮")
	add("\x1b[2J👍")
	if got := theirs(); got != "[2J👍" {
		t.Fatalf("their reaction = %q, want the cleaned 👍 replacing 😮", got)
	}
	if !strings.Contains(h.screen(), "👍") {
		t.Fatalf("reaction not shown:\n%s", h.screen())
	}
	h.do(core.ReactionRemoved{Thread: sel.Thread, Message: sel.ID, User: other})
	if got := theirs(); got != "" {
		t.Fatalf("their reaction = %q after removal, want none", got)
	}
}

func TestEscCancelsReplyFirst(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "r", "esc")
	if h.m.reply.ID != "" || h.m.focus != paneComposer {
		t.Fatalf("first esc: reply %+v, focus %d; want no reply, composer", h.m.reply, h.m.focus)
	}
	h.press("esc")
	if h.m.focus != paneThreads {
		t.Fatalf("second esc: focus %d, want the thread list", h.m.focus)
	}
}

func TestOpeningAnotherThreadDropsReply(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "shift+tab", "r", "tab", "j", "enter")
	if h.m.reply.ID != "" {
		t.Fatalf("reply %+v kept after opening another thread", h.m.reply)
	}
}

func TestReplyKeyIgnoresMessageStillSending(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter", "pending", "enter") // the echo stays queued
	h.press("shift+tab", "r")
	if h.m.reply.ID != "" || h.m.focus != paneChat || len(h.act.sent) != 1 {
		t.Fatalf("r on a sending message: reply %+v, focus %d, %d sends", h.m.reply, h.m.focus, len(h.act.sent))
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

func TestOthersTyping(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	id, _ := h.m.chat.Thread()
	who := h.m.threads[id].Members[0].ID
	start := core.Typing{Thread: id, User: who, Active: true}
	typing := func(want bool, when string) {
		t.Helper()
		if got := strings.Contains(h.screen(), "is typing…"); got != want {
			t.Fatalf("%s: typing shown = %t\n%s", when, got, h.screen())
		}
	}

	h.do(start)
	typing(true, "after a start")
	h.do(core.Typing{Thread: id, User: who})
	typing(false, "after a stop")

	h.do(start)
	h.do(core.MessageAdded{Message: core.Message{ID: "typed", Thread: id, Sender: who, At: testNow, Text: "hi"}})
	typing(false, "after their message")

	// Each start shows it for typingShown; an older start's timer doesn't
	// end a newer one.
	h.timeouts = nil
	h.do(start)
	h.do(start)
	h.do(h.timeouts[0])
	typing(true, "after the first start's timer")
	h.do(h.timeouts[1])
	typing(false, "after the last start's timer")
}

func TestTellsThreadWhileTyping(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.press("enter")
	id, _ := h.m.chat.Thread()
	var want []core.Typing
	told := func(active bool, when string) {
		t.Helper()
		want = append(want, core.Typing{Thread: id, Active: active})
		if !slices.Equal(h.act.typing, want) {
			t.Fatalf("%s: typing calls %+v, want %+v", when, h.act.typing, want)
		}
	}

	h.press("hi")
	told(true, "typing (once, as the clock hasn't moved)")
	h.press("enter")
	told(false, "after sending")

	h.timeouts = nil
	h.press("ab")
	told(true, "typing again")
	h.do(h.timeouts[0])
	if len(h.act.typing) != len(want) {
		t.Fatalf("stopped by an older edit's idle timer: %+v", h.act.typing)
	}
	h.do(h.timeouts[1])
	told(false, "after typingIdle")

	h.press("c")
	told(true, "typing after the idle stop")
	h.press("backspace", "backspace", "backspace")
	told(false, "after clearing the text")

	h.press("d", "esc", "j", "enter")
	want = append(want, core.Typing{Thread: id, Active: true})
	told(false, "after typing, then switching threads")
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
	for _, msg := range h.timeouts {
		if _, ok := msg.(echoTimeoutMsg); ok {
			t.Fatal("echo timeout started for a failed send")
		}
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
