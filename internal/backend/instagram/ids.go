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

// idMap maps thread keys to their other IDs, and short IDs back to keys. It
// learns them from every thread and ID lookup; it isn't saved.
type idMap struct {
	mu      sync.Mutex
	byKey   map[core.ThreadID]threadIDs
	byShort map[string]core.ThreadID
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
	}
	if old, ok := m.byKey[key]; ok && old.short != ids.short {
		delete(m.byShort, old.short)
	}
	m.byKey[key] = ids
	m.byShort[ids.short] = key
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
