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
	"cmp"
	"slices"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"

	"termeownal/internal/core"
)

// contentReactionLog is the content type of a history entry that records a
// reaction. It isn't a chat line (docs/ids.md §7).
const contentReactionLog = "REACTION_LOG_XMAT"

// knownSubtypes are the thread subtypes upstream knows. Others get a label
// in the title: they might be chats termeownal can't show, such as
// end-to-end encrypted ones (AGENTS.md §7.4).
var knownSubtypes = []slidetypes.ThreadSubtype{
	slidetypes.ThreadSubtypeDirect,
	slidetypes.ThreadSubtypeDirectBusiness,
	slidetypes.ThreadSubtypeAIBot,
	slidetypes.ThreadSubtypeGroup,
}

// idsOf returns a thread's short and long IDs.
func idsOf(t *slidetypes.ThreadInfo) threadIDs {
	return threadIDs{short: t.ID, long: t.ThreadID}
}

// convertThread converts a thread for the logged-in user me.
func convertThread(t *slidetypes.ThreadInfo, me core.UserID, log zerolog.Logger) core.Thread {
	key := core.ThreadID(t.ThreadKey)
	out := core.Thread{
		ID:           key,
		IsGroup:      t.IsGroup || len(t.Users) > 1,
		LastActivity: t.LastActivityTimestampMS.Time,
		Unread:       true,
	}
	var names []string
	for _, u := range t.Users {
		if u == nil {
			continue
		}
		out.Members = append(out.Members, core.User{ID: core.UserID(u.InteropMessagingUserFBID), Username: u.Username, Name: u.FullName})
		names = append(names, cmp.Or(u.FullName, u.Username))
	}
	if out.IsGroup {
		out.Title = cmp.Or(t.ThreadTitle, strings.Join(names, ", "))
	} else {
		out.Title = cmp.Or(strings.Join(names, ", "), t.ThreadTitle)
	}
	// shortcut: input_mode isn't checked as its usual values aren't known;
	// label on it too once the debug log shows them.
	log.Debug().Str("subtype", string(t.ThreadSubtype)).Int("input_mode", t.InputMode).Msg("thread")
	if !slices.Contains(knownSubtypes, t.ThreadSubtype) {
		out.Title += " [" + string(t.ThreadSubtype) + "]"
	}
	// Read if my read receipt reaches the last activity, as upstream decides.
	for _, rr := range t.SlideReadReceipts {
		if core.UserID(rr.ParticipantFBID) == me {
			out.Unread = rr.WatermarkTimestampMS.Before(out.LastActivity)
			break
		}
	}
	if t.MarkedAsUnread {
		out.Unread = true
	}
	if t.SlideMessages != nil {
		out.Messages = convertMessages(t.SlideMessages, key, log)
	}
	return out
}

// convertMessages converts a page of messages, newest first as Instagram
// sends them, to chat lines oldest first.
func convertMessages(page *slidetypes.SlideMessages, key core.ThreadID, log zerolog.Logger) []core.Message {
	var out []core.Message
	for _, e := range slices.Backward(page.Edges) {
		if m, ok := convertMessage(e.Node, key, log); ok {
			out = append(out, m)
		}
	}
	return out
}

// convertMessage converts a message in thread key. It reports false for
// entries that aren't chat lines.
func convertMessage(m *slidetypes.Message, key core.ThreadID, log zerolog.Logger) (core.Message, bool) {
	if m == nil || m.ContentType == contentReactionLog {
		return core.Message{}, false
	}
	log.Debug().Str("content_type", m.ContentType).Msg("message")
	return core.Message{
		ID:          core.MessageID(m.MessageID),
		Thread:      key,
		Sender:      core.UserID(m.SenderFBID),
		At:          m.TimestampMS.Time,
		Text:        m.TextBody,
		ContentType: m.ContentType,
		OTID:        m.OfflineThreadingID,
	}, true
}
