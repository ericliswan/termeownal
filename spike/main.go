// termeownal — terminal Instagram DMs
// Copyright (C) 2026 termeownal contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Command spike is Phase 0 throwaway code: it exercises every instameow call
// termeownal needs and prints the IDs involved, so we can write docs/ids.md.
// It never prints message text or cookie values. Delete after Phase 0.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/util/jsontime"
)

const usage = `usage: spike -cookies PATH [-state DIR] COMMAND [ARGS]

commands:
  index                      LoadIndex (always cold); print viewer and every thread's IDs
  history SHORT_ID [N]       PaginateMessages; print last N (default 20) messages' IDs and content types
  send THREAD_ID TEXT        connect, SendMessage, wait for the echo and compare OTIDs
                             (upstream passes the SHORT id; put -long before "send" if you passed the long one)
  send-new USER_FBID TEXT    connect, send to a user with no thread via RecipientIGIDs
  listen [DURATION]          connect and print every event (default 5m; Ctrl+C to stop)
  markread SHORT_ID          GetThread, then MarkRead + MarkReadValidation on the newest message
  newdm USER_FBID            FetchThreadID + GetUserForNewDM

Every command except index resumes like the real app and prints the path taken:
  FAST = saved state + seq ID; delete state.json for WARM; delete state.json and seq.json for COLD.
`

const (
	stateFile = "state.json"
	seqFile   = "seq.json"
	logFile   = "spike.log"
)

type spike struct {
	dir    string
	cli    *instameow.Client
	events chan slidetypes.ClientEvent
}

func main() {
	home, _ := os.UserHomeDir()
	cookiesPath := flag.String("cookies", "", "cookies JSON file, outside the repo")
	stateDir := flag.String("state", filepath.Join(home, ".termeownal-spike"), "dir for saved state, seq ID and log, outside the repo")
	long := flag.Bool("long", false, "send: THREAD_ID is the long ID (for labelling output only)")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()
	if *cookiesPath == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := newSpike(*cookiesPath, *stateDir)
	if err == nil {
		err = s.run(ctx, flag.Arg(0), flag.Args()[1:], *long)
		s.cli.Disconnect()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newSpike(cookiesPath, dir string) (*spike, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(cookiesPath)
	if err != nil {
		return nil, fmt.Errorf("read cookies: %w", err)
	}
	c := &cookies.Cookies{Platform: types.Instagram}
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse cookies (expected a JSON object of name -> value): %w", err)
	}
	if missing := c.GetMissingCookieNames(); len(missing) > 0 {
		return nil, fmt.Errorf("missing required cookies: %v", missing)
	}

	lf, err := os.OpenFile(filepath.Join(dir, logFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	// Info, not debug: upstream may log payloads at lower levels (AGENTS.md rule 4).
	log := zerolog.New(lf).Level(zerolog.InfoLevel).With().Timestamp().Logger()

	s := &spike{dir: dir, events: make(chan slidetypes.ClientEvent, 1024)}
	s.cli = instameow.NewClient(instameow.ClientParams{
		Cookies: c,
		Log:     log,
		// Block rather than drop when full, like the real app will (rule 8).
		EventHandler: func(ctx context.Context, evt slidetypes.ClientEvent) error {
			select {
			case s.events <- evt:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	return s, nil
}

func (s *spike) run(ctx context.Context, cmd string, args []string, long bool) error {
	switch cmd {
	case "index":
		return s.index(ctx)
	case "history":
		n := 20
		if len(args) == 2 {
			n, _ = strconv.Atoi(args[1])
		}
		if len(args) < 1 || n <= 0 {
			return errors.New("usage: history SHORT_ID [N]")
		}
		return s.history(ctx, args[0], n)
	case "send", "send-new":
		if len(args) != 2 {
			return fmt.Errorf("usage: %s ID TEXT", cmd)
		}
		return s.send(ctx, cmd == "send-new", args[0], args[1], long)
	case "listen":
		d := 5 * time.Minute
		if len(args) == 1 {
			var err error
			if d, err = time.ParseDuration(args[0]); err != nil {
				return err
			}
		}
		return s.listen(ctx, d)
	case "markread":
		if len(args) != 1 {
			return errors.New("usage: markread SHORT_ID")
		}
		return s.markRead(ctx, args[0])
	case "newdm":
		if len(args) != 1 {
			return errors.New("usage: newdm USER_FBID")
		}
		return s.newDM(ctx, args[0])
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// resume mirrors igconnector's connectWithRetry: fast, warm or cold.
func (s *spike) resume(ctx context.Context) error {
	if b, err := os.ReadFile(filepath.Join(s.dir, seqFile)); err == nil {
		var seq struct {
			SeqID int64
			TS    time.Time
		}
		if err := json.Unmarshal(b, &seq); err == nil && seq.SeqID != 0 {
			s.cli.SetSeqID(seq.SeqID, seq.TS)
		}
	}
	if state, err := os.ReadFile(filepath.Join(s.dir, stateFile)); err == nil {
		if err := s.cli.LoadState(state); err != nil {
			fmt.Println("saved state unusable:", err)
		} else if s.cli.HasSeqID() {
			fmt.Println("resume path: FAST (saved state + seq ID)")
			return nil
		}
	}
	if s.cli.HasSeqID() {
		fmt.Println("resume path: WARM (seq ID only -> ReloadIndex)")
		if _, err := s.cli.ReloadIndex(ctx); err != nil {
			if errors.Is(err, types.ErrPleaseReloadPage) {
				_ = os.Remove(filepath.Join(s.dir, stateFile))
			}
			return fmt.Errorf("reload index: %w", err)
		}
		s.drain()
		return nil
	}
	fmt.Println("resume path: COLD (no seq ID -> LoadIndex)")
	_, err := s.loadIndex(ctx)
	return err
}

func (s *spike) loadIndex(ctx context.Context) (*slidetypes.Mailbox, error) {
	viewer, mailbox, err := s.cli.LoadIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}
	s.drain()
	s.saveSeq(mailbox.UQSeqID, time.Now())
	fmt.Printf("viewer: igid=%s fbid=%d mailbox_seq=%d\n", viewer.ID, s.cli.GetOwnFBID(), mailbox.UQSeqID)
	return mailbox, nil
}

func (s *spike) index(ctx context.Context) error {
	mailbox, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	fmt.Println("# pinned")
	for _, t := range mailbox.PinnedThreadsV2 {
		printThread(t)
	}
	fmt.Println("# threads_by_folder")
	for _, e := range mailbox.ThreadsByFolder.Edges {
		printThread(e.Node)
	}
	fmt.Printf("has_next_page=%t\n", mailbox.ThreadsByFolder.PageInfo.HasNextPage)
	return nil
}

func printThread(w slidetypes.WrappedThreadInfo) {
	t := w.AsIGDirectThread
	if t == nil {
		fmt.Printf("- wrapper_id=%s (no as_ig_direct_thread)\n", w.ID)
		return
	}
	fmt.Printf("- key=%d id=%s thread_fbid=%s thread_id=%s wrapper_id=%s group=%t subtype=%q input_mode=%d folder=%q title=%q\n",
		t.ThreadKey, t.ID, t.ThreadFBID, t.ThreadID, w.ID, t.IsGroup, t.ThreadSubtype, t.InputMode, t.Folder, t.ThreadTitle)
	for _, u := range t.Users {
		fmt.Printf("    user igid=%s fbid=%d username=%s\n", u.ID, u.InteropMessagingUserFBID, u.Username)
	}
}

func (s *spike) history(ctx context.Context, shortID string, n int) error {
	if err := s.resume(ctx); err != nil {
		return err
	}
	resp, err := s.cli.PaginateMessages(ctx, &slidetypes.PaginateMessagesRequest{
		ThreadID:                shortID,
		FirstN:                  n,
		InitialMessagePageCount: n,
	})
	if err != nil {
		return fmt.Errorf("paginate messages: %w", err)
	}
	t := resp.ThreadInfo.AsIGDirectThread
	if t == nil || t.Messages == nil {
		return errors.New("response has no messages")
	}
	fmt.Printf("thread id=%s wrapper_id=%s messages=%d has_next_page=%t\n",
		t.ID, resp.ThreadInfo.ID, len(t.Messages.Edges), t.Messages.PageInfo.HasNextPage)
	for _, e := range t.Messages.Edges {
		fmt.Println("-", describeMessage(e.Node))
	}
	return nil
}

func describeMessage(m *slidetypes.Message) string {
	if m == nil {
		return "nil message"
	}
	return fmt.Sprintf("id=%s message_id=%s thread_fbid=%s sender=%d ts=%s content_type=%q typename=%q otid=%s text_len=%d snippet_len=%d reply_to=%s reactions=%d forwarded=%t edits=%d",
		m.ID, m.MessageID, m.ThreadFBID, m.SenderFBID, m.TimestampMS.Format(time.RFC3339), m.ContentType, m.Typename,
		m.OfflineThreadingID, len(m.TextBody), len(m.IGDSnippet), m.RepliedToMessageID, len(m.Reactions),
		m.IGDIsForwarded, len(m.SlideEditHistory))
}

func (s *spike) connect(ctx context.Context) error {
	if err := s.resume(ctx); err != nil {
		return err
	}
	go s.cli.Connect(ctx)
	for {
		select {
		case evt := <-s.events:
			fmt.Println(s.process(evt))
			if _, ok := evt.(*slidetypes.Connected); ok {
				return nil
			}
		case <-time.After(30 * time.Second):
			return errors.New("no Connected event within 30s")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *spike) send(ctx context.Context, toUser bool, id, text string, long bool) error {
	if err := s.connect(ctx); err != nil {
		return err
	}
	otid := strconv.FormatInt(methods.GenerateEpochID(), 10)
	req := &slidetypes.SendTextRequest{
		OfflineThreadingID: otid,
		Text:               slidetypes.SensitiveString{Value: text},
		SendAttribution:    new("igd_web_chat_tab:in_thread"),
	}
	label := "short"
	switch {
	case toUser:
		req.RecipientIGIDs = []string{id}
		label = "recipient_fbid"
	case long:
		req.IGThreadIGID = &id
		label = "long"
	default:
		req.IGThreadIGID = &id
	}
	fmt.Printf("sending via %s id=%s otid=%s\n", label, id, otid)
	resp, err := s.cli.SendMessage(ctx, req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	fmt.Printf("send response: id=%s message_id=%s ts=%s\n", resp.Message.ID, resp.Message.MessageID, resp.Message.TimestampMS.Format(time.RFC3339))

	timeout := time.After(30 * time.Second)
	for {
		select {
		case evt := <-s.events:
			fmt.Println(s.process(evt))
			if d, ok := evt.(*slidetypes.Delta); ok {
				if nm, ok := d.Data.(*slidetypes.NewMessageEvent); ok && nm.Message != nil && nm.Message.OfflineThreadingID == otid {
					fmt.Printf("ECHO MATCHED: otid=%s id=%s (send response id=%s, equal=%t)\n",
						otid, nm.Message.ID, resp.Message.ID, nm.Message.ID == resp.Message.ID)
					return nil
				}
			}
		case <-timeout:
			return errors.New("no echo with matching OTID within 30s")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *spike) listen(ctx context.Context, d time.Duration) error {
	if err := s.connect(ctx); err != nil {
		return err
	}
	fmt.Printf("listening for %s; message the test account from your phone now\n", d)
	timeout := time.After(d)
	for {
		select {
		case evt := <-s.events:
			fmt.Println(s.process(evt))
		case <-timeout:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *spike) markRead(ctx context.Context, shortID string) error {
	if err := s.resume(ctx); err != nil {
		return err
	}
	resp, err := s.cli.GetThread(ctx, slidetypes.MakeGetThreadInfoRequest(shortID))
	if err != nil {
		return fmt.Errorf("get thread: %w", err)
	}
	t := resp.ThreadInfo.AsIGDirectThread
	if t == nil || t.SlideMessages == nil || len(t.SlideMessages.Edges) == 0 {
		return errors.New("thread has no messages")
	}
	printThread(resp.ThreadInfo)
	newest := slices.MaxFunc(t.SlideMessages.Edges, func(a, b slidetypes.Node[*slidetypes.Message]) int {
		return a.Node.TimestampMS.Compare(b.Node.TimestampMS.Time)
	}).Node
	fmt.Println("newest:", describeMessage(newest))

	meta := slidetypes.MarkReadMetadata{IGThreadIGID: t.ThreadID}
	fmt.Printf("MarkRead with long id=%s message id=%s\n", t.ThreadID, newest.ID)
	if _, err := s.cli.MarkRead(ctx, &slidetypes.MarkReadRequest{
		Metadata: meta,
		Data:     slidetypes.MarkReadData{MessageID: newest.ID, ItemID: new("")},
	}); err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if _, err := s.cli.MarkReadValidation(ctx, &slidetypes.MarkReadRequest{
		Metadata: meta,
		Data:     slidetypes.MarkReadData{MessageID: newest.ID, MessageTimestampMS: jsontime.UnixMilliString{Time: newest.TimestampMS.Time}},
	}); err != nil {
		return fmt.Errorf("mark read validation: %w", err)
	}
	fmt.Println("mark read OK; check the phone")
	return nil
}

func (s *spike) newDM(ctx context.Context, userFBID string) error {
	fbid, err := strconv.ParseInt(userFBID, 10, 64)
	if err != nil {
		return fmt.Errorf("USER_FBID must be a number: %w", err)
	}
	if err := s.resume(ctx); err != nil {
		return err
	}
	ids, err := s.cli.FetchThreadID(ctx, fbid)
	switch {
	case errors.Is(err, instameow.ErrThreadNotFound):
		fmt.Println("FetchThreadID: no thread yet (ErrThreadNotFound) -> try send-new")
	case err != nil:
		return fmt.Errorf("fetch thread id: %w", err)
	default:
		fmt.Printf("FetchThreadID: long=%s short=%s\n", ids.LongID, ids.ShortID)
	}
	user, err := s.cli.GetUserForNewDM(ctx, fbid)
	if err != nil {
		return fmt.Errorf("get user for new dm: %w", err)
	}
	if user.Data != nil {
		fmt.Printf("user igid=%s fbid=%d username=%s\n", user.Data.ID, user.Data.InteropMessagingUserFBID, user.Data.Username)
	}
	return nil
}

// drain handles events emitted synchronously during LoadIndex/ReloadIndex.
func (s *spike) drain() {
	for {
		select {
		case evt := <-s.events:
			fmt.Println(s.process(evt))
		default:
			return
		}
	}
}

// process saves state and seq IDs and returns a one-line, text-free description.
func (s *spike) process(evt slidetypes.ClientEvent) string {
	switch e := evt.(type) {
	case *slidetypes.Connected:
		return fmt.Sprintf("[Connected] subscribed_seq=%d latest_seq=%d", e.SubscribedSeqID, e.LatestSeqID)
	case *slidetypes.Disconnected:
		return fmt.Sprintf("[Disconnected] failures=%d err=%v", e.FailureCount, e.Error)
	case *slidetypes.AuthError:
		return fmt.Sprintf("[AuthError] %v", e.Error)
	case *slidetypes.ResnapshotRequired:
		return "[ResnapshotRequired]"
	case *slidetypes.ReconnectionStateUpdate:
		s.writeFile(stateFile, e.State)
		return fmt.Sprintf("[ReconnectionStateUpdate] saved %d bytes", len(e.State))
	case *slidetypes.SeqIDUpdate:
		s.saveSeq(e.SeqID, e.Timestamp)
		return fmt.Sprintf("[SeqIDUpdate] seq=%d", e.SeqID)
	case *slidetypes.TypingNotification:
		return fmt.Sprintf("[Typing] thread_id=%s sender=%d ttl=%d activity=%d", e.ThreadID, e.SenderID, e.TTL, e.ActivityStatus)
	case *slidetypes.Delta:
		return fmt.Sprintf("[Delta %s] seq=%d delta.thread_fbid=%s %s", e.TypeName, e.UQSeqID, e.ThreadIGID, describeDelta(e.Data))
	}
	return fmt.Sprintf("[%T]", evt)
}

func describeDelta(d slidetypes.DeltaEvent) string {
	switch e := d.(type) {
	case *slidetypes.NewMessageEvent:
		return fmt.Sprintf("NewMessage read_effect=%q %s", e.ThreadReadStateEffect, describeMessage(e.Message))
	case *slidetypes.AdminMessageEvent:
		return "AdminMessage " + describeMessage(e.Message)
	case *slidetypes.EditMessageEvent:
		return fmt.Sprintf("Edit message_id=%s text_len=%d", e.MessageID, len(e.TextBody))
	case *slidetypes.DeleteMessageEvent:
		return "Delete message_id=" + e.MessageID
	case *slidetypes.CreateReactionEvent:
		return fmt.Sprintf("ReactionAdd message_id=%s sender=%d emoji=%s", e.MessageID, e.Reaction.SenderFBID, e.Reaction.Reaction)
	case *slidetypes.DeleteReactionEvent:
		return fmt.Sprintf("ReactionRemove message_id=%s sender=%d", e.MessageID, e.Reaction.SenderFBID)
	case *slidetypes.ReadReceiptEvent:
		return fmt.Sprintf("ReadReceipt participant=%d watermark=%s", e.ReadReceipt.ParticipantFBID, e.ReadReceipt.WatermarkTimestampMS.Format(time.RFC3339))
	case *slidetypes.MarkReadEvent:
		return "MarkRead ts=" + e.ReadTimestampMS.Format(time.RFC3339)
	case *slidetypes.MarkUnreadEvent:
		return fmt.Sprintf("MarkUnread marked=%t", e.MarkedAsUnread)
	case slidetypes.UnknownEvent:
		return "Unknown keys=" + mapKeys(e)
	case slidetypes.IgnoredEvent:
		return "Ignored keys=" + mapKeys(e)
	}
	return fmt.Sprintf("%T", d)
}

func mapKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func (s *spike) saveSeq(seq int64, ts time.Time) {
	b, _ := json.Marshal(struct {
		SeqID int64
		TS    time.Time
	}{seq, ts})
	s.writeFile(seqFile, b)
}

// writeFile writes atomically with 0600 permissions; these files hold session tokens.
func (s *spike) writeFile(name string, data []byte) {
	tmp := filepath.Join(s.dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write", name, err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		fmt.Fprintln(os.Stderr, "rename", name, err)
	}
}
