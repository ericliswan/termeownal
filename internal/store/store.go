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

// Package store is termeownal's SQLite cache of threads and messages.
// Everything in it can be fetched again, so it is safe to delete. Secrets
// never go in it; message text does, in plain text.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"termeownal/internal/core"
)

// Memory is the path of a private in-memory cache, for tests.
const Memory = ":memory:"

// pragmas run on every connection. FULL sync makes each commit durable
// before it returns, so the seq ID the app saves after a write can never be
// ahead of the stored messages, even after a power cut.
const pragmas = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)"

// migrations[i] moves the schema from version i to i+1, kept in SQLite's
// user_version. Append only: never edit a migration that has shipped.
var migrations = []string{
	// 1: what Phase 1 uses. Later phases add their columns and tables.
	`CREATE TABLE account (
		fbid         INTEGER PRIMARY KEY,
		username     TEXT NOT NULL,
		display_name TEXT NOT NULL
	);
	CREATE TABLE users (
		fbid      INTEGER PRIMARY KEY,
		username  TEXT NOT NULL,
		full_name TEXT NOT NULL
	);
	CREATE TABLE threads (
		thread_key       INTEGER PRIMARY KEY,
		title            TEXT NOT NULL,
		is_group         INTEGER NOT NULL,
		last_activity_ms INTEGER NOT NULL,
		unread           INTEGER NOT NULL,
		history_complete INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX threads_by_activity ON threads (last_activity_ms);
	CREATE TABLE thread_members (
		thread_key INTEGER NOT NULL,
		user_fbid  INTEGER NOT NULL,
		PRIMARY KEY (thread_key, user_fbid)
	);
	CREATE TABLE messages (
		id           TEXT PRIMARY KEY,
		thread_key   INTEGER NOT NULL,
		sender_fbid  INTEGER NOT NULL,
		ts_ms        INTEGER NOT NULL,
		content_type TEXT NOT NULL,
		text         TEXT NOT NULL,
		otid         TEXT NOT NULL,
		send_state   INTEGER NOT NULL
	);
	CREATE INDEX messages_by_thread ON messages (thread_key, ts_ms, id);
	CREATE INDEX messages_by_otid ON messages (otid);`,
}

// tables lists every table, for emptying the cache when the account changes.
var tables = []string{"account", "users", "threads", "thread_members", "messages"}

// Store is the cache. Its methods are safe for concurrent use; they run one
// at a time.
type Store struct{ db *sql.DB }

// Open opens the cache at path, creating it and its directory if needed, and
// brings its schema up to date.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("open cache %s: path contains '?'", path)
	}
	if path != Memory {
		// SQLite gives its -wal and -shm files the database file's mode.
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("open cache: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open cache: %w", err)
		}
		err = errors.Join(f.Chmod(0o600), f.Close())
		if err != nil {
			return nil, fmt.Errorf("open cache: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		return nil, fmt.Errorf("open cache %s: %w", path, err)
	}
	// One connection: writes never contend, and Memory stays one database.
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db, len(migrations)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open cache %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the cache.
func (s *Store) Close() error { return s.db.Close() }

// migrate applies migrations until the schema is at version to.
func migrate(ctx context.Context, db *sql.DB, to int) error {
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this meow knows (%d); delete the cache to start over", v, len(migrations))
	}
	for ; v < to; v++ {
		err := inTx(ctx, db, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
				return err
			}
			// PRAGMA takes no parameters; v is an int.
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1))
			return err
		})
		if err != nil {
			return fmt.Errorf("migrate to version %d: %w", v+1, err)
		}
	}
	return nil
}

// inTx runs f in a transaction and commits if it returns nil.
func inTx(ctx context.Context, db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetMe records the logged-in user. If another user's data is cached, it is
// deleted first, so one account never sees another's messages.
func (s *Store) SetMe(ctx context.Context, me core.User) error {
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		var old core.UserID
		err := tx.QueryRowContext(ctx, "SELECT fbid FROM account").Scan(&old)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case old != me.ID:
			for _, t := range tables {
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO account (fbid, username, display_name) VALUES (?, ?, ?)
			ON CONFLICT (fbid) DO UPDATE SET username = excluded.username, display_name = excluded.display_name`,
			me.ID, me.Username, me.Name)
		return err
	})
	if err != nil {
		return fmt.Errorf("cache account: %w", err)
	}
	return nil
}
