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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/dgw"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"

	"termeownal/internal/core"
)

var testCookies = map[cookies.MetaCookieName]string{
	cookies.IGCookieSessionID: "fake-session-value",
	cookies.IGCookieCSRFToken: "fake-csrf-value",
	cookies.IGCookieDSUserID:  "100",
}

var testState = json.RawMessage(`{"fake":"saved state"}`)

// dave has no thread in the fixture; live deltas introduce him.
var dave = core.User{ID: 10000000000000009, Username: "dave.example"}

// fakeUpstream stands in for instameow.Client. Like upstream, it has a seq
// ID if it was built with one or has loaded the index.
type fakeUpstream struct {
	mailbox *slidetypes.MailboxResponse
	threads map[string]*slidetypes.ThreadInfo // GetThread, by short ID
	ids     map[int64]*instameow.ThreadIGIDs  // FetchThreadID
	page    *slidetypes.PaginateMessagesResponse
	connect func(context.Context) // Connect's body; by default it waits for ctx

	mu       sync.Mutex
	params   instameow.ClientParams
	seq      int64
	loaded   json.RawMessage
	fail     map[string]error // the next call of each method fails
	calls    []string
	requests []any
}

var _ upstream = (*fakeUpstream)(nil)

func newFakeUpstream(t *testing.T) *fakeUpstream {
	return &fakeUpstream{
		mailbox: loadMailbox(t),
		threads: map[string]*slidetypes.ThreadInfo{},
		ids:     map[int64]*instameow.ThreadIGIDs{},
		fail:    map[string]error{},
	}
}

// call records a call and returns its planned error.
func (f *fakeUpstream) call(name string, req any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	if req != nil {
		f.requests = append(f.requests, req)
	}
	err := f.fail[name]
	delete(f.fail, name)
	return err
}

// takeCalls returns the calls since the last takeCalls, and their requests.
func (f *fakeUpstream) takeCalls() ([]string, []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls, reqs := f.calls, f.requests
	f.calls, f.requests = nil, nil
	return calls, reqs
}

func (f *fakeUpstream) LoadState(s json.RawMessage) error {
	f.mu.Lock()
	f.loaded = s
	f.mu.Unlock()
	return f.call("LoadState", nil)
}

func (f *fakeUpstream) HasSeqID() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seq != 0
}

func (f *fakeUpstream) LoadIndex(context.Context) (*types.PolarisViewer, *slidetypes.Mailbox, error) {
	if err := f.call("LoadIndex", nil); err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	f.seq = f.mailbox.Mailbox.UQSeqID
	f.mu.Unlock()
	viewer := &types.PolarisViewer{Data: types.ViewerData{Username: testMe.Username, FullName: testMe.Name}}
	return viewer, f.mailbox.Mailbox, nil
}

func (f *fakeUpstream) ReloadIndex(context.Context) (bool, error) {
	return true, f.call("ReloadIndex", nil)
}

func (f *fakeUpstream) GetMailbox(context.Context) (*slidetypes.MailboxResponse, error) {
	if err := f.call("GetMailbox", nil); err != nil {
		return nil, err
	}
	return f.mailbox, nil
}

func (*fakeUpstream) GetOwnFBID() int64 { return int64(testMe.ID) }

func (f *fakeUpstream) GetCookies() *cookies.Cookies {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params.Cookies
}

func (f *fakeUpstream) GetThread(_ context.Context, req *slidetypes.GetThreadInfoRequest) (*slidetypes.ThreadInfoResponse, error) {
	if err := f.call("GetThread "+req.ThreadFBID, nil); err != nil {
		return nil, err
	}
	return &slidetypes.ThreadInfoResponse{ThreadInfo: slidetypes.WrappedThreadInfo{AsIGDirectThread: f.threads[req.ThreadFBID]}}, nil
}

func (f *fakeUpstream) FetchThreadID(_ context.Context, key int64) (*instameow.ThreadIGIDs, error) {
	if err := f.call(fmt.Sprint("FetchThreadID ", key), nil); err != nil {
		return nil, err
	}
	if ids, ok := f.ids[key]; ok {
		return ids, nil
	}
	return nil, instameow.ErrThreadNotFound
}

func (f *fakeUpstream) PaginateMessages(_ context.Context, req *slidetypes.PaginateMessagesRequest) (*slidetypes.PaginateMessagesResponse, error) {
	return f.page, f.call("PaginateMessages", req)
}

func (f *fakeUpstream) SendMessage(_ context.Context, req *slidetypes.SendTextRequest) (*slidetypes.SendTextResponse, error) {
	return &slidetypes.SendTextResponse{}, f.call("SendMessage", req)
}

func (f *fakeUpstream) MarkRead(_ context.Context, req *slidetypes.MarkReadRequest) (*slidetypes.MarkReadResponse, error) {
	return &slidetypes.MarkReadResponse{}, f.call("MarkRead", req)
}

func (f *fakeUpstream) MarkReadValidation(_ context.Context, req *slidetypes.MarkReadRequest) (*slidetypes.MarkReadValidationResponse, error) {
	return &slidetypes.MarkReadValidationResponse{}, f.call("MarkReadValidation", req)
}

func (f *fakeUpstream) Connect(ctx context.Context) {
	_ = f.call("Connect", nil)
	if f.connect != nil {
		f.connect(ctx)
		return
	}
	<-ctx.Done()
}

func (f *fakeUpstream) Disconnect() { _ = f.call("Disconnect", nil) }

// emit delivers an event as upstream does, from inside Connect.
func (f *fakeUpstream) emit(ctx context.Context, ev slidetypes.ClientEvent) error {
	f.mu.Lock()
	handle := f.params.EventHandler
	f.mu.Unlock()
	return handle(ctx, ev)
}

// testSession is a saved session with the test cookies.
func testSession(state json.RawMessage, seq int64) core.Session {
	auth, err := json.Marshal(authData{Cookies: testCookies, State: state})
	if err != nil {
		panic(err)
	}
	return core.Session{Auth: auth, SeqID: seq, SeqAt: time.UnixMilli(1767225000000)}
}

// newTestBackend returns a Backend restored from s, using f for Instagram.
func newTestBackend(t *testing.T, f *fakeUpstream, s core.Session) *Backend {
	t.Helper()
	return restoreWith(t, New(zerolog.Nop()), f, s)
}

func restoreWith(t *testing.T, b *Backend, f *fakeUpstream, s core.Session) *Backend {
	t.Helper()
	b.newClient = func(p instameow.ClientParams) upstream {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.params, f.seq = p, p.SeqID
		return f
	}
	if err := b.Restore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

// snapshot loads the fixture's inbox, so b knows its threads' IDs.
func snapshot(t *testing.T, b *Backend, f *fakeUpstream) {
	t.Helper()
	if _, _, err := b.Snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.takeCalls()
}

// handle delivers an event that must not fail.
func handle(t *testing.T, b *Backend, ev slidetypes.ClientEvent) {
	t.Helper()
	if err := b.handle(t.Context(), ev); err != nil {
		t.Fatalf("handle %T: %v", ev, err)
	}
}

// delta parses a delta as upstream does.
func delta(t *testing.T, js string) *slidetypes.Delta {
	t.Helper()
	var d slidetypes.Delta
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

// expect checks the next events, then that no more are waiting.
func expect(t *testing.T, b *Backend, want ...core.Event) {
	t.Helper()
	for _, w := range want {
		select {
		case got := <-b.Events():
			if !reflect.DeepEqual(got, w) {
				t.Fatalf("event:\n got %#v\nwant %#v", got, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no event; want %#v", w)
		}
	}
	select {
	case got := <-b.Events():
		t.Fatalf("unexpected event %#v", got)
	default:
	}
}

func TestRestoreRejectsAnUnusableLogin(t *testing.T) {
	noSession := maps.Clone(testCookies)
	delete(noSession, cookies.IGCookieSessionID)
	noSessionAuth, _ := json.Marshal(authData{Cookies: noSession})
	for name, auth := range map[string]core.AuthData{
		"not JSON":      core.AuthData(`fake-session-value`),
		"no session ID": noSessionAuth,
	} {
		t.Run(name, func(t *testing.T) {
			b := New(zerolog.Nop())
			defer b.Close()
			err := b.Restore(t.Context(), core.Session{Auth: auth})
			if !errors.Is(err, core.ErrSessionExpired) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "fake-") {
				t.Fatalf("error shows the login: %v", err)
			}
		})
	}
}

func TestSnapshotResumes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state json.RawMessage
		seq   int64
		fail  map[string]error
		calls []string
		cold  bool
	}{
		{name: "fast", state: testState, seq: 400,
			calls: []string{"LoadState", "GetMailbox"}},
		{name: "warm", seq: 400,
			calls: []string{"ReloadIndex", "GetMailbox"}},
		{name: "warm, state too old", state: testState, seq: 400, fail: map[string]error{"LoadState": instameow.ErrCachedStateTooOld},
			calls: []string{"LoadState", "ReloadIndex", "GetMailbox"}},
		{name: "cold", state: testState,
			calls: []string{"LoadState", "LoadIndex"}, cold: true},
		{name: "state rejected", state: testState, seq: 400, fail: map[string]error{"GetMailbox": types.ErrPleaseReloadPage},
			calls: []string{"LoadState", "GetMailbox", "Disconnect", "LoadIndex"}, cold: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUpstream(t)
			maps.Copy(f.fail, tc.fail)
			b := newTestBackend(t, f, testSession(tc.state, tc.seq))
			me, threads, err := b.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if me != testMe {
				t.Errorf("me = %+v", me)
			}
			if !reflect.DeepEqual(threads, fixtureThreads) {
				t.Errorf("threads:\n got %+v\nwant %+v", threads, fixtureThreads)
			}
			if calls, _ := f.takeCalls(); !slices.Equal(calls, tc.calls) {
				t.Errorf("calls = %q, want %q", calls, tc.calls)
			}
			// Snapshot runs on the pump goroutine, so it sends nothing; a cold
			// start's seq ID comes first from Connect.
			expect(t, b)
			f.connect = func(context.Context) {}
			_ = b.Connect(t.Context())
			if tc.cold {
				ev := <-b.Events()
				if seq, ok := ev.(core.SeqAdvanced); !ok || seq.SeqID != 500 {
					t.Fatalf("first event = %#v", ev)
				}
			}
			expect(t, b)
		})
	}
}

func TestSnapshotFails(t *testing.T) {
	f := newFakeUpstream(t)
	f.fail["GetMailbox"] = httpclient.ErrTokenInvalidated
	b := newTestBackend(t, f, testSession(testState, 400))
	if _, _, err := b.Snapshot(t.Context()); !errors.Is(err, core.ErrSessionExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeltas(t *testing.T) {
	f := newFakeUpstream(t)
	group := *f.mailbox.Mailbox.ThreadsByFolder.Edges[1].Node.AsIGDirectThread
	group.ThreadTitle = "Meow Club"
	f.threads[group.ID] = &group
	f.threads["3400000000000009"] = &slidetypes.ThreadInfo{
		ID: "3400000000000009", ThreadKey: int64(dave.ID), ThreadID: "340282366841710300949128000000000000009",
		ThreadSubtype: slidetypes.ThreadSubtypeDirect,
		Users:         []*slidetypes.User{{InteropMessagingUserFBID: int64(dave.ID), Username: dave.Username}},
	}
	b := newTestBackend(t, f, testSession(testState, 400))
	snapshot(t, b, f)

	davesThread := core.Thread{ID: core.ThreadID(dave.ID), Title: dave.Username, Members: []core.User{dave}, Unread: true}
	renamed := fixtureThreads[1]
	renamed.Title = "Meow Club"
	message := func(id string, thread core.ThreadID, sender core.UserID) core.Event {
		return core.MessageAdded{Message: core.Message{ID: core.MessageID(id), Thread: thread, Sender: sender, At: time.UnixMilli(1767225700000), Text: "placeholder live", ContentType: "TEXT"}}
	}
	newMessage := func(id, deltaShort, msgShort string, sender core.UserID) string {
		return fmt.Sprintf(`{"__typename":"SlideUQPPNewMessage","thread_fbid":%q,"message":{"message_id":%q,"sender_fbid":"%d","thread_fbid":%q,"content_type":"TEXT","timestamp_ms":"1767225700000","text_body":"placeholder live"}}`, deltaShort, id, sender, msgShort)
	}
	for _, step := range []struct {
		name  string
		delta string
		want  []core.Event
		calls []string
	}{
		{name: "message in a known thread", delta: newMessage("mid.$live1", "3400000000000001", "3400000000000001", alice.ID),
			want: []core.Event{message("mid.$live1", core.ThreadID(alice.ID), alice.ID)}},
		{name: "message in a new thread fetches it first", delta: newMessage("mid.$live2", "3400000000000009", "3400000000000009", dave.ID),
			want:  []core.Event{core.ThreadUpdated{Thread: davesThread}, message("mid.$live2", core.ThreadID(dave.ID), dave.ID)},
			calls: []string{"GetThread 3400000000000009"}},
		{name: "then it's known", delta: newMessage("mid.$live3", "3400000000000009", "3400000000000009", testMe.ID),
			want: []core.Event{message("mid.$live3", core.ThreadID(dave.ID), testMe.ID)}},
		{name: "short ID only on the message", delta: newMessage("mid.$live4", "", "3400000000000002", bob.ID),
			want: []core.Event{message("mid.$live4", 3400000000000002, bob.ID)}},
		{name: "mark read", delta: `{"__typename":"SlideUQPPMarkRead","thread_fbid":"3400000000000002","read_timestamp_ms":"1767225800000"}`,
			want: []core.Event{core.ThreadRead{Thread: 3400000000000002, Until: time.UnixMilli(1767225800000)}}},
		{name: "mark read in an unknown thread", delta: `{"__typename":"SlideUQPPMarkRead","thread_fbid":"3400000000000008","read_timestamp_ms":"1767225800000"}`},
		{name: "rename fetches the thread", delta: `{"__typename":"SlideUQPPThreadName","thread_fbid":"3400000000000002","thread_name":"Meow Club"}`,
			want: []core.Event{core.ThreadUpdated{Thread: renamed}}, calls: []string{"GetThread 3400000000000002"}},
		{name: "no thread", delta: `{"__typename":"SlideUQPPCreate1To1Thread","thread_fbid":""}`},
		{name: "unknown type", delta: `{"__typename":"SlideUQPPSomethingNew","thread_fbid":"3400000000000001"}`},
		{name: "a thread termeownal can't show", delta: newMessage("mid.$live5", "3400000000000007", "3400000000000007", dave.ID),
			calls: []string{"GetThread 3400000000000007"}},
	} {
		handle(t, b, delta(t, step.delta))
		expect(t, b, step.want...)
		if calls, _ := f.takeCalls(); !slices.Equal(calls, step.calls) {
			t.Errorf("%s: calls = %q, want %q", step.name, calls, step.calls)
		}
	}
}

func TestDeltaFailsIfTheThreadCantBeFetched(t *testing.T) {
	f := newFakeUpstream(t)
	f.fail["GetThread 3400000000000009"] = errors.New("fake network error")
	b := newTestBackend(t, f, testSession(testState, 400))
	// The error makes upstream reconnect and deliver the delta again.
	err := b.handle(t.Context(), delta(t, `{"__typename":"SlideUQPPNewMessage","thread_fbid":"3400000000000009","message":{"message_id":"mid.$live1","sender_fbid":"1","content_type":"TEXT","timestamp_ms":"1767225700000"}}`))
	if err == nil {
		t.Fatal("no error")
	}
	expect(t, b)
}

func TestSessionChanges(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(nil, 400))
	at := time.UnixMilli(1767225900000)

	handle(t, b, &slidetypes.SeqIDUpdate{SeqID: 401, Timestamp: at})
	expect(t, b, core.SeqAdvanced{SeqID: 401, At: at})

	// New client state is saved with the next seq ID, then not again.
	state := json.RawMessage(`{"fake":"new state"}`)
	handle(t, b, &slidetypes.ReconnectionStateUpdate{State: state})
	expect(t, b)
	handle(t, b, &slidetypes.SeqIDUpdate{SeqID: 402, Timestamp: at})
	expect(t, b, core.SeqAdvanced{SeqID: 402, At: at}, core.SessionChanged{})
	if _, err := b.ExportAuth(); err != nil {
		t.Fatal(err)
	}
	handle(t, b, &slidetypes.SeqIDUpdate{SeqID: 403, Timestamp: at})
	expect(t, b, core.SeqAdvanced{SeqID: 403, At: at})

	// So are cookies Instagram changed.
	f.GetCookies().Set(cookies.IGCookieRUR, "fake-rur-value")
	handle(t, b, &slidetypes.Connected{})
	expect(t, b, core.StatusChanged{Status: core.Connected}, core.SessionChanged{})
	auth, err := b.ExportAuth()
	if err != nil {
		t.Fatal(err)
	}

	// The next start resumes with them.
	f2 := newFakeUpstream(t)
	newTestBackend(t, f2, core.Session{Auth: auth, SeqID: 403})
	want := maps.Clone(testCookies)
	want[cookies.IGCookieRUR] = "fake-rur-value"
	if got := f2.GetCookies().GetAll(); !maps.Equal(got, want) {
		t.Errorf("cookies = %v", slices.Collect(maps.Keys(got)))
	}
	if string(f2.loaded) != string(state) {
		t.Errorf("state = %s", f2.loaded)
	}
}

func TestHandlerBlocksOnAFullQueue(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(nil, 400))
	for i := range queueSize {
		handle(t, b, &slidetypes.SeqIDUpdate{SeqID: int64(i + 1)})
	}
	// These arrive during HTTP calls, so they never wait.
	handle(t, b, &slidetypes.ReconnectionStateUpdate{State: testState})
	handle(t, b, &slidetypes.ResnapshotRequired{})
	handle(t, b, &slidetypes.AuthError{Error: errors.New("fake auth error")})

	done := make(chan error, 1)
	go func() { done <- b.handle(t.Context(), &slidetypes.SeqIDUpdate{SeqID: queueSize + 1}) }()
	select {
	case err := <-done:
		t.Fatalf("handle returned %v with a full queue", err)
	case <-time.After(50 * time.Millisecond):
	}
	for i := range queueSize + 1 {
		if ev := <-b.Events(); ev != (core.SeqAdvanced{SeqID: int64(i + 1)}) {
			t.Fatalf("event %d = %#v", i, ev)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	expect(t, b, core.SessionChanged{})
}

func TestCloseUnblocksTheHandler(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(nil, 400))
	for i := range queueSize {
		handle(t, b, &slidetypes.SeqIDUpdate{SeqID: int64(i + 1)})
	}
	done := make(chan error, 1)
	go func() { done <- b.handle(t.Context(), &slidetypes.SeqIDUpdate{SeqID: queueSize + 1}) }()
	time.Sleep(10 * time.Millisecond)
	b.Close()
	if err := <-done; err == nil {
		t.Fatal("blocked handle returned nil after Close")
	}
	if err := b.handle(t.Context(), &slidetypes.Connected{}); err == nil {
		t.Fatal("handle returned nil after Close")
	}
	n := 0
	for range b.Events() {
		n++
	}
	if n != queueSize {
		t.Fatalf("%d events", n)
	}
}

func TestConnectReportsStatus(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(nil, 400))
	f.connect = func(ctx context.Context) {
		_ = f.emit(ctx, &slidetypes.Connected{})
		_ = f.emit(ctx, &slidetypes.Disconnected{Error: errors.New("fake drop"), FailureCount: 1})
	}
	if err := b.Connect(t.Context()); err == nil {
		t.Fatal("Connect returned nil when the connection ended")
	}
	expect(t, b, core.StatusChanged{Status: core.Connected}, core.StatusChanged{Status: core.Reconnecting})
}

func TestConnectAsksForAResync(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(testState, 400))
	snapshot(t, b, f)
	f.connect = func(ctx context.Context) {
		// Upstream forgets its seq ID, then Connect returns.
		f.mu.Lock()
		f.seq = 0
		f.mu.Unlock()
		_ = f.emit(ctx, &slidetypes.ResnapshotRequired{})
	}
	if err := b.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	expect(t, b, core.ResyncNeeded{})

	// The pump snapshots again, which starts cold, and reconnects.
	snapshot(t, b, f)
	f.connect = func(context.Context) {}
	_ = b.Connect(t.Context())
	if ev := <-b.Events(); ev.(core.SeqAdvanced).SeqID != 500 {
		t.Fatalf("event = %#v", ev)
	}
}

func TestConnectStopsWhenTheLoginFails(t *testing.T) {
	for name, ev := range map[string]slidetypes.ClientEvent{
		"socket refused":   &slidetypes.Disconnected{Error: dgw.ErrUnauthorized},
		"user ID missing":  &slidetypes.Disconnected{Error: instameow.ErrUserIDMissing},
		"request rejected": &slidetypes.AuthError{Error: httpclient.ErrTokenInvalidated},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeUpstream(t)
			b := newTestBackend(t, f, testSession(nil, 400))
			f.connect = func(ctx context.Context) {
				_ = f.emit(ctx, ev)
				<-ctx.Done()
			}
			if err := b.Connect(t.Context()); !errors.Is(err, core.ErrSessionExpired) {
				t.Fatalf("err = %v", err)
			}
			if err := b.Connect(t.Context()); !errors.Is(err, core.ErrSessionExpired) {
				t.Fatalf("second Connect: err = %v", err)
			}
		})
	}
}

func TestCloseEndsConnect(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(nil, 400))
	started := make(chan struct{})
	f.connect = func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}
	done := make(chan error, 1)
	go func() { done <- b.Connect(context.Background()) }()
	<-started
	b.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := <-b.Events(); ok {
		t.Fatal("Events not closed")
	}
	if calls, _ := f.takeCalls(); !slices.Contains(calls, "Disconnect") {
		t.Errorf("calls = %q", calls)
	}
	if _, err := b.ExportAuth(); err != nil {
		t.Errorf("ExportAuth after Close: %v", err)
	}
}

func TestHistory(t *testing.T) {
	f := newFakeUpstream(t)
	if err := json.Unmarshal([]byte(`{"fetch__SlideThread":{"id":"x","as_ig_direct_thread":{"id":"3400000000000001","slide_messages":{
		"edges":[
			{"node":{"message_id":"mid.$old2","sender_fbid":"100","content_type":"TEXT","timestamp_ms":"1767225200000","text_body":"placeholder old two"}},
			{"node":{"message_id":"mid.$old1","sender_fbid":"10000000000000001","content_type":"TEXT","timestamp_ms":"1767225100000","text_body":"placeholder old one"}}
		],
		"page_info":{"has_next_page":true}}}}}`), &f.page); err != nil {
		t.Fatal(err)
	}
	b := newTestBackend(t, f, testSession(testState, 400))
	snapshot(t, b, f)

	thread := core.ThreadID(alice.ID)
	msgs, more, err := b.History(t.Context(), thread, "mid.$fake1a", 20)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Message{
		{ID: "mid.$old1", Thread: thread, Sender: alice.ID, At: time.UnixMilli(1767225100000), Text: "placeholder old one", ContentType: "TEXT"},
		{ID: "mid.$old2", Thread: thread, Sender: testMe.ID, At: time.UnixMilli(1767225200000), Text: "placeholder old two", ContentType: "TEXT"},
	}
	if !reflect.DeepEqual(msgs, want) || !more {
		t.Fatalf("History = %+v, %v", msgs, more)
	}
	_, reqs := f.takeCalls()
	req := reqs[0].(*slidetypes.PaginateMessagesRequest)
	if req.ThreadID != "3400000000000001" || req.FirstN != 20 || req.OlderThanMessageID == nil || *req.OlderThanMessageID != "mid.$fake1a" {
		t.Fatalf("request = %+v", req)
	}

	// The newest page has no cursor.
	if _, _, err := b.History(t.Context(), thread, "", 20); err != nil {
		t.Fatal(err)
	}
	if _, reqs := f.takeCalls(); reqs[0].(*slidetypes.PaginateMessagesRequest).OlderThanMessageID != nil {
		t.Fatal("cursor sent for the newest page")
	}
}

func TestSendAndMarkRead(t *testing.T) {
	f := newFakeUpstream(t)
	b := newTestBackend(t, f, testSession(testState, 400))
	snapshot(t, b, f)
	thread := core.ThreadID(alice.ID)

	if err := b.Send(t.Context(), thread, "placeholder sent", "7000000000000000002"); err != nil {
		t.Fatal(err)
	}
	_, reqs := f.takeCalls()
	send := reqs[0].(*slidetypes.SendTextRequest)
	if *send.IGThreadIGID != "3400000000000001" || send.OfflineThreadingID != "7000000000000000002" || send.Text.Value != "placeholder sent" {
		t.Fatalf("send = %+v", send)
	}

	at := time.UnixMilli(1767225600000)
	if err := b.MarkRead(t.Context(), core.Message{ID: "mid.$fake1c", Thread: thread, At: at}); err != nil {
		t.Fatal(err)
	}
	calls, reqs := f.takeCalls()
	if !slices.Equal(calls, []string{"MarkRead", "MarkReadValidation"}) {
		t.Fatalf("calls = %q", calls)
	}
	for _, r := range reqs {
		r := r.(*slidetypes.MarkReadRequest)
		if r.Metadata.IGThreadIGID != "340282366841710300949128000000000000001" || r.Data.MessageID != "mid.$fake1c" {
			t.Fatalf("mark read = %+v", r)
		}
	}
	if got := reqs[1].(*slidetypes.MarkReadRequest).Data.MessageTimestampMS.Time; !got.Equal(at) {
		t.Fatalf("validation timestamp = %v", got)
	}

	f.fail["SendMessage"] = httpclient.ErrRateLimited
	if err := b.Send(t.Context(), thread, "placeholder sent", "7000000000000000003"); !errors.Is(err, core.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
}

func TestThreadIDLookup(t *testing.T) {
	// A thread opened from the cache before the first Snapshot.
	f := newFakeUpstream(t)
	f.ids[int64(alice.ID)] = &instameow.ThreadIGIDs{ShortID: "3400000000000001", LongID: "340282366841710300949128000000000000001"}
	f.threads["3400000000000002"] = f.mailbox.Mailbox.ThreadsByFolder.Edges[1].Node.AsIGDirectThread
	b := newTestBackend(t, f, testSession(testState, 400))
	f.takeCalls()

	for _, step := range []struct {
		name   string
		thread core.ThreadID
		calls  []string
		short  string
	}{
		{name: "1:1 by the other user's FBID", thread: core.ThreadID(alice.ID),
			calls: []string{"FetchThreadID 10000000000000001", "SendMessage"}, short: "3400000000000001"},
		{name: "then it's known", thread: core.ThreadID(alice.ID),
			calls: []string{"SendMessage"}, short: "3400000000000001"},
		{name: "group by its short ID", thread: 3400000000000002,
			calls: []string{"FetchThreadID 3400000000000002", "GetThread 3400000000000002", "SendMessage"}, short: "3400000000000002"},
		{name: "unknown", thread: 10000000000000005,
			calls: []string{"FetchThreadID 10000000000000005", "GetThread 10000000000000005"}},
	} {
		err := b.Send(t.Context(), step.thread, "placeholder sent", "7000000000000000002")
		calls, reqs := f.takeCalls()
		if !slices.Equal(calls, step.calls) {
			t.Errorf("%s: calls = %q, want %q", step.name, calls, step.calls)
		}
		if step.short == "" {
			if err == nil {
				t.Errorf("%s: no error", step.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := *reqs[0].(*slidetypes.SendTextRequest).IGThreadIGID; got != step.short {
			t.Errorf("%s: sent to %s", step.name, got)
		}
	}
}

func TestLogsHoldNoSecretsOrText(t *testing.T) {
	var buf bytes.Buffer
	f := newFakeUpstream(t)
	b := restoreWith(t, New(zerolog.New(&buf).Level(zerolog.DebugLevel)), f, testSession(testState, 400))
	snapshot(t, b, f)
	handle(t, b, delta(t, `{"__typename":"SlideUQPPNewMessage","thread_fbid":"3400000000000001","message":{"message_id":"mid.$live1","sender_fbid":"1","thread_fbid":"3400000000000001","content_type":"TEXT","timestamp_ms":"1767225700000","text_body":"placeholder live"}}`))
	handle(t, b, &slidetypes.ReconnectionStateUpdate{State: testState})
	handle(t, b, &slidetypes.Disconnected{Error: errors.New("fake drop")})
	if err := b.Send(t.Context(), core.ThreadID(alice.ID), "placeholder sent", "7000000000000000002"); err != nil {
		t.Fatal(err)
	}
	b.Close()
	if buf.Len() == 0 {
		t.Fatal("nothing logged")
	}
	for _, secret := range []string{"placeholder", "fake-session-value", "fake-csrf-value", "saved state"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("log contains %q", secret)
		}
	}
}
