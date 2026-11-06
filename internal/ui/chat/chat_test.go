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

	tea "charm.land/bubbletea/v2"

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

func TestQuoteText(t *testing.T) {
	me := core.User{ID: 1}
	ann := core.User{ID: 2, Name: "Ann Lee"}
	thread := core.Thread{Members: []core.User{ann}}
	loaded := []core.Message{{ID: "m1", Sender: ann.ID, Text: "from\nthe cache"}}

	tests := []struct {
		name string
		q    core.Quote
		want string
	}{
		{"text", core.Quote{ID: "x", Sender: ann.ID, Text: "hi  there\nyou"}, "Ann: hi there you"},
		{"own", core.Quote{ID: "x", Sender: me.ID, Text: "hi"}, "You: hi"},
		{"content type", core.Quote{ID: "x", Sender: ann.ID, ContentType: "IMAGE"}, "Ann: [IMAGE]"},
		{"empty, loaded", core.Quote{ID: "m1"}, "Ann: from the cache"},
		{"empty, not loaded", core.Quote{ID: "gone"}, "a message"},
		{"unknown sender", core.Quote{ID: "x", Sender: 9, Text: "hi"}, "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuoteText(thread, me, loaded, tt.q); got != tt.want {
				t.Errorf("QuoteText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectionMovesAndScrolls(t *testing.T) {
	at := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	ann := core.User{ID: 2, Name: "Ann"}
	var msgs []core.Message
	for i := range 30 {
		msgs = append(msgs, core.Message{ID: core.MessageID(rune('a' + i)), Sender: ann.ID, At: at.Add(time.Duration(i) * time.Hour), Text: "x"})
	}
	msgs[28].ContentType = reactionLog // not selectable

	m := New(func() time.Time { return at })
	m.SetSize(40, 10)
	m.Show(core.Thread{ID: 1, Members: []core.User{ann}}, core.User{ID: 1}, msgs, false, false)
	if _, ok := m.Selected(); ok {
		t.Fatal("selection while unfocused")
	}
	m.SetFocused(true)
	key := func(r rune) { m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)}) }
	want := func(id core.MessageID) {
		t.Helper()
		sel, ok := m.Selected()
		if !ok || sel.ID != id {
			t.Fatalf("selected %q, want %q", sel.ID, id)
		}
		span := m.spans[id]
		if span[0] < m.vp.YOffset() || span[1] > m.vp.YOffset()+m.vp.Height() {
			t.Fatalf("selected lines %v outside the view at %d+%d", span, m.vp.YOffset(), m.vp.Height())
		}
	}
	want(msgs[29].ID)
	key('k')
	want(msgs[27].ID) // skips the reaction log
	key('g')
	want(msgs[0].ID)
	key('j')
	want(msgs[1].ID)
	key('G')
	want(msgs[29].ID)
	m.SetFocused(false)
	if _, ok := m.Selected(); ok {
		t.Fatal("selection kept after blur")
	}
}
