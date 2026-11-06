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

package chat

import (
	"testing"
	"time"

	"termeownal/internal/core"
)

func TestSeenLine(t *testing.T) {
	at := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	me := core.User{ID: 1, Name: "Me"}
	ann := core.User{ID: 2, Name: "Ann Lee"}
	bob := core.User{ID: 3, Name: "Bob"}
	mine := core.Message{ID: "m1", Sender: me.ID, At: at}
	theirs := core.Message{ID: "m2", Sender: ann.ID, At: at.Add(time.Minute)}
	reaction := core.Message{ID: "m3", Sender: ann.ID, At: at.Add(time.Minute), ContentType: reactionLog}
	sending := core.Message{ID: "local:1", Sender: me.ID, At: at.Add(time.Minute), State: core.Sending}
	read := func(users ...core.User) map[core.UserID]time.Time {
		m := map[core.UserID]time.Time{}
		for _, u := range users {
			m[u.ID] = at
		}
		return m
	}
	dm := core.Thread{Members: []core.User{ann}}
	group := core.Thread{IsGroup: true, Members: []core.User{ann, bob}}

	tests := []struct {
		name   string
		thread core.Thread
		reads  map[core.UserID]time.Time
		msgs   []core.Message
		want   string
	}{
		{"no messages", dm, read(ann), nil, ""},
		{"unread", dm, nil, []core.Message{mine}, ""},
		{"read too early", dm, map[core.UserID]time.Time{ann.ID: at.Add(-time.Millisecond)}, []core.Message{mine}, ""},
		{"read within the millisecond", dm, map[core.UserID]time.Time{ann.ID: at}, []core.Message{{ID: "m1", Sender: me.ID, At: at.Add(500 * time.Microsecond)}}, "Seen"},
		{"1:1", dm, read(ann), []core.Message{mine}, "Seen"},
		{"after a reaction log", dm, read(ann), []core.Message{mine, reaction}, "Seen"},
		{"their reply is newest", dm, read(ann), []core.Message{mine, theirs}, ""},
		{"still sending", dm, read(ann), []core.Message{mine, sending}, ""},
		{"group, some", group, read(ann), []core.Message{mine}, "Seen by Ann"},
		{"group, all", group, read(ann, bob), []core.Message{mine}, "Seen by everyone"},
		{"non-member watermark", dm, read(bob), []core.Message{mine}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := tt.thread
			th.ReadUpTo = tt.reads
			if got := seenLine(th, me, tt.msgs); got != tt.want {
				t.Errorf("seenLine = %q, want %q", got, tt.want)
			}
		})
	}
}
