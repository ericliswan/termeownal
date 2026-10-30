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

package core

import "context"

// Backend is a logged-in chat service. Call Restore first; after that the
// methods are safe for concurrent use.
type Backend interface {
	// Restore builds the client from a saved session.
	Restore(ctx context.Context, s Session) error

	// Snapshot fetches the logged-in user and the most recent threads. The
	// app calls it from the goroutine that reads Events, so it must not wait
	// for Events to be read.
	Snapshot(ctx context.Context) (me User, threads []Thread, err error)

	// History returns up to n messages older than before, oldest first, and
	// whether older ones remain. An empty before means the newest messages.
	History(ctx context.Context, thread ThreadID, before MessageID, n int) (msgs []Message, more bool, err error)

	// NewOTID returns a fresh offline threading ID for Send.
	NewOTID() string

	// Send sends text. Success means the service accepted it; the message
	// counts as sent when its echo arrives as a MessageAdded with the same
	// OTID. Retrying with the same OTID can't create a duplicate.
	Send(ctx context.Context, thread ThreadID, text, otid string) error

	// MarkRead marks upTo's thread read up to and including upTo.
	MarkRead(ctx context.Context, upTo Message) error

	// Connect runs the live connection, reconnecting on its own, and blocks
	// until ctx is cancelled, Close is called, or the backend sends
	// ResyncNeeded.
	Connect(ctx context.Context) error

	// Events delivers live events in order. It is closed by Close.
	Events() <-chan Event

	// ExportAuth returns the current login data to save. It changes while
	// the backend runs; SessionChanged says when to export again. It works
	// after Close, for a final save.
	ExportAuth() (AuthData, error)

	// Close disconnects and closes the Events channel.
	Close()
}
