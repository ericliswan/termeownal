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

// Package core holds termeownal's domain types and the interfaces between
// the app and a chat backend. It imports no other termeownal package.
package core

import "time"

// ThreadID identifies a thread. For Instagram it is the thread key: the other
// user's FBID in a 1:1, or the thread's short ID in a group. The other thread
// IDs Instagram uses stay inside the backend (docs/ids.md).
type ThreadID int64

// UserID identifies a user. For Instagram it is the FBID.
type UserID int64

// MessageID identifies a message.
type MessageID string

// User is a person, including the logged-in user.
type User struct {
	ID       UserID
	Username string
	Name     string
}

// Thread is a conversation.
type Thread struct {
	ID ThreadID
	// Title is the name to display. The backend fills it in when the
	// service leaves it empty, e.g. with the other person's name in a 1:1.
	Title   string
	IsGroup bool
	// Members excludes the logged-in user.
	Members      []User
	LastActivity time.Time
	Unread       bool
	// Messages holds the newest messages, oldest first, when the backend
	// returns them with the thread. It may be empty.
	Messages []Message
	// ReadUpTo is how far each member has read, where the backend knows.
	ReadUpTo map[UserID]time.Time
}

// SendState tracks a message the user sent from this client.
type SendState int

// Send states. The zero value is Sent, so messages from a backend need no
// state set.
const (
	Sent SendState = iota
	Sending
	SendFailed
)

// Message is one chat line.
type Message struct {
	ID     MessageID
	Thread ThreadID
	Sender UserID
	At     time.Time
	// Text is empty for non-text messages; show ContentType instead.
	Text        string
	ContentType string
	// OTID is the offline threading ID chosen when sending. The echo of a
	// sent message carries the same OTID, which is how it is matched.
	OTID  string
	State SendState
	// ReplyTo is the message this one replies to; its ID is "" if none.
	ReplyTo Quote
}

// Quote is the message a reply quotes, as it was when the reply was sent.
// Only ID is set if the service didn't include a copy, e.g. after an unsend.
type Quote struct {
	ID     MessageID
	Sender UserID
	// Text is empty for non-text messages; show ContentType instead.
	Text        string
	ContentType string
}

// AuthData is a backend's saved login: cookies and cached client state. Only
// the backend that produced it reads it. It is secret, so String and GoString
// hide it from fmt and loggers that use them.
type AuthData []byte

// String implements fmt.Stringer without revealing the data.
func (AuthData) String() string { return "[redacted]" }

// GoString implements fmt.GoStringer without revealing the data.
func (AuthData) GoString() string { return "[redacted]" }

// Session is what termeownal saves to resume without logging in again.
type Session struct {
	Auth AuthData
	// SeqID is the last event sequence ID whose events are all in the store,
	// and SeqAt is when it was reached. Zero means none: the backend starts
	// cold.
	SeqID int64
	SeqAt time.Time
}
