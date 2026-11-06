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
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"

	"termeownal/internal/backend/fake"
	"termeownal/internal/core"
	"termeownal/internal/store"
	"termeownal/internal/ui"
)

var testNow = time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)

// recorder stands in for the UI. Holding mu stalls it, like a busy terminal.
type recorder struct {
	mu   sync.Mutex
	msgs []tea.Msg
	// check, if set, sees each message before it's recorded.
	check func(tea.Msg)
}

func (r *recorder) Send(msg tea.Msg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.check != nil {
		r.check(msg)
	}
	r.msgs = append(r.msgs, msg)
}

func (r *recorder) sent() []tea.Msg {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs)
}

// sentOf returns the messages of type T among msgs.
func sentOf[T any](msgs []tea.Msg) []T {
	var out []T
	for _, msg := range msgs {
		if m, ok := msg.(T); ok {
			out = append(out, m)
		}
	}
	return out
}

// restoredFake returns a logged-in fake backend with a fixed clock and no
// incoming messages.
func restoredFake(t *testing.T) *fake.Backend {
	t.Helper()
	ctx := context.Background()
	step, err := fake.Authenticator{}.Submit(ctx, map[string]string{"username": "u", "password": "p"})
	if err != nil {
		t.Fatal(err)
	}
	b := fake.New(fake.Options{Now: func() time.Time { return testNow }})
	if err := b.Restore(ctx, core.Session{Auth: step.Result.Auth}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), store.Memory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newTestPump(t *testing.T) (*pump, *fake.Backend, *recorder) {
	t.Helper()
	b := restoredFake(t)
	rec := &recorder{}
	return newPump(b, openStore(t), rec, zerolog.Nop()), b, rec
}

// start runs the pump and the connection as Run does, until the test ends.
func start(t *testing.T, p *pump) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { p.run(ctx) })
	wg.Go(func() { p.connectLoop(ctx) })
	t.Cleanup(func() {
		cancel()
		p.b.Close()
		wg.Wait()
	})
}

// waitFor fails the test if ok isn't true within 5 seconds.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func connected(rec *recorder) func() bool {
	return func() bool { return len(sentOf[core.StatusChanged](rec.sent())) > 0 }
}

// send sends n messages to the fake's first thread; their echoes arrive as
// live events.
func send(t *testing.T, b *fake.Backend, n int) {
	t.Helper()
	for range n {
		if err := b.Send(context.Background(), 101, "hi", b.NewOTID(), ""); err != nil {
			t.Fatal(err)
		}
	}
}

// countLive counts the cached messages that arrived as live events.
func countLive(ctx context.Context, st *store.Store) (int, error) {
	msgs, _, err := st.History(ctx, 101, "", 1000)
	n := 0
	for _, m := range msgs {
		if strings.HasPrefix(string(m.ID), "mid.$fakelive.") {
			n++
		}
	}
	return n, err
}

// seqIDs returns 1 to n.
func seqIDs(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

func TestCacheShowsFirst(t *testing.T) {
	p, b, rec := newTestPump(t)
	ctx := context.Background()
	me, _, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached := core.Thread{ID: 424242, Title: "From the cache", LastActivity: testNow.Add(-time.Hour)}
	if err := p.st.SetMe(ctx, me); err != nil {
		t.Fatal(err)
	}
	if err := p.st.PutThreads(ctx, []core.Thread{cached}); err != nil {
		t.Fatal(err)
	}

	start(t, p)
	waitFor(t, "the connection", connected(rec))
	got := rec.sent()
	first, ok1 := got[0].(ui.SnapshotMsg)
	second, ok2 := got[1].(ui.SnapshotMsg)
	if _, ok3 := got[2].(core.StatusChanged); !ok1 || !ok2 || !ok3 {
		t.Fatalf("sent %T, %T, %T; want the cache, then the backend's snapshot, then the status", got[0], got[1], got[2])
	}
	if first.Me != me || len(first.Threads) != 1 || first.Threads[0].ID != cached.ID {
		t.Errorf("first snapshot has %d threads, want only the cached one", len(first.Threads))
	}
	if len(second.Threads) != 10 {
		t.Errorf("second snapshot has %d threads, want the backend's 10", len(second.Threads))
	}
	if _, threads, err := p.st.Snapshot(ctx, 1); err != nil || len(threads) != 11 {
		t.Errorf("cache has %d threads (err %v), want the backend's 10 and the cached one", len(threads), err)
	}
}

func TestSeqSavedOnlyAfterItsEvents(t *testing.T) {
	p, b, rec := newTestPump(t)
	var mu sync.Mutex
	var saved []int64
	p.save = func(s core.Session) error {
		// The fake sends one message per seq ID, so seq ID n needs n stored.
		if n, err := countLive(context.Background(), p.st); err != nil || int64(n) != s.SeqID {
			t.Errorf("seq ID %d saved with %d live messages stored (err %v)", s.SeqID, n, err)
		}
		mu.Lock()
		defer mu.Unlock()
		saved = append(saved, s.SeqID)
		return nil
	}
	start(t, p)
	waitFor(t, "the connection", connected(rec))

	send(t, b, 20)
	waitFor(t, "20 saved seq IDs", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(saved) == 20
	})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(saved, seqIDs(20)) {
		t.Errorf("saved seq IDs %v, want 1 to 20 in order", saved)
	}
}

func TestUIGetsEventsOnceStored(t *testing.T) {
	p, b, rec := newTestPump(t)
	shown := 0 // guarded by rec.mu
	rec.check = func(msg tea.Msg) {
		if _, ok := msg.(core.MessageAdded); !ok {
			return
		}
		shown++
		if n, err := countLive(context.Background(), p.st); err != nil || n < shown {
			t.Errorf("UI got live message %d with %d stored (err %v)", shown, n, err)
		}
	}
	start(t, p)
	waitFor(t, "the connection", connected(rec))

	send(t, b, 5)
	waitFor(t, "5 messages", func() bool { return len(sentOf[core.MessageAdded](rec.sent())) == 5 })
}

func TestFullQueueBlocksInsteadOfDropping(t *testing.T) {
	p, b, rec := newTestPump(t)
	ctx := context.Background()
	start(t, p)
	waitFor(t, "the connection", connected(rec))

	rec.mu.Lock() // stall the UI, and so the pump
	unstall := sync.OnceFunc(rec.mu.Unlock)
	defer unstall()
	const n = 100 // 200 events, three times what the queue holds
	send(t, b, n)
	waitFor(t, "a full queue", func() bool { return len(b.Events()) == cap(b.Events()) })
	unstall()

	waitFor(t, "every event", func() bool { return len(sentOf[core.SeqAdvanced](rec.sent())) == n })
	var seqs []int64
	for _, ev := range sentOf[core.SeqAdvanced](rec.sent()) {
		seqs = append(seqs, ev.SeqID)
	}
	if !slices.Equal(seqs, seqIDs(n)) {
		t.Errorf("seq IDs %v, want 1 to %d in order", seqs, n)
	}
	if got := len(sentOf[core.MessageAdded](rec.sent())); got != n {
		t.Errorf("UI got %d messages, want %d", got, n)
	}
	if got, err := countLive(ctx, p.st); err != nil || got != n {
		t.Errorf("cached %d messages (err %v), want %d", got, err, n)
	}
}

func TestReplayedEventsChangeNothing(t *testing.T) {
	p, _, _ := newTestPump(t)
	ctx := context.Background()
	p.sync(ctx)
	_, threads, err := p.st.Snapshot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	th := threads[slices.IndexFunc(threads, func(t core.Thread) bool { return t.ID == 101 })]
	th.Title, th.Messages = "Renamed", nil
	at := testNow.Add(time.Minute)
	events := []core.Event{
		core.ThreadUpdated{Thread: th},
		core.MessageAdded{Message: core.Message{ID: "mid.replayed", Thread: th.ID, Sender: th.Members[0].ID, At: at, Text: "hi"}},
		core.ThreadRead{Thread: th.ID, Until: at},
		core.ReadReceipt{Thread: th.ID, User: th.Members[0].ID, UpTo: at},
		core.ReadReceipt{Thread: th.ID, User: th.Members[0].ID, UpTo: testNow}, // older, late
		core.SeqAdvanced{SeqID: 1, At: at},
	}

	var cached [2][]core.Thread
	for i := range cached {
		// The second pass is the replay after a restart.
		for _, ev := range events {
			p.apply(ctx, ev)
		}
		if _, cached[i], err = p.st.Snapshot(ctx, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(cached[0], cached[1]) {
		t.Error("replaying events changed the cache")
	}
	got := cached[1][slices.IndexFunc(cached[1], func(t core.Thread) bool { return t.ID == 101 })]
	if got.Unread {
		t.Error("thread is unread after replaying its read")
	}
	if upTo := got.ReadUpTo[th.Members[0].ID]; !upTo.Equal(at) {
		t.Errorf("member read up to %v, want %v", upTo, at)
	}
	if p.failed {
		t.Error("a write failed")
	}
}

func TestNoSeqSavedAfterAFailedWrite(t *testing.T) {
	p, b, rec := newTestPump(t)
	var saves atomic.Int32
	p.save = func(core.Session) error {
		saves.Add(1)
		return nil
	}
	start(t, p)
	waitFor(t, "the connection", connected(rec))

	if err := p.st.Close(); err != nil {
		t.Fatal(err)
	}
	send(t, b, 2)
	waitFor(t, "both seq IDs", func() bool { return len(sentOf[core.SeqAdvanced](rec.sent())) == 2 })
	if n := saves.Load(); n != 0 {
		t.Errorf("saved %d seq IDs after a failed write, want none", n)
	}
	if n := len(sentOf[ui.ErrorMsg](rec.sent())); n != 1 {
		t.Errorf("showed %d errors, want only the first", n)
	}
}

func TestSessionChangedSavesTheLogin(t *testing.T) {
	p, b, _ := newTestPump(t)
	ctx := context.Background()
	var saved []core.Session
	p.session = core.Session{SeqID: 7}
	p.save = func(s core.Session) error {
		saved = append(saved, s)
		return nil
	}
	p.failed = true // as after a failed write, so 7 is the last good seq ID
	p.apply(ctx, core.SeqAdvanced{SeqID: 8, At: testNow})
	p.apply(ctx, core.SessionChanged{})
	assertSavedLogin(t, b, saved)
}

func TestSessionSavedAtExit(t *testing.T) {
	p, b, _ := newTestPump(t)
	var saved []core.Session
	p.session = core.Session{SeqID: 7}
	p.save = func(s core.Session) error {
		saved = append(saved, s)
		return nil
	}
	// Stopped as Run stops it, so run returns once it has tried to sync.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.Close()
	p.run(ctx)
	assertSavedLogin(t, b, saved)
}

// assertSavedLogin fails unless saved is one session with b's login data and
// seq ID 7.
func assertSavedLogin(t *testing.T, b *fake.Backend, saved []core.Session) {
	t.Helper()
	auth, err := b.ExportAuth()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d sessions, want 1", len(saved))
	}
	if got := saved[0]; string(got.Auth) != string(auth) || got.SeqID != 7 {
		t.Errorf("saved seq ID %d, login data matches %v; want seq ID 7 and the backend's login data", got.SeqID, string(got.Auth) == string(auth))
	}
}

func TestResync(t *testing.T) {
	p, _, rec := newTestPump(t)
	p.apply(context.Background(), core.ResyncNeeded{})
	if n := len(sentOf[ui.SnapshotMsg](rec.sent())); n != 1 {
		t.Errorf("sent %d snapshots, want 1", n)
	}
	if len(p.connect) != 1 {
		t.Error("no connection asked for after the snapshot")
	}
}

func TestExpiredSessionOnSnapshot(t *testing.T) {
	b := fake.New(fake.Options{}) // not restored, as if the session expired
	t.Cleanup(b.Close)
	rec := &recorder{}
	p := newPump(b, openStore(t), rec, zerolog.Nop())
	p.sync(context.Background())

	got := rec.sent()
	if len(got) != 1 {
		t.Fatalf("sent %d messages, want only the auth failure", len(got))
	}
	if failed, ok := got[0].(core.AuthFailed); !ok || !errors.Is(failed.Err, core.ErrSessionExpired) {
		t.Errorf("sent %T, want AuthFailed for the expired session", got[0])
	}
	if len(p.connect) != 0 {
		t.Error("asked to connect without a snapshot")
	}
}

// flaky fails its first snapshots and connections with err.
type flaky struct {
	*fake.Backend
	err                 error
	failSnapshots       int32
	failConnects        int32
	snapshots, connects atomic.Int32
}

func (f *flaky) Snapshot(ctx context.Context) (core.User, []core.Thread, error) {
	if f.snapshots.Add(1) <= f.failSnapshots {
		return core.User{}, nil, f.err
	}
	return f.Backend.Snapshot(ctx)
}

func (f *flaky) Connect(ctx context.Context) error {
	if f.connects.Add(1) <= f.failConnects {
		return f.err
	}
	return f.Backend.Connect(ctx)
}

func newFlakyPump(t *testing.T, b *flaky) (*pump, *recorder) {
	t.Helper()
	b.Backend = restoredFake(t)
	rec := &recorder{}
	p := newPump(b, openStore(t), rec, zerolog.Nop())
	p.wait = func(int) time.Duration { return time.Millisecond }
	return p, rec
}

func TestRetriesUntilConnected(t *testing.T) {
	b := &flaky{err: errors.New("fake network down"), failSnapshots: 2, failConnects: 2}
	p, rec := newFlakyPump(t, b)
	start(t, p)
	waitFor(t, "connection", connected(rec))

	got := rec.sent()
	if n := len(sentOf[ui.ErrorMsg](got)); n != 4 {
		t.Errorf("showed %d errors, want 4", n)
	}
	if n := len(sentOf[ui.SnapshotMsg](got)); n != 2 {
		t.Errorf("sent %d snapshots, want the cache's and the backend's", n)
	}
	if s, c := b.snapshots.Load(), b.connects.Load(); s != 3 || c != 3 {
		t.Errorf("%d snapshots and %d connects, want 3 of each", s, c)
	}
}

func TestNoRetryWhenTheLoginFails(t *testing.T) {
	b := &flaky{err: fmt.Errorf("fake: %w", core.ErrSessionExpired), failConnects: 100}
	p, rec := newFlakyPump(t, b)
	start(t, p)
	waitFor(t, "auth failure", func() bool { return len(sentOf[core.AuthFailed](rec.sent())) > 0 })
	time.Sleep(20 * time.Millisecond) // time for retries, at 1 ms each
	if n := b.connects.Load(); n != 1 {
		t.Errorf("connected %d times, want 1", n)
	}
}

func TestBackoff(t *testing.T) {
	for n, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute, time.Minute} {
		for range 100 {
			if d := backoff(n); d > want || d < want*4/5 {
				t.Fatalf("backoff(%d) = %v, want %v less up to a fifth", n, d, want)
			}
		}
	}
	if d := backoff(1000); d > time.Minute {
		t.Errorf("backoff(1000) = %v", d)
	}
}

// anonymous is a backend whose snapshot has no username, as Instagram's is
// on a fast or warm start with an empty inbox.
type anonymous struct{ *fake.Backend }

func (a anonymous) Snapshot(ctx context.Context) (core.User, []core.Thread, error) {
	me, threads, err := a.Backend.Snapshot(ctx)
	return core.User{ID: me.ID}, threads, err
}

func TestSnapshotKeepsTheCachedUsername(t *testing.T) {
	ctx := context.Background()
	b := restoredFake(t)
	cached, _, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	if err := st.SetMe(ctx, cached); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	p := newPump(anonymous{b}, st, rec, zerolog.Nop())
	start(t, p)

	waitFor(t, "the backend's snapshot", func() bool { return len(sentOf[ui.SnapshotMsg](rec.sent())) == 2 })
	if got := sentOf[ui.SnapshotMsg](rec.sent())[1].Me; got != cached {
		t.Errorf("UI got %+v, want %+v", got, cached)
	}
	if got, _, err := st.Snapshot(ctx, 0); err != nil || got != cached {
		t.Errorf("cache has %+v, %v; want %+v", got, err, cached)
	}
}
