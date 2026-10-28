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
	"errors"
	"fmt"
	"slices"
	"time"

	"termeownal/internal/core"
)

// Every write below can be repeated without changing the result: live
// events are delivered at least once (AGENTS.md §6.4).

// notMe matches messages someone else sent. With no account cached it
// matches all of them.
const notMe = "sender_fbid IS NOT (SELECT fbid FROM account)"

const messageCols = "id, thread_key, sender_fbid, ts_ms, content_type, text, otid, send_state"

// Snapshot returns the cached account (the zero User if none) and every
// cached thread, most recently active first, each with its newest n messages,
// oldest first.
func (s *Store) Snapshot(ctx context.Context, n int) (core.User, []core.Thread, error) {
	var me core.User
	var threads []core.Thread
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT fbid, username, display_name FROM account").
			Scan(&me.ID, &me.Username, &me.Name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if threads, err = readThreads(ctx, tx); err != nil {
			return err
		}
		for i := range threads {
			if threads[i].Messages, err = messagesBefore(ctx, tx, threads[i].ID, "", n); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return core.User{}, nil, fmt.Errorf("read cache: %w", err)
	}
	return me, threads, nil
}

// readThreads returns every thread with its members, without messages.
func readThreads(ctx context.Context, tx *sql.Tx) ([]core.Thread, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT thread_key, title, is_group, last_activity_ms, unread FROM threads
		ORDER BY last_activity_ms DESC, thread_key`)
	if err != nil {
		return nil, err
	}
	var threads []core.Thread
	index := map[core.ThreadID]int{}
	for rows.Next() {
		var t core.Thread
		var ms int64
		if err := rows.Scan(&t.ID, &t.Title, &t.IsGroup, &ms, &t.Unread); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.LastActivity = time.UnixMilli(ms)
		index[t.ID] = len(threads)
		threads = append(threads, t)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}

	// rowid order is the order PutThreads inserted the members in.
	rows, err = tx.QueryContext(ctx, `
		SELECT m.thread_key, u.fbid, u.username, u.full_name
		FROM thread_members m JOIN users u ON u.fbid = m.user_fbid
		ORDER BY m.thread_key, m.rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key core.ThreadID
		var u core.User
		if err := rows.Scan(&key, &u.ID, &u.Username, &u.Name); err != nil {
			return nil, err
		}
		if i, ok := index[key]; ok {
			threads[i].Members = append(threads[i].Members, u)
		}
	}
	return threads, rows.Err()
}

// PutThreads stores threads from a snapshot or a thread update: their
// details, members and unread state, and the messages that come with them.
// Cached threads missing from the list are kept.
func (s *Store) PutThreads(ctx context.Context, threads []core.Thread) error {
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		for _, t := range threads {
			if err := putThread(ctx, tx, t); err != nil {
				return fmt.Errorf("thread %d: %w", t.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("cache threads: %w", err)
	}
	return nil
}

func putThread(ctx context.Context, tx *sql.Tx, t core.Thread) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO threads (thread_key, title, is_group, last_activity_ms, unread) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (thread_key) DO UPDATE SET
			title = excluded.title,
			is_group = excluded.is_group,
			last_activity_ms = max(last_activity_ms, excluded.last_activity_ms),
			unread = excluded.unread`,
		t.ID, t.Title, t.IsGroup, t.LastActivity.UnixMilli(), t.Unread)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM thread_members WHERE thread_key = ?", t.ID); err != nil {
		return err
	}
	for _, u := range t.Members {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO users (fbid, username, full_name) VALUES (?, ?, ?)
			ON CONFLICT (fbid) DO UPDATE SET username = excluded.username, full_name = excluded.full_name`,
			u.ID, u.Username, u.Name)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO thread_members (thread_key, user_fbid) VALUES (?, ?)
			ON CONFLICT DO NOTHING`, t.ID, u.ID)
		if err != nil {
			return err
		}
	}
	if err := trimGap(ctx, tx, t.ID, t.Messages); err != nil {
		return err
	}
	for _, m := range t.Messages {
		if _, err := putMessage(ctx, tx, m); err != nil {
			return err
		}
	}
	return nil
}

// AddMessage stores a live message, or a local one being sent (State is not
// Sent, and ID is chosen by the caller). A new message from someone else
// marks its thread unread. The echo of a message sent from here (same OTID)
// replaces the local copy.
func (s *Store) AddMessage(ctx context.Context, m core.Message) error {
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		added, err := putMessage(ctx, tx, m)
		if err != nil || !added {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE threads SET unread = 1 WHERE thread_key = ? AND ? IS NOT (SELECT fbid FROM account)",
			m.Thread, m.Sender)
		return err
	})
	if err != nil {
		return fmt.Errorf("cache message: %w", err)
	}
	return nil
}

// putMessage stores m unless it is cached already, and reports whether it
// was added.
func putMessage(ctx context.Context, tx *sql.Tx, m core.Message) (bool, error) {
	if m.OTID != "" {
		if m.State == core.Sent {
			// An echo: drop the local copy, whether pending or failed.
			_, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE otid = ? AND send_state <> 0 AND id <> ?", m.OTID, m.ID)
			if err != nil {
				return false, err
			}
		} else {
			// The echo can win the race against storing the local copy.
			var echoed bool
			err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM messages WHERE otid = ? AND send_state = 0)", m.OTID).
				Scan(&echoed)
			if err != nil || echoed {
				return false, err
			}
		}
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO messages (`+messageCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		m.ID, m.Thread, m.Sender, m.At.UnixMilli(), m.ContentType, m.Text, m.OTID, m.State)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE threads SET last_activity_ms = max(last_activity_ms, ?) WHERE thread_key = ?",
		m.At.UnixMilli(), m.Thread)
	return err == nil, err
}

// trimGap keeps a thread's cached messages contiguous when page, its newest
// messages from the server, is stored. If page doesn't reach back to a
// cached message, messages between them may be missing, so the sent ones
// older than page are dropped; paging back fetches them again. Local
// messages not yet sent are kept.
func trimGap(ctx context.Context, tx *sql.Tx, thread core.ThreadID, page []core.Message) error {
	// shortcut: a thread snapshot without messages leaves cached messages alone, so a gap can hide messages; trim by LastActivity if a backend ever returns threads without messages.
	if len(page) == 0 {
		return nil
	}
	oldest := slices.MinFunc(page, func(a, b core.Message) int { return a.At.Compare(b.At) })
	var overlaps bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM messages WHERE id = ?)", oldest.ID).Scan(&overlaps)
	if err != nil || overlaps {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM messages WHERE thread_key = ? AND send_state = 0 AND (ts_ms, id) < (?, ?)",
		thread, oldest.At.UnixMilli(), oldest.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE threads SET history_complete = 0 WHERE thread_key = ?", thread)
	return err
}

// MarkRead records that a thread was read up to until. The thread stays
// unread if someone else wrote after until, so an old read arriving late
// changes nothing.
func (s *Store) MarkRead(ctx context.Context, thread core.ThreadID, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads SET unread = 0
		WHERE thread_key = ?1 AND NOT EXISTS (
			SELECT 1 FROM messages WHERE thread_key = ?1 AND ts_ms > ?2 AND `+notMe+`)`,
		thread, until.UnixMilli())
	if err != nil {
		return fmt.Errorf("cache read state: %w", err)
	}
	return nil
}

// SetSendState updates a local message that hasn't been sent: Sending on a
// retry, SendFailed when sending fails. Only its echo marks it Sent, so once
// the echo has arrived this does nothing.
func (s *Store) SetSendState(ctx context.Context, id core.MessageID, state core.SendState) error {
	if state == core.Sent {
		return errors.New("cache send state: only the echo marks a message sent")
	}
	_, err := s.db.ExecContext(ctx, "UPDATE messages SET send_state = ? WHERE id = ? AND send_state <> 0", state, id)
	if err != nil {
		return fmt.Errorf("cache send state: %w", err)
	}
	return nil
}

// PutHistory stores a page of a thread's history fetched from the server:
// msgs, oldest first, are the messages before message before (the newest
// ones if before is ""), and more says whether older ones exist. A page
// whose before is no longer cached is ignored.
func (s *Store) PutHistory(ctx context.Context, thread core.ThreadID, before core.MessageID, msgs []core.Message, more bool) error {
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		if before == "" {
			if err := trimGap(ctx, tx, thread, msgs); err != nil {
				return err
			}
		} else {
			var cached bool
			err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM messages WHERE id = ? AND thread_key = ?)", before, thread).
				Scan(&cached)
			if err != nil || !cached {
				return err
			}
		}
		for _, m := range msgs {
			if _, err := putMessage(ctx, tx, m); err != nil {
				return err
			}
		}
		if more {
			return nil
		}
		_, err := tx.ExecContext(ctx, "UPDATE threads SET history_complete = 1 WHERE thread_key = ?", thread)
		return err
	})
	if err != nil {
		return fmt.Errorf("cache history: %w", err)
	}
	return nil
}

// History returns up to n cached messages of a thread older than message
// before (the newest if before is ""), oldest first. more reports whether
// older messages exist, cached or on the server.
func (s *Store) History(ctx context.Context, thread core.ThreadID, before core.MessageID, n int) ([]core.Message, bool, error) {
	var msgs []core.Message
	var more bool
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		if msgs, err = messagesBefore(ctx, tx, thread, before, n+1); err != nil {
			return err
		}
		if len(msgs) > n {
			msgs, more = msgs[1:], true
			return nil
		}
		var complete bool
		err = tx.QueryRowContext(ctx, "SELECT history_complete FROM threads WHERE thread_key = ?", thread).Scan(&complete)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		more = !complete
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("read cached history: %w", err)
	}
	return msgs, more, nil
}

// messagesBefore returns up to n of a thread's messages older than message
// before (the newest if before is ""), oldest first.
func messagesBefore(ctx context.Context, tx *sql.Tx, thread core.ThreadID, before core.MessageID, n int) ([]core.Message, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+messageCols+` FROM messages
		WHERE thread_key = ?1 AND (?2 = '' OR (ts_ms, id) < (SELECT ts_ms, id FROM messages WHERE id = ?2))
		ORDER BY ts_ms DESC, id DESC LIMIT ?3`,
		thread, before, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var msgs []core.Message
	for rows.Next() {
		var m core.Message
		var ms int64
		if err := rows.Scan(&m.ID, &m.Thread, &m.Sender, &ms, &m.ContentType, &m.Text, &m.OTID, &m.State); err != nil {
			return nil, err
		}
		m.At = time.UnixMilli(ms)
		msgs = append(msgs, m)
	}
	slices.Reverse(msgs)
	return msgs, rows.Err()
}
