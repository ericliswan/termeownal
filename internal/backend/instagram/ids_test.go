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
	"testing"

	"termeownal/internal/core"
)

func TestIDMap(t *testing.T) {
	type add struct {
		key core.ThreadID
		ids threadIDs
	}
	oneToOne := threadIDs{short: "3400000000000001", long: "340282366841710300949128000000000000001"}
	newShort := threadIDs{short: "3400000000000009", long: oneToOne.long}
	newLong := threadIDs{short: oneToOne.short, long: "340282366841710300949128000000000000009"}
	group := threadIDs{short: "3400000000000002", long: "340282366841710300949128000000000000002"}
	for _, tc := range []struct {
		name      string
		adds      []add
		key       core.ThreadID
		want      threadIDs
		wantFound bool
		short     string // looked up with key()
		wantKey   core.ThreadID
		long      string // looked up with keyOfLong()
		wantLong  core.ThreadID
	}{
		{name: "1:1, key is the other user's FBID", adds: []add{{10000000000000001, oneToOne}},
			key: 10000000000000001, want: oneToOne, wantFound: true, short: oneToOne.short, wantKey: 10000000000000001,
			long: oneToOne.long, wantLong: 10000000000000001},
		{name: "group, key is the short ID", adds: []add{{3400000000000002, group}},
			key: 3400000000000002, want: group, wantFound: true, short: group.short, wantKey: 3400000000000002,
			long: group.long, wantLong: 3400000000000002},
		{name: "unknown key", key: 10000000000000001, short: oneToOne.short, long: oneToOne.long},
		{name: "new short ID forgets the old one", adds: []add{{10000000000000001, oneToOne}, {10000000000000001, newShort}},
			key: 10000000000000001, want: newShort, wantFound: true, short: oneToOne.short},
		{name: "new long ID forgets the old one", adds: []add{{10000000000000001, oneToOne}, {10000000000000001, newLong}},
			key: 10000000000000001, want: newLong, wantFound: true, short: oneToOne.short, wantKey: 10000000000000001,
			long: oneToOne.long},
		{name: "zero key ignored", adds: []add{{0, oneToOne}}, short: oneToOne.short, long: oneToOne.long},
		{name: "missing long ID ignored", adds: []add{{10000000000000001, threadIDs{short: oneToOne.short}}},
			key: 10000000000000001, short: oneToOne.short, long: oneToOne.long},
		{name: "missing short ID ignored", adds: []add{{10000000000000001, threadIDs{long: oneToOne.long}}},
			key: 10000000000000001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m idMap
			for _, a := range tc.adds {
				m.add(a.key, a.ids)
			}
			got, found := m.get(tc.key)
			if got != tc.want || found != tc.wantFound {
				t.Errorf("get(%d) = %+v, %v", tc.key, got, found)
			}
			key, found := m.key(tc.short)
			if key != tc.wantKey || found != (tc.wantKey != 0) {
				t.Errorf("key(%q) = %d, %v", tc.short, key, found)
			}
			key, found = m.keyOfLong(tc.long)
			if key != tc.wantLong || found != (tc.wantLong != 0) {
				t.Errorf("keyOfLong(%q) = %d, %v", tc.long, key, found)
			}
		})
	}
}

func TestIDMapUsers(t *testing.T) {
	var m idMap
	m.addUser("9001", 10000000000000001)
	m.addUser("", 10000000000000002)
	m.addUser("9003", 0)
	if fbid, ok := m.user("9001"); fbid != 10000000000000001 || !ok {
		t.Errorf("user(9001) = %d, %v", fbid, ok)
	}
	for _, igid := range []string{"", "9003", "9000"} {
		if fbid, ok := m.user(igid); ok {
			t.Errorf("user(%q) = %d, want none", igid, fbid)
		}
	}
}
