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
	{ID: 101, Username: "mittens.k", Name: "Mittens Kowalski"},
	{ID: 102, Username: "biscuit", Name: "Biscuit"},
	{ID: 103, Username: "oolong_tea", Name: "Oolong"},
	{ID: 104, Username: "pixel.paws", Name: "Pixel Paws"},
	{ID: 105, Username: "sir.whiskers", Name: "Sir Whiskers"},
	{ID: 106, Username: "tofu_the_cat", Name: "Tofu"},
	{ID: 107, Username: "maru.box", Name: "Maru"},
	{ID: 108, Username: "nyan", Name: "Nyan 🌈"},
	{ID: 109, Username: "koshka", Name: "Кошка"},
	{ID: 110, Username: "neko_chan", Name: "ねこ"},
	{ID: 111, Username: "luna.moon", Name: "Luna"},
	{ID: 112, Username: "pumpkin", Name: "Pumpkin Spice"},
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
	{id: 9001, title: "Cat Café Crew", members: []int{1, 2, 5, 6}, messages: 1500, unread: true},
	{members: []int{3}, messages: 64},
	{id: 9002, members: []int{1, 5, 6}, messages: 25},
	{members: []int{4}, messages: 400, unread: true},
	{members: []int{7}, messages: 9},
	{id: 9003, title: "Night Zoomies 🌙", members: []int{8, 9, 10, 11, 0}, messages: 120},
	{members: []int{9}, messages: 1},
	{members: []int{10}, messages: 75},
	{members: []int{11}, messages: 260},
}

var lines = []string{
	"hey!",
	"did you feed the cat",
	"she knocked the plant over again 🌱",
	"lol",
	"omw",
	"can't, there's a cat on my keyboard",
	"ok",
	"🐈🐈🐈",
	"what time tomorrow?",
	"around 7 works for me",
	"have you seen this https://example.com/cats/loaf",
	"no way",
	"he's been sitting in that box for three hours and refuses to come out, I think he lives there now",
	"ねこはかわいいです",
	"Кот спит весь день",
	"thank you!!",
	"👀",
	"brb",
	"Okay so long story: I went to the shop for cat food, they were out of the usual kind, so I bought the fancy one, and now she won't eat anything else. I have created a monster. Send help (and tuna).",
	"sounds good",
	"haha yes",
	"wait what",
	"good night 🌙",
	"morning ☕",
	"zoomies at 3am again",
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
