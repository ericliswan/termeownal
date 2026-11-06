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
	"slices"
	"testing"

	"termeownal/internal/core"
	"termeownal/internal/ui"
)

func ids(msgs []core.Message) []core.MessageID {
	out := make([]core.MessageID, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

// TestHistory pages back through a thread as the UI does, then again with
// the backend gone: every page but the newest comes from the cache.
func TestHistory(t *testing.T) {
	b := restoredFake(t)
	st := openStore(t)
	ctx := context.Background()
	me, threads, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMe(ctx, me); err != nil {
		t.Fatal(err)
	}
	if err := st.PutThreads(ctx, threads); err != nil {
		t.Fatal(err)
	}
	a := actions{ctx: ctx, b: b, st: st}

	// The snapshot caches each thread's newest 20 of 180 messages. Pages of
	// 14 are then part cached, part fetched, and the last is short, so
	// offline it can only come from the cache.
	const n = 14
	type page struct {
		before core.MessageID
		ids    []core.MessageID
		more   bool
	}
	var pages []page
	for before := core.MessageID(""); ; {
		msgs, more, err := a.history(101, before, n)
		if err != nil {
			t.Fatal(err)
		}
		want, wantMore, err := b.History(ctx, 101, before, n)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(msgs), ids(want)) || more != wantMore {
			t.Fatalf("page before %q: got %v (more %v), want %v (more %v)", before, ids(msgs), more, ids(want), wantMore)
		}
		pages = append(pages, page{before, ids(msgs), more})
		if !more {
			break
		}
		before = msgs[0].ID
	}

	b.Close()
	if _, _, err := a.history(101, "", n); err == nil {
		t.Error("loaded the newest page without the backend")
	}
	for _, p := range pages[1:] {
		msgs, more, err := a.history(101, p.before, n)
		if err != nil || !slices.Equal(ids(msgs), p.ids) || more != p.more {
			t.Errorf("offline page before %q: got %v (more %v, err %v), want %v (more %v)", p.before, ids(msgs), more, err, p.ids, p.more)
		}
	}
}

// TestFailedSendSurvivesRestart sends with the backend gone: the local copy
// is cached first, so the next start shows it as failed, ready to retry.
func TestFailedSendSurvivesRestart(t *testing.T) {
	b := restoredFake(t)
	st := openStore(t)
	ctx := context.Background()
	_, threads, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutThreads(ctx, threads); err != nil {
		t.Fatal(err)
	}
	b.Close()
	local := core.Message{ID: "local:otid-1", Thread: 101, Sender: 1, At: testNow, Text: "hi", OTID: "otid-1", State: core.Sending}
	sent, ok := actions{ctx: ctx, b: b, st: st}.Send(local)().(ui.SentMsg)
	if !ok || sent.Err == nil {
		t.Fatalf("Send with the backend closed = %+v, want an error", sent)
	}

	if err := st.FailUnsent(ctx); err != nil { // as the next start does
		t.Fatal(err)
	}
	msgs, _, err := st.History(ctx, 101, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(msgs, func(m core.Message) bool { return m.ID == local.ID })
	if i < 0 || msgs[i].State != core.SendFailed || msgs[i].OTID != local.OTID {
		t.Fatalf("cached messages = %+v, want %s failed", msgs, local.ID)
	}
}

func TestMarkReadClearsCachedUnread(t *testing.T) {
	b := restoredFake(t)
	st := openStore(t)
	ctx := context.Background()
	_, threads, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutThreads(ctx, threads); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(threads, func(t core.Thread) bool { return t.Unread && len(t.Messages) > 0 })
	if i < 0 {
		t.Fatal("fake has no unread thread with messages")
	}
	upTo := threads[i].Messages[len(threads[i].Messages)-1]
	if msg := (actions{ctx: ctx, b: b, st: st}).MarkRead(upTo)(); msg != nil {
		t.Fatalf("MarkRead = %+v, want nil", msg)
	}
	_, cached, err := st.Snapshot(ctx, snapshotMessages)
	if err != nil {
		t.Fatal(err)
	}
	if j := slices.IndexFunc(cached, func(t core.Thread) bool { return t.ID == upTo.Thread }); j < 0 || cached[j].Unread {
		t.Fatalf("cached thread %d still unread", upTo.Thread)
	}
}

func TestTypingFollowsTheSetting(t *testing.T) {
	b := restoredFake(t)
	if cmd := (actions{ctx: t.Context(), b: b}).Typing(1, true); cmd != nil {
		t.Fatal("typing sent with send_typing off")
	}
	_, threads, _ := b.Snapshot(t.Context())
	cmd := actions{ctx: t.Context(), b: b, sendTyping: true}.Typing(threads[0].ID, true)
	if cmd == nil || cmd() != nil {
		t.Fatal("typing not sent with send_typing on")
	}
}
