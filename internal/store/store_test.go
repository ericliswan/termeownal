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

package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"termeownal/internal/core"
)

// Made-up IDs. A 1:1 thread's key is the other user's ID.
const (
	meID  core.UserID   = 1
	anaID core.UserID   = 2
	benID core.UserID   = 3
	dm    core.ThreadID = 2
	group core.ThreadID = 900
)

var (
	me  = core.User{ID: meID, Username: "me", Name: "Me Myself"}
	ana = core.User{ID: anaID, Username: "ana", Name: "Ana A"}
	ben = core.User{ID: benID, Username: "ben", Name: "Ben B"}
)

// at returns a time min minutes after a fixed instant.
func at(min int) time.Time { return time.UnixMilli(1_700_000_000_000 + int64(min)*60_000) }

func msg(id string, thread core.ThreadID, from core.UserID, min int) core.Message {
	return core.Message{ID: core.MessageID(id), Thread: thread, Sender: from, At: at(min), Text: "text " + id, ContentType: "TEXT"}
}

// local is a message being sent from this client.
func local(otid string, min int) core.Message {
	m := msg("local:"+otid, dm, meID, min)
	m.OTID, m.State = otid, core.Sending
	return m
}

// echo is the server's copy of local(otid, min).
func echo(id, otid string, min int) core.Message {
	m := msg(id, dm, meID, min)
	m.Text, m.OTID = "text local:"+otid, otid
	return m
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), Memory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SetMe(t.Context(), me); err != nil {
		t.Fatal(err)
	}
	return s
}

// withDM returns a store holding the 1:1 thread with msgs.
func withDM(t *testing.T, msgs ...core.Message) *Store {
	t.Helper()
	s := newStore(t)
	must(t, s.PutThreads(t.Context(), []core.Thread{{ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(0), Messages: msgs}}))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// history returns every cached message of thread.
func history(t *testing.T, s *Store, thread core.ThreadID) []core.Message {
	t.Helper()
	msgs, _, err := s.History(t.Context(), thread, "", 1000)
	must(t, err)
	return msgs
}

func ids(msgs []core.Message) []core.MessageID {
	out := make([]core.MessageID, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func wantIDs(t *testing.T, msgs []core.Message, want ...core.MessageID) {
	t.Helper()
	if got := ids(msgs); !slices.Equal(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
}

// thread returns the cached thread with id, without messages.
func thread(t *testing.T, s *Store, id core.ThreadID) core.Thread {
	t.Helper()
	_, threads, err := s.Snapshot(t.Context(), 0)
	must(t, err)
	for _, th := range threads {
		if th.ID == id {
			return th
		}
	}
	t.Fatalf("thread %d not cached", id)
	return core.Thread{}
}

// dump returns every row of every table, for comparing whole databases.
func dump(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	ctx := t.Context()
	out := map[string][]string{}
	for _, table := range tableNames(t, db) {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+table)
		must(t, err)
		cols, err := rows.Columns()
		must(t, err)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			must(t, rows.Scan(ptrs...))
			out[table] = append(out[table], fmt.Sprint(vals...))
		}
		must(t, rows.Err())
		must(t, rows.Close())
		slices.Sort(out[table])
	}
	return out
}

func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT name FROM sqlite_schema WHERE type = 'table'")
	must(t, err)
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		must(t, rows.Scan(&n))
		names = append(names, n)
	}
	must(t, rows.Err())
	return names
}

func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	must(t, db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&v))
	return v
}

func TestMigrateFromEveryVersion(t *testing.T) {
	ctx := t.Context()
	for from := range len(migrations) + 1 {
		t.Run(fmt.Sprint(from), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			db, err := sql.Open("sqlite", path)
			must(t, err)
			must(t, migrate(ctx, db, from))
			must(t, db.Close())

			for range 2 { // the second Open has nothing to migrate
				s, err := Open(ctx, path)
				must(t, err)
				if v := schemaVersion(t, s.db); v != len(migrations) {
					t.Errorf("version = %d, want %d", v, len(migrations))
				}
				must(t, s.SetMe(ctx, me))
				must(t, s.Close())
			}
		})
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	_, err = db.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", len(migrations)+1))
	must(t, err)
	must(t, db.Close())

	if s, err := Open(t.Context(), path); err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded on a newer schema")
	}
}

func TestOpenRejectsQueryInPath(t *testing.T) {
	if s, err := Open(t.Context(), filepath.Join(t.TempDir(), "cache.db?mode=ro")); err == nil {
		_ = s.Close()
		t.Fatal("Open accepted a path with '?'")
	}
}

func TestOpenIsPrivateAndDurable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix file modes")
	}
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "cache.db")

	for range 2 { // the second time, the file exists with a wider mode
		s, err := Open(ctx, path)
		must(t, err)
		must(t, s.SetMe(ctx, me))
		for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600, path + "-wal": 0o600} {
			fi, err := os.Stat(p)
			must(t, err)
			if got := fi.Mode().Perm(); got != want {
				t.Errorf("%s mode = %o, want %o", filepath.Base(p), got, want)
			}
		}
		var journal string
		var sync int
		must(t, s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal))
		must(t, s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync))
		if journal != "wal" || sync != 2 {
			t.Errorf("journal_mode = %s, synchronous = %d; want wal, 2 (FULL)", journal, sync)
		}
		must(t, s.Close())
		must(t, os.Chmod(path, 0o644))
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	must(t, s.PutThreads(ctx, []core.Thread{
		{
			ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(10), Unread: true,
			Messages: []core.Message{msg("a1", dm, anaID, 1), msg("a2", dm, meID, 5), msg("a3", dm, anaID, 10)},
		},
		{
			ID: group, Title: "Group", IsGroup: true, Members: []core.User{ben, ana}, LastActivity: at(20),
			Messages: []core.Message{msg("g1", group, benID, 20)},
		},
	}))

	gotMe, got, err := s.Snapshot(ctx, 2)
	must(t, err)
	want := []core.Thread{
		{
			ID: group, Title: "Group", IsGroup: true, Members: []core.User{ben, ana}, LastActivity: at(20),
			Messages: []core.Message{msg("g1", group, benID, 20)},
		},
		{
			ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(10), Unread: true,
			Messages: []core.Message{msg("a2", dm, meID, 5), msg("a3", dm, anaID, 10)},
		},
	}
	if gotMe != me || !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot =\n%v\n%+v\nwant\n%v\n%+v", gotMe, got, me, want)
	}
}

func TestSnapshotOfEmptyCache(t *testing.T) {
	s, err := Open(t.Context(), Memory)
	must(t, err)
	defer func() { _ = s.Close() }()
	gotMe, threads, err := s.Snapshot(t.Context(), 20)
	if err != nil || gotMe != (core.User{}) || len(threads) != 0 {
		t.Errorf("Snapshot = %v, %v, %v; want zero user, no threads", gotMe, threads, err)
	}
}

func TestPutThreadsUpdatesThread(t *testing.T) {
	s := withDM(t, msg("a1", dm, anaID, 1))
	ctx := t.Context()
	renamed := core.Thread{ID: dm, Title: "Ana!", Members: []core.User{{ID: anaID, Username: "ana2", Name: "Ana B"}}, LastActivity: at(-5)}
	must(t, s.PutThreads(ctx, []core.Thread{renamed}))

	got := thread(t, s, dm)
	renamed.LastActivity = at(1) // never moves back
	if !reflect.DeepEqual(got, renamed) {
		t.Errorf("thread = %+v, want %+v", got, renamed)
	}
	wantIDs(t, history(t, s, dm), "a1")
}

func TestReplayChangesNothing(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	steps := []func() error{
		func() error {
			return s.PutThreads(ctx, []core.Thread{{
				ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(3), Unread: true,
				Messages: []core.Message{msg("a1", dm, anaID, 1), msg("a2", dm, meID, 2), msg("a3", dm, anaID, 3)},
			}})
		},
		func() error { return s.AddMessage(ctx, msg("a4", dm, anaID, 4)) },
		func() error { return s.AddMessage(ctx, local("o1", 5)) },
		func() error { return s.SetSendState(ctx, "local:o1", core.SendFailed) },
		func() error { return s.AddMessage(ctx, echo("a5", "o1", 5)) },
		func() error { return s.MarkRead(ctx, dm, at(4)) },
		func() error {
			return s.PutHistory(ctx, dm, "a1", []core.Message{msg("a0", dm, anaID, -2), msg("a0b", dm, meID, -1)}, false)
		},
	}
	for _, step := range steps {
		must(t, step())
	}
	first := dump(t, s.db)
	for _, step := range steps {
		must(t, step())
	}
	if again := dump(t, s.db); !reflect.DeepEqual(first, again) {
		t.Errorf("replaying changed the cache:\n%v\nthen\n%v", first, again)
	}
	wantIDs(t, history(t, s, dm), "a0", "a0b", "a1", "a2", "a3", "a4", "a5")
	if thread(t, s, dm).Unread {
		t.Error("thread unread after replay")
	}
}

func TestUnread(t *testing.T) {
	s := withDM(t, msg("a1", dm, anaID, 1))
	ctx := t.Context()
	steps := []struct {
		name string
		do   func() error
		want bool
	}{
		{"own message", func() error { return s.AddMessage(ctx, msg("a2", dm, meID, 2)) }, false},
		{"incoming", func() error { return s.AddMessage(ctx, msg("a3", dm, anaID, 3)) }, true},
		{"read up to before it", func() error { return s.MarkRead(ctx, dm, at(2)) }, true},
		{"read up to it", func() error { return s.MarkRead(ctx, dm, at(3)) }, false},
		{"incoming replayed", func() error { return s.AddMessage(ctx, msg("a3", dm, anaID, 3)) }, false},
		{"local message", func() error { return s.AddMessage(ctx, local("o1", 4)) }, false},
		{"its echo", func() error { return s.AddMessage(ctx, echo("a4", "o1", 4)) }, false},
		{"newer incoming", func() error { return s.AddMessage(ctx, msg("a5", dm, anaID, 5)) }, true},
		{"older read again", func() error { return s.MarkRead(ctx, dm, at(3)) }, true},
		{"snapshot says read", func() error {
			return s.PutThreads(ctx, []core.Thread{{ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(5)}})
		}, false},
	}
	for _, st := range steps {
		must(t, st.do())
		if got := thread(t, s, dm).Unread; got != st.want {
			t.Fatalf("after %s: unread = %v, want %v", st.name, got, st.want)
		}
	}
}

func TestSendReconcile(t *testing.T) {
	sent := func(t *testing.T, s *Store) {
		t.Helper()
		msgs := history(t, s, dm)
		wantIDs(t, msgs, "a1", "m1")
		if len(msgs) == 2 && (msgs[1].State != core.Sent || msgs[1].OTID != "o1") {
			t.Errorf("echo = %+v, want Sent with OTID o1", msgs[1])
		}
	}
	t.Run("echo", func(t *testing.T) {
		s := withDM(t, msg("a1", dm, anaID, 1))
		must(t, s.AddMessage(t.Context(), local("o1", 2)))
		wantIDs(t, history(t, s, dm), "a1", "local:o1")
		must(t, s.AddMessage(t.Context(), echo("m1", "o1", 2)))
		sent(t, s)
	})
	t.Run("echo after failure and retry", func(t *testing.T) {
		s := withDM(t, msg("a1", dm, anaID, 1))
		ctx := t.Context()
		must(t, s.AddMessage(ctx, local("o1", 2)))
		must(t, s.SetSendState(ctx, "local:o1", core.SendFailed))
		if st := history(t, s, dm)[1].State; st != core.SendFailed {
			t.Fatalf("state = %v, want SendFailed", st)
		}
		must(t, s.SetSendState(ctx, "local:o1", core.Sending))
		must(t, s.SetSendState(ctx, "local:o1", core.SendFailed))
		must(t, s.AddMessage(ctx, echo("m1", "o1", 2)))
		must(t, s.SetSendState(ctx, "local:o1", core.SendFailed)) // a late timeout
		sent(t, s)
	})
	t.Run("duplicate echo", func(t *testing.T) {
		s := withDM(t, msg("a1", dm, anaID, 1))
		must(t, s.AddMessage(t.Context(), local("o1", 2)))
		must(t, s.AddMessage(t.Context(), echo("m1", "o1", 2)))
		must(t, s.AddMessage(t.Context(), echo("m1", "o1", 2)))
		sent(t, s)
	})
	t.Run("echo before local copy", func(t *testing.T) {
		s := withDM(t, msg("a1", dm, anaID, 1))
		must(t, s.AddMessage(t.Context(), echo("m1", "o1", 2)))
		must(t, s.AddMessage(t.Context(), local("o1", 2)))
		sent(t, s)
	})
	t.Run("echo in a snapshot", func(t *testing.T) {
		s := withDM(t, msg("a1", dm, anaID, 1))
		must(t, s.AddMessage(t.Context(), local("o1", 2)))
		must(t, s.PutThreads(t.Context(), []core.Thread{{
			ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(2),
			Messages: []core.Message{msg("a1", dm, anaID, 1), echo("m1", "o1", 2)},
		}}))
		sent(t, s)
	})
	t.Run("only the echo marks sent", func(t *testing.T) {
		s := withDM(t)
		if err := s.SetSendState(t.Context(), "local:o1", core.Sent); err == nil {
			t.Error("SetSendState(Sent) succeeded")
		}
	})
	t.Run("reaction log entries share an OTID", func(t *testing.T) {
		s := withDM(t, msg("a1", dm, anaID, 1))
		ctx := t.Context()
		r1, r2 := msg("r1", dm, anaID, 2), msg("r2", dm, meID, 3)
		r1.ContentType, r1.OTID = "REACTION_LOG_XMAT", "262"
		r2.ContentType, r2.OTID = "REACTION_LOG_XMAT", "262"
		must(t, s.AddMessage(ctx, r1))
		must(t, s.AddMessage(ctx, r2))
		wantIDs(t, history(t, s, dm), "a1", "r1", "r2")
	})
}

func TestHistoryPaging(t *testing.T) {
	ctx := t.Context()
	s := withDM(t, msg("m5", dm, anaID, 5), msg("m6", dm, meID, 6), msg("m7", dm, anaID, 7),
		msg("m8", dm, anaID, 8), msg("m9", dm, meID, 8)) // m8 and m9 share a time
	page := func(before core.MessageID, n int, wantMore bool, want ...core.MessageID) {
		t.Helper()
		msgs, more, err := s.History(ctx, dm, before, n)
		must(t, err)
		wantIDs(t, msgs, want...)
		if more != wantMore {
			t.Errorf("History(%q, %d) more = %v, want %v", before, n, more, wantMore)
		}
	}
	page("", 1, true, "m9")
	page("m9", 1, true, "m8")
	page("", 3, true, "m7", "m8", "m9")
	page("m7", 3, true, "m5", "m6") // the server may have more

	must(t, s.PutHistory(ctx, dm, "m5", []core.Message{msg("m1", dm, anaID, 1), msg("m2", dm, anaID, 2), msg("m3", dm, meID, 3), msg("m4", dm, anaID, 4)}, false))
	page("m5", 3, true, "m2", "m3", "m4")
	page("m2", 3, false, "m1")
	page("m1", 3, false)
	page("unknown", 3, false)

	_, more, err := s.History(ctx, group, "", 3)
	must(t, err)
	if !more {
		t.Error("History of an uncached thread: more = false, want true")
	}
}

func TestGapDropsOlderMessages(t *testing.T) {
	cached := []core.Message{msg("m1", dm, anaID, 1), msg("m2", dm, meID, 2), msg("m3", dm, anaID, 4)}
	newest := []core.Message{msg("m8", dm, anaID, 8), msg("m9", dm, anaID, 9)}
	overlapping := []core.Message{msg("m3", dm, anaID, 4), msg("m4", dm, anaID, 5)}
	snapshot := func(s *Store, msgs []core.Message) error {
		return s.PutThreads(context.Background(), []core.Thread{{ID: dm, Title: "Ana", Members: []core.User{ana}, LastActivity: at(9), Messages: msgs}})
	}
	newestHistory := func(s *Store, msgs []core.Message) error {
		return s.PutHistory(context.Background(), dm, "", msgs, true)
	}
	for name, put := range map[string]func(*Store, []core.Message) error{"PutThreads": snapshot, "PutHistory": newestHistory} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			s := withDM(t, cached...)
			must(t, s.PutHistory(ctx, dm, "m1", nil, false))
			must(t, s.AddMessage(ctx, local("o1", 3)))
			must(t, s.SetSendState(ctx, "local:o1", core.SendFailed))

			must(t, put(s, overlapping))
			wantIDs(t, history(t, s, dm), "m1", "m2", "local:o1", "m3", "m4")
			must(t, put(s, nil))
			wantIDs(t, history(t, s, dm), "m1", "m2", "local:o1", "m3", "m4")

			must(t, put(s, newest))
			msgs, more, err := s.History(ctx, dm, "", 10)
			must(t, err)
			wantIDs(t, msgs, "local:o1", "m8", "m9") // the unsent message stays
			if !more {
				t.Error("more = false after a gap, want true")
			}

			// A page requested before the gap was trimmed is dropped.
			must(t, s.PutHistory(ctx, dm, "m1", []core.Message{msg("m0", dm, anaID, 0)}, false))
			wantIDs(t, history(t, s, dm), "local:o1", "m8", "m9")
		})
	}
}

func TestSetMe(t *testing.T) {
	s := withDM(t, msg("a1", dm, anaID, 1))
	ctx := t.Context()

	renamed := me
	renamed.Name = "Me Renamed"
	must(t, s.SetMe(ctx, renamed))
	gotMe, threads, err := s.Snapshot(ctx, 20)
	must(t, err)
	if gotMe != renamed || len(threads) != 1 || len(threads[0].Messages) != 1 {
		t.Fatalf("after renaming: %v, %+v; want the cache kept", gotMe, threads)
	}

	other := core.User{ID: 77, Username: "other", Name: "Other"}
	must(t, s.SetMe(ctx, other))
	for table, rows := range dump(t, s.db) {
		if table != "account" && len(rows) > 0 {
			t.Errorf("table %s kept %d rows after switching account", table, len(rows))
		}
	}
	if gotMe, _, _ := s.Snapshot(ctx, 20); gotMe != other {
		t.Errorf("account = %v, want %v", gotMe, other)
	}
	if len(tableNames(t, s.db)) != len(tables) {
		t.Errorf("tables = %v, want %v: update tables", tableNames(t, s.db), tables)
	}
}
