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

import "time"

// Event is something a Backend reports on its Events channel. Delivery is
// at least once, so applying an event twice must change nothing.
type Event interface{ event() }

// Status is the state of the live connection.
type Status int

// Connection states.
const (
	Connecting Status = iota
	Connected
	Reconnecting
)

// StatusChanged reports a change in the live connection.
type StatusChanged struct{ Status Status }

// AuthFailed means the login no longer works. Stop retrying and ask the user
// to log in again. Err wraps one of the sentinel errors where one fits.
type AuthFailed struct{ Err error }

// ResyncNeeded means local state can't be caught up from events: take a new
// Snapshot, then Connect again.
type ResyncNeeded struct{}

// MessageAdded is a new message, or the echo of one sent from here.
type MessageAdded struct{ Message Message }

// ThreadRead means the user read the thread up to Until on another device.
type ThreadRead struct {
	Thread ThreadID
	Until  time.Time
}

// ThreadUpdated carries a thread's current details. It comes before the
// first event for a thread the backend hadn't reported yet.
type ThreadUpdated struct{ Thread Thread }

// SeqAdvanced means every event sent before it belongs to sequence ID SeqID.
// Save it only after those events are stored.
type SeqAdvanced struct {
	SeqID int64
	At    time.Time
}

// SessionChanged means the login data changed: call ExportAuth and save it.
type SessionChanged struct{}

func (StatusChanged) event()  {}
func (AuthFailed) event()     {}
func (ResyncNeeded) event()   {}
func (MessageAdded) event()   {}
func (ThreadRead) event()     {}
func (ThreadUpdated) event()  {}
func (SeqAdvanced) event()    {}
func (SessionChanged) event() {}
