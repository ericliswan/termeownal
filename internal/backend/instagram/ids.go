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

package instagram

import (
	"sync"

	"termeownal/internal/core"
)

// threadIDs are the two IDs Instagram uses for a thread besides its key
// (docs/ids.md §1).
type threadIDs struct {
	short string // ThreadInfo.ID: history, sending and live deltas
	long  string // ThreadInfo.ThreadID: marking read
}

// idMap maps thread keys to their other IDs and back, and users' IGIDs to
// their FBIDs, for typing notifications. It learns them from every thread
// and ID lookup; it isn't saved.
type idMap struct {
	mu      sync.Mutex
	byKey   map[core.ThreadID]threadIDs
	byShort map[string]core.ThreadID
	byLong  map[string]core.ThreadID
	users   map[string]core.UserID
}

// add records a thread's IDs. A thread's short ID can change (a new DM gets
// one with its first message), so the old one is forgotten.
func (m *idMap) add(key core.ThreadID, ids threadIDs) {
	if key == 0 || ids.short == "" || ids.long == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byKey == nil {
		m.byKey = map[core.ThreadID]threadIDs{}
		m.byShort = map[string]core.ThreadID{}
		m.byLong = map[string]core.ThreadID{}
	}
	if old, ok := m.byKey[key]; ok {
		delete(m.byShort, old.short)
		delete(m.byLong, old.long)
	}
	m.byKey[key] = ids
	m.byShort[ids.short] = key
	m.byLong[ids.long] = key
}

// addUser records a user's FBID by IGID.
func (m *idMap) addUser(igid string, fbid core.UserID) {
	if igid == "" || fbid == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users == nil {
		m.users = map[string]core.UserID{}
	}
	m.users[igid] = fbid
}

// get returns a thread's IDs.
func (m *idMap) get(key core.ThreadID) (threadIDs, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids, ok := m.byKey[key]
	return ids, ok
}

// key returns the key of the thread with a short ID.
func (m *idMap) key(short string) (core.ThreadID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byShort[short]
	return key, ok
}

// keyOfLong returns the key of the thread with a long ID.
func (m *idMap) keyOfLong(long string) (core.ThreadID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byLong[long]
	return key, ok
}

// user returns the FBID of the user with an IGID.
func (m *idMap) user(igid string) (core.UserID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fbid, ok := m.users[igid]
	return fbid, ok
}
