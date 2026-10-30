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

// Package instagram is the Instagram backend. It's the only package that
// imports upstream (mautrix-meta); everything it returns is a core type.
package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/util/exhttp"
	"go.mau.fi/util/shlex"

	"termeownal/internal/core"
)

// pasteField is the first step's one field: a "Copy as cURL" command or a
// JSON object. Left empty, it leads to a prompt for each cookie.
const pasteField = "paste"

// codeBadCredentials is the error code Instagram uses for a login that's no
// longer valid.
const codeBadCredentials = 1357053

const loginInstructions = `Log in with cookies from your browser:
  1. In a Chromium-based browser (Chrome, Edge, Brave), open a private window
     and log in to instagram.com.
  2. Open DevTools (F12) and the Network tab, then reload the page.
  3. Right-click the first request and choose Copy > Copy as cURL (bash).
     A JSON object of cookie names and values works too.
  4. Close the window without logging out: logging out ends the session.
Nothing is shown as you paste.`

// Login logs in with cookies copied from a browser.
type Login struct {
	// check asks Instagram whose cookies they are.
	check func(context.Context, *cookies.Cookies) (core.User, error)
}

var _ core.Authenticator = (*Login)(nil)

// NewLogin returns a cookie login that logs to log.
func NewLogin(log zerolog.Logger) *Login {
	return &Login{check: func(ctx context.Context, c *cookies.Cookies) (core.User, error) {
		return whoAmI(ctx, c, log)
	}}
}

// authData is the AuthData a login produces and the backend saves: the
// cookies, and the client state that makes the next start fast.
type authData struct {
	Cookies map[cookies.MetaCookieName]string `json:"cookies"`
	State   json.RawMessage                   `json:"state,omitempty"`
}

// Start asks for a paste.
func (*Login) Start(context.Context) (core.LoginStep, error) {
	return core.LoginStep{
		Instructions: loginInstructions,
		Fields: []core.LoginField{{
			ID: pasteField, Label: "Paste (or press Enter to type each cookie)", Secret: true, Optional: true,
		}},
	}, nil
}

// Submit takes a paste, or the cookies one by one, and checks them with
// Instagram.
func (l *Login) Submit(ctx context.Context, values map[string]string) (core.LoginStep, error) {
	paste, pasted := values[pasteField]
	if pasted && strings.TrimSpace(paste) == "" {
		return cookieStep(), nil
	}
	if pasted {
		var err error
		if values, err = parsePaste(paste); err != nil {
			return core.LoginStep{}, err
		}
	}
	c := &cookies.Cookies{Platform: types.Instagram}
	known := map[cookies.MetaCookieName]string{}
	for _, name := range slices.Concat(cookies.IGRequiredCookies, cookies.IGOptionalCookies) {
		if v := strings.TrimSpace(values[string(name)]); v != "" {
			known[name] = unescape(v)
		}
	}
	c.UpdateValues(known)
	if missing := c.GetMissingCookieNames(); len(missing) > 0 {
		names := make([]string, len(missing))
		for i, name := range missing {
			names[i] = string(name)
		}
		return core.LoginStep{}, fmt.Errorf("missing cookies: %s", strings.Join(names, ", "))
	}
	me, err := l.check(ctx, c)
	if err != nil {
		return core.LoginStep{}, err
	}
	auth, err := json.Marshal(authData{Cookies: c.GetAll()})
	if err != nil {
		return core.LoginStep{}, fmt.Errorf("encode cookies: %w", err)
	}
	return core.LoginStep{Result: &core.LoginResult{Me: me, Auth: auth}}, nil
}

// cookieStep asks for each cookie.
func cookieStep() core.LoginStep {
	var fields []core.LoginField
	for _, name := range cookies.IGRequiredCookies {
		fields = append(fields, core.LoginField{ID: string(name), Label: string(name), Secret: true})
	}
	for _, name := range cookies.IGOptionalCookies {
		fields = append(fields, core.LoginField{ID: string(name), Label: string(name) + " (optional)", Secret: true, Optional: true})
	}
	return core.LoginStep{
		Instructions: "Copy each value from DevTools > Application > Cookies > https://www.instagram.com. Press Enter to skip an optional one.",
		Fields:       fields,
	}
}

// parsePaste reads cookies from a JSON object of names and values, or from
// the Cookie header of a "Copy as cURL" command. Its errors never quote the
// input.
func parsePaste(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(s), &m) != nil {
			return nil, errors.New("that isn't a JSON object of cookie names and string values")
		}
		return m, nil
	}
	// shortcut: $'…' quoting isn't understood; browsers only use it when the
	// header has a ', ! or control character, which Instagram's cookies don't.
	args, err := shlex.Split(s)
	if err != nil || len(args) == 0 || args[0] != "curl" {
		return nil, errors.New("paste a curl command or a JSON object")
	}
	var header string
	for i := 1; i < len(args)-1; i++ {
		switch next := args[i+1]; args[i] {
		case "-b", "--cookie":
			header = next
		case "-H", "--header":
			if name, value, ok := strings.Cut(next, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "cookie") {
				header = value
			}
		}
	}
	if header == "" {
		return nil, errors.New("the curl command has no cookies; copy it from a request to instagram.com")
	}
	// Split by hand: http.ParseCookie rejects the backslashes in rur.
	m := map[string]string{}
	for pair := range strings.SplitSeq(header, ";") {
		if name, value, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			m[name] = value
		}
	}
	return m, nil
}

// unescape URL-decodes a cookie value as upstream's cookie login does,
// keeping it as it is if it isn't valid URL encoding.
func unescape(v string) string {
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}

// whoAmI loads the inbox page with the cookies, which checks them and says
// whose they are.
func whoAmI(ctx context.Context, c *cookies.Cookies, log zerolog.Logger) (core.User, error) {
	client := instameow.NewClient(instameow.ClientParams{
		Cookies:  c,
		Log:      log,
		Settings: exhttp.SensibleClientSettings,
		// LoadIndex reports progress to the handler, so it can't be nil.
		EventHandler: func(context.Context, slidetypes.ClientEvent) error { return nil },
	})
	viewer, _, err := client.LoadIndex(ctx)
	if err != nil {
		return core.User{}, fmt.Errorf("load inbox: %w", coreError(err))
	}
	fbid := client.GetOwnFBID()
	if fbid == 0 {
		return core.User{}, errors.New("load inbox: Instagram didn't say which account this is")
	}
	return core.User{ID: core.UserID(fbid), Username: viewer.GetUsername(), Name: viewer.GetName()}, nil
}

// coreError wraps err with the core error callers branch on, if there is
// one (AGENTS.md §6.6).
func coreError(err error) error {
	for _, m := range []struct{ upstream, core error }{
		{httpclient.ErrRateLimited, core.ErrRateLimited},
		{httpclient.ErrChallengeRequired, core.ErrVerificationRequired},
		{httpclient.ErrCheckpointRequired, core.ErrVerificationRequired},
		{httpclient.ErrConsentRequired, core.ErrConsentRequired},
		{httpclient.ErrAccountSuspended, core.ErrAccountSuspended},
		{httpclient.ErrTokenInvalidated, core.ErrSessionExpired},
	} {
		if errors.Is(err, m.upstream) {
			return fmt.Errorf("%w: %w", m.core, err)
		}
	}
	if resp := (*types.ErrorResponse)(nil); errors.As(err, &resp) && resp.ErrorCode == codeBadCredentials {
		return fmt.Errorf("%w: %w", core.ErrSessionExpired, err)
	}
	return err
}
