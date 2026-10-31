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

package fake

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"termeownal/internal/core"
)

// Content types as Instagram reports them (docs/ids.md §6).
const (
	contentText        = "TEXT"
	contentReactionLog = "REACTION_LOG_XMAT"
	contentSticker     = "CUTOUT_STICKER_XMA"
)

var me = core.User{ID: 100, Username: "meow.tester", Name: "Meow Tester"}

var people = []core.User{
	{ID: 101, Username: "priya.sharma", Name: "Priya Sharma"},
	{ID: 102, Username: "james.oc", Name: "James O'Connor"},
	{ID: 103, Username: "sofia.rossi", Name: "Sofia Rossi"},
	{ID: 104, Username: "tom_hughes", Name: "Tom Hughes"},
	{ID: 105, Username: "aisha.b", Name: "Aisha Bello"},
	{ID: 106, Username: "ben.carter", Name: "Ben Carter"},
	{ID: 107, Username: "chloe.w", Name: "Chloe Wong"},
	{ID: 108, Username: "dan.plays", Name: "Dan 🎸"},
	{ID: 109, Username: "katya.v", Name: "Катя"},
	{ID: 110, Username: "yuki.t", Name: "ゆき"},
	{ID: 111, Username: "lucas.m", Name: "Lucas Martin"},
	{ID: 112, Username: "hannah.k", Name: "Hannah Kim"},
}

// threadSpecs lists the canned threads, most recent first. A 1:1 thread's ID
// is the other person's user ID, as on Instagram; a group gets its own.
var threadSpecs = []struct {
	id       core.ThreadID // 0 for a 1:1
	title    string        // empty: the backend fills it in
	members  []int         // indexes into people
	messages int
	unread   bool
}{
	{members: []int{0}, messages: 180, unread: true},
	{id: 9001, title: "COMP2003 project", members: []int{1, 2, 5, 6}, messages: 1500, unread: true},
	{members: []int{3}, messages: 64},
	{id: 9002, members: []int{1, 5, 6}, messages: 25},
	{members: []int{4}, messages: 400, unread: true},
	{members: []int{7}, messages: 9},
	{id: 9003, title: "Film Society 🎬", members: []int{8, 9, 10, 11, 0}, messages: 120},
	{members: []int{9}, messages: 1},
	{members: []int{10}, messages: 75},
	{members: []int{11}, messages: 260},
}

var lines = []string{
	"hey!",
	"has anyone started the report yet",
	"I can do the intro and the lit review",
	"lol",
	"omw",
	"in the library, 3rd floor, come find me",
	"ok",
	"😭😭😭",
	"what time tomorrow?",
	"around 7 works for me",
	"slides are here https://example.com/slides/week-9",
	"no way",
	"the deadline got moved to Friday, which is great, except now it clashes with the stats exam, so not that great",
	"明日の試験がんばってね",
	"Встретимся в библиотеке?",
	"thank you!!",
	"👀",
	"brb",
	"Okay so long story: I went to the lecture, it was cancelled and nobody told us, so I went to the café, ran into half the society, and somehow I'm now running the bake sale. I don't even bake.",
	"sounds good",
	"haha yes",
	"wait what",
	"good luck tomorrow 🍀",
	"coffee before the lecture? ☕",
	"are you coming to the social on Thursday",
	"just submitted it 🎉",
}

// seedThread is a thread with its full history, oldest first.
type seedThread struct {
	core.Thread
	history []core.Message
}

// seedData builds the canned threads. The same now gives the same data.
func seedData(now time.Time) []*seedThread {
	rng := rand.New(rand.NewPCG(1, 2))
	threads := make([]*seedThread, 0, len(threadSpecs))
	last := now
	for i, spec := range threadSpecs {
		t := &seedThread{Thread: core.Thread{ID: spec.id, Title: spec.title, IsGroup: spec.id != 0}}
		for _, p := range spec.members {
			t.Members = append(t.Members, people[p])
		}
		if !t.IsGroup {
			t.ID = core.ThreadID(t.Members[0].ID)
		}
		if t.Title == "" {
			t.Title = defaultTitle(t.Members)
		}

		last = last.Add(-time.Duration(rng.IntN(90)+i*i*20) * time.Minute)
		t.LastActivity = last
		t.Unread = spec.unread
		t.history = make([]core.Message, spec.messages)
		at := last
		for k := spec.messages - 1; k >= 0; k-- {
			m := core.Message{
				ID:          core.MessageID(fmt.Sprintf("mid.$fake%d.%d", i, k)),
				Thread:      t.ID,
				Sender:      me.ID,
				At:          at,
				ContentType: contentText,
			}
			// Unread threads end with a message from someone else.
			if rng.IntN(10) < 6 || (spec.unread && k == spec.messages-1) {
				m.Sender = t.Members[rng.IntN(len(t.Members))].ID
			}
			switch {
			case k%41 == 40:
				m.ContentType = contentReactionLog
			case k%67 == 66:
				m.ContentType = contentSticker
			default:
				m.Text = lines[rng.IntN(len(lines))]
			}
			t.history[k] = m

			at = at.Add(-time.Duration(rng.IntN(600)+20) * time.Second)
			if rng.IntN(12) == 0 {
				at = at.Add(-time.Duration(rng.IntN(36)) * time.Hour)
			}
		}
		threads = append(threads, t)
	}
	return threads
}

// defaultTitle names an untitled thread after its members.
func defaultTitle(members []core.User) string {
	names := make([]string, len(members))
	for i, u := range members {
		names[i] = strings.Fields(u.Name)[0]
	}
	return strings.Join(names, ", ")
}
