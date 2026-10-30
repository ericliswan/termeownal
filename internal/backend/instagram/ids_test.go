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
	group := threadIDs{short: "3400000000000002", long: "340282366841710300949128000000000000002"}
	for _, tc := range []struct {
		name      string
		adds      []add
		key       core.ThreadID
		want      threadIDs
		wantFound bool
		short     string // looked up with key()
		wantKey   core.ThreadID
	}{
		{name: "1:1, key is the other user's FBID", adds: []add{{10000000000000001, oneToOne}},
			key: 10000000000000001, want: oneToOne, wantFound: true, short: oneToOne.short, wantKey: 10000000000000001},
		{name: "group, key is the short ID", adds: []add{{3400000000000002, group}},
			key: 3400000000000002, want: group, wantFound: true, short: group.short, wantKey: 3400000000000002},
		{name: "unknown key", key: 10000000000000001, short: oneToOne.short},
		{name: "new short ID forgets the old one", adds: []add{{10000000000000001, oneToOne}, {10000000000000001, newShort}},
			key: 10000000000000001, want: newShort, wantFound: true, short: oneToOne.short},
		{name: "zero key ignored", adds: []add{{0, oneToOne}}, short: oneToOne.short},
		{name: "missing long ID ignored", adds: []add{{10000000000000001, threadIDs{short: oneToOne.short}}},
			key: 10000000000000001, short: oneToOne.short},
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
		})
	}
}
