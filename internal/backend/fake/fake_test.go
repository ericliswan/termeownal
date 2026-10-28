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

package fake

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"termeownal/internal/core"
)

var fixedNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// newBackend returns a logged-in Backend that is closed when the test ends.
func newBackend(t *testing.T, opts Options) *Backend {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return fixedNow }
	}
	b := New(opts)
	t.Cleanup(b.Close)
	if err := b.Restore(t.Context(), core.Session{Auth: login(t, "tester"), SeqID: 500}); err != nil {
		t.Fatal(err)
	}
	return b
}

func login(t *testing.T, user string) core.AuthData {
	t.Helper()
	var a Authenticator
	if _, err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	step, err := a.Submit(t.Context(), map[string]string{"username": user, "password": "pw"})
	if err != nil || step.Result == nil {
		t.Fatalf("Submit = %+v, %v", step, err)
	}
	return step.Result.Auth
}

// next returns the next event or fails after a timeout.
func next(t *testing.T, b *Backend) core.Event {
	t.Helper()
	select {
	case ev := <-b.Events():
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return nil
	}
}

func TestLogin(t *testing.T) {
	var a Authenticator
	step, err := a.Start(t.Context())
	if err != nil || len(step.Fields) != 2 || !step.Fields[1].Secret {
		t.Fatalf("Start = %+v, %v", step, err)
	}
	for _, vals := range []map[string]string{
		{"username": "x", "password": WrongPassword},
		{"username": "x"},
		{"password": "pw"},
	} {
		if _, err := a.Submit(t.Context(), vals); err == nil {
			t.Errorf("Submit(%v) succeeded", vals)
		}
	}
	step, err = a.Submit(t.Context(), map[string]string{"username": "x", "password": "hunter2"})
	if err != nil || step.Result == nil || step.Result.Me != me {
		t.Fatalf("Submit = %+v, %v", step, err)
	}
	if strings.Contains(string(step.Result.Auth), "hunter2") {
		t.Error("auth data contains the password")
	}
}

func TestNeedsRestore(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	if _, _, err := b.Snapshot(t.Context()); !errors.Is(err, core.ErrSessionExpired) {
		t.Errorf("Snapshot before Restore: %v", err)
	}
	if err := b.Connect(t.Context()); !errors.Is(err, core.ErrSessionExpired) {
		t.Errorf("Connect before Restore: %v", err)
	}
	if err := b.Restore(t.Context(), core.Session{Auth: core.AuthData("junk")}); !errors.Is(err, core.ErrSessionExpired) {
		t.Errorf("Restore(junk): %v", err)
	}
	auth := login(t, "tester")
	if err := b.Restore(t.Context(), core.Session{Auth: auth}); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ExportAuth(); err != nil || string(got) != string(auth) {
		t.Errorf("ExportAuth = %q, %v", got, err)
	}
}

func TestSnapshot(t *testing.T) {
	b := newBackend(t, Options{})
	gotMe, threads, err := b.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotMe != me || len(threads) != len(threadSpecs) {
		t.Fatalf("me = %+v, %d threads", gotMe, len(threads))
	}
	var groups, unread int
	for i, th := range threads {
		if i > 0 && th.LastActivity.After(threads[i-1].LastActivity) {
			t.Errorf("thread %d is newer than the one before it", i)
		}
		if th.Title == "" || len(th.Members) == 0 || len(th.Messages) == 0 {
			t.Errorf("thread %d incomplete: %+v", th.ID, th)
		}
		if th.IsGroup {
			groups++
		} else if th.ID != core.ThreadID(th.Members[0].ID) {
			t.Errorf("1:1 thread %d is not keyed by the other user", th.ID)
		}
		if th.Unread {
			unread++
			if last := th.Messages[len(th.Messages)-1]; last.Sender == me.ID {
				t.Errorf("unread thread %d ends with my own message", th.ID)
			}
		}
		for _, m := range th.Messages {
			if m.Thread != th.ID {
				t.Errorf("message %s has thread %d, want %d", m.ID, m.Thread, th.ID)
			}
		}
	}
	if groups == 0 || unread == 0 {
		t.Errorf("%d groups, %d unread; want some of each", groups, unread)
	}

	_, again, _ := newBackend(t, Options{}).Snapshot(t.Context())
	if !reflect.DeepEqual(threads, again) {
		t.Error("the same clock gave different data")
	}
}

func TestHistoryPaging(t *testing.T) {
	b := newBackend(t, Options{})
	_, threads, _ := b.Snapshot(t.Context())
	for _, th := range threads {
		var all []core.Message
		var before core.MessageID
		for more := true; more; {
			page, m, err := b.History(t.Context(), th.ID, before, 50)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				t.Fatalf("thread %d: empty page with more", th.ID)
			}
			all = append(page, all...)
			before, more = page[0].ID, m
		}
		seen := map[core.MessageID]bool{}
		for i, m := range all {
			if seen[m.ID] {
				t.Fatalf("thread %d: duplicate %s", th.ID, m.ID)
			}
			seen[m.ID] = true
			if i > 0 && m.At.Before(all[i-1].At) {
				t.Fatalf("thread %d: history out of order at %d", th.ID, i)
			}
		}
		if want := th.Messages[len(th.Messages)-1].ID; all[len(all)-1].ID != want {
			t.Errorf("thread %d: newest history %s, snapshot %s", th.ID, all[len(all)-1].ID, want)
		}
	}
	if _, _, err := b.History(t.Context(), threads[0].ID, "mid.nope", 10); err == nil {
		t.Error("unknown before ID accepted")
	}
}

func TestSendEchoesOnce(t *testing.T) {
	b := newBackend(t, Options{})
	_, threads, _ := b.Snapshot(t.Context())
	th := threads[0].ID
	otid := b.NewOTID()
	if otid == b.NewOTID() {
		t.Fatal("NewOTID repeated")
	}
	for range 3 { // retries with the same OTID
		if err := b.Send(t.Context(), th, "hello", otid); err != nil {
			t.Fatal(err)
		}
	}
	added, ok := next(t, b).(core.MessageAdded)
	if !ok || added.Message.OTID != otid || added.Message.Sender != me.ID ||
		added.Message.Text != "hello" || added.Message.State != core.Sent {
		t.Fatalf("echo = %+v", added)
	}
	if seq, ok := next(t, b).(core.SeqAdvanced); !ok || seq.SeqID != 501 {
		t.Fatalf("after echo: %+v", seq)
	}
	select {
	case ev := <-b.Events():
		t.Fatalf("second echo: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	page, _, _ := b.History(t.Context(), th, "", 1)
	if page[0].ID != added.Message.ID {
		t.Error("sent message missing from history")
	}
	if err := b.Send(t.Context(), 12345, "hi", b.NewOTID()); err == nil {
		t.Error("send to unknown thread succeeded")
	}
}

func TestSeqIDsFollowTheirDeltas(t *testing.T) {
	b := newBackend(t, Options{IncomingEvery: time.Millisecond})
	_, threads, _ := b.Snapshot(t.Context())
	go func() { _ = b.Connect(t.Context()) }()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() { _ = b.Send(t.Context(), threads[i%len(threads)].ID, "hi", b.NewOTID()) })
	}
	wg.Wait()

	var seq int64 = 500
	pending := false // a MessageAdded is waiting for its SeqAdvanced
	for seq < 560 {
		switch ev := next(t, b).(type) {
		case core.MessageAdded:
			if pending {
				t.Fatal("two deltas without a SeqAdvanced between")
			}
			pending = true
		case core.SeqAdvanced:
			if !pending || ev.SeqID != seq+1 {
				t.Fatalf("SeqAdvanced %d after %d, pending=%v", ev.SeqID, seq, pending)
			}
			seq, pending = ev.SeqID, false
		}
	}
}

func TestIncomingAndMarkRead(t *testing.T) {
	b := newBackend(t, Options{IncomingEvery: time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- b.Connect(ctx) }()

	if ev := next(t, b); ev != (core.StatusChanged{Status: core.Connected}) {
		t.Fatalf("first event %+v", ev)
	}
	added, ok := next(t, b).(core.MessageAdded)
	if !ok || added.Message.Sender == me.ID || added.Message.Text == "" {
		t.Fatalf("incoming = %+v", added)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Connect = %v", err)
	}

	unread := func() bool {
		_, threads, _ := b.Snapshot(t.Context())
		for _, th := range threads {
			if th.ID == added.Message.Thread {
				return th.Unread
			}
		}
		t.Fatal("thread gone")
		return false
	}
	if !unread() {
		t.Error("incoming message didn't mark the thread unread")
	}
	if err := b.MarkRead(t.Context(), added.Message.Thread, added.Message.ID); err != nil {
		t.Fatal(err)
	}
	if unread() {
		t.Error("MarkRead didn't clear unread")
	}
}

// Close must not panic or hang while events are blocked on a full queue.
func TestCloseWhileBlocked(t *testing.T) {
	b := New(Options{IncomingEvery: time.Microsecond, EchoDelay: time.Microsecond})
	if err := b.Restore(t.Context(), core.Session{Auth: login(t, "tester")}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { done <- b.Connect(t.Context()) }()
	_, threads, _ := b.Snapshot(t.Context())
	for range queueSize {
		_ = b.Send(t.Context(), threads[0].ID, "hi", b.NewOTID())
	}
	time.Sleep(20 * time.Millisecond) // let the queue fill

	b.Close()
	b.Close()
	if err := <-done; err != nil {
		t.Errorf("Connect after Close = %v", err)
	}
	n := 0
	for range b.Events() {
		n++
	}
	if n != queueSize {
		t.Errorf("drained %d events, want a full queue of %d", n, queueSize)
	}
	if err := b.Send(t.Context(), threads[0].ID, "hi", b.NewOTID()); !errors.Is(err, errClosed) {
		t.Errorf("Send after Close = %v", err)
	}
}
