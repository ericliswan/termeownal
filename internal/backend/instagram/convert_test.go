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
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"

	"termeownal/internal/core"
)

var (
	alice = core.User{ID: 10000000000000001, Username: "alice.example", Name: "Alice Example"}
	bob   = core.User{ID: 10000000000000002, Username: "bob.example"}
	carol = core.User{ID: 10000000000000003, Username: "carol.example"}
)

// fixtureThreads are the threads in testdata/mailbox.json, as Snapshot
// returns them.
var fixtureThreads = []core.Thread{
	{
		ID:           core.ThreadID(alice.ID),
		Title:        "Alice Example",
		Members:      []core.User{alice},
		LastActivity: time.UnixMilli(1767225600000),
		Unread:       true, // my read receipt is behind the last message
		Messages: []core.Message{
			{ID: "mid.$fake1a", Thread: core.ThreadID(alice.ID), Sender: testMe.ID, At: time.UnixMilli(1767225400000), Text: "placeholder one", ContentType: "TEXT", OTID: "7000000000000000001"},
			{
				ID: "mid.$fake1c", Thread: core.ThreadID(alice.ID), Sender: alice.ID, At: time.UnixMilli(1767225600000), Text: "placeholder three", ContentType: "TEXT",
				ReplyTo: core.Quote{ID: "mid.$fake1a", Sender: testMe.ID, Text: "placeholder one", ContentType: "TEXT"},
			},
		},
		ReadUpTo: map[core.UserID]time.Time{alice.ID: time.UnixMilli(1767225600000)},
	},
	{
		ID:           3400000000000002,
		Title:        "Alice Example, bob.example",
		IsGroup:      true,
		Members:      []core.User{alice, bob},
		LastActivity: time.UnixMilli(1767225300000),
		Messages: []core.Message{
			{ID: "mid.$fake2a", Thread: 3400000000000002, Sender: bob.ID, At: time.UnixMilli(1767225300000), ContentType: "MEDIA_SHARE"},
		},
		ReadUpTo: map[core.UserID]time.Time{bob.ID: time.UnixMilli(1767225300000)},
	},
	{
		ID:           core.ThreadID(carol.ID),
		Title:        "carol.example [IG_SOMETHING_NEW]",
		Members:      []core.User{carol},
		LastActivity: time.UnixMilli(1767225200000),
		Unread:       true, // marked unread, though read
	},
}

// loadMailbox reads testdata/mailbox.json.
func loadMailbox(t *testing.T) *slidetypes.MailboxResponse {
	t.Helper()
	data, err := os.ReadFile("testdata/mailbox.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp slidetypes.MailboxResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	return &resp
}

func TestConvertThread(t *testing.T) {
	edges := loadMailbox(t).Mailbox.ThreadsByFolder.Edges
	if len(edges) != len(fixtureThreads) {
		t.Fatalf("%d threads in the fixture", len(edges))
	}
	for i, want := range fixtureThreads {
		got := convertThread(edges[i].Node.AsIGDirectThread, testMe.ID, zerolog.Nop())
		if !reflect.DeepEqual(got, want) {
			t.Errorf("thread %d:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func TestConvertThreadNamedGroup(t *testing.T) {
	th := loadMailbox(t).Mailbox.ThreadsByFolder.Edges[1].Node.AsIGDirectThread
	th.ThreadTitle = "Meow Club"
	if got := convertThread(th, testMe.ID, zerolog.Nop()).Title; got != "Meow Club" {
		t.Fatalf("title = %q", got)
	}
}

// Upstream keys messages by id; message_id is sometimes empty.
func TestConvertMessageID(t *testing.T) {
	for _, m := range []*slidetypes.Message{{ID: "mid.$x"}, {ID: "mid.$x", MessageID: "mid.$x"}, {MessageID: "mid.$x"}} {
		if got, ok := convertMessage(m, 1, zerolog.Nop()); !ok || got.ID != "mid.$x" {
			t.Errorf("%+v: ID = %q, ok = %v", m, got.ID, ok)
		}
	}
}

func TestConvertMessageSkips(t *testing.T) {
	for _, m := range []*slidetypes.Message{nil, {ContentType: "TEXT"}, {ID: "mid.$x", ContentType: contentReactionLog}} {
		if _, ok := convertMessage(m, 1, zerolog.Nop()); ok {
			t.Errorf("%+v converted", m)
		}
	}
}

// A reply to an unsent message carries only the original's ID.
func TestConvertReplyWithoutCopy(t *testing.T) {
	got, _ := convertMessage(&slidetypes.Message{ID: "mid.$x", RepliedToMessageID: "mid.$gone"}, 1, zerolog.Nop())
	if got.ReplyTo != (core.Quote{ID: "mid.$gone"}) {
		t.Errorf("ReplyTo = %+v", got.ReplyTo)
	}
}

func TestConvertReactions(t *testing.T) {
	got, _ := convertMessage(&slidetypes.Message{ID: "mid.$x", Reactions: []*slidetypes.Reaction{
		{LogMessageID: "mid.$r1", Reaction: "😂", SenderFBID: 7},
		nil,
		{LogMessageID: "mid.$r2", Reaction: "😂"},
		{LogMessageID: "mid.$r3", SenderFBID: 8},
	}}, 1, zerolog.Nop())
	if want := []core.Reaction{{User: 7, Emoji: "😂", ID: "mid.$r1"}}; !slices.Equal(got.Reactions, want) {
		t.Errorf("Reactions = %+v, want %+v", got.Reactions, want)
	}
}
