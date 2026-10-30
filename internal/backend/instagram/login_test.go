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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"

	"termeownal/internal/core"
)

var testMe = core.User{ID: 100, Username: "meow.tester", Name: "Meow Tester"}

// rur is stored as the browser has it: quoted, with backslashes.
const testRUR = `"CLN\054100\0541900000000:01fakefake"`

// fakeLogin records the cookies it was asked to check.
func fakeLogin(err error) (*Login, *map[cookies.MetaCookieName]string) {
	got := new(map[cookies.MetaCookieName]string)
	return &Login{check: func(_ context.Context, c *cookies.Cookies) (core.User, error) {
		*got = c.GetAll()
		return testMe, err
	}}, got
}

func TestStartAsksForAPaste(t *testing.T) {
	step, err := (&Login{}).Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(step.Fields) != 1 || step.Fields[0].ID != pasteField || !step.Fields[0].Secret || !step.Fields[0].Optional {
		t.Fatalf("fields = %+v", step.Fields)
	}
}

func TestEmptyPasteAsksForEachCookie(t *testing.T) {
	l, _ := fakeLogin(nil)
	step, err := l.Submit(t.Context(), map[string]string{pasteField: " \n"})
	if err != nil {
		t.Fatal(err)
	}
	var required []string
	for _, f := range step.Fields {
		if !f.Secret {
			t.Errorf("%s isn't secret", f.ID)
		}
		if !f.Optional {
			required = append(required, f.ID)
		}
	}
	if fmt.Sprint(required) != "[sessionid csrftoken ds_user_id]" || len(step.Fields) != 8 || step.Result != nil {
		t.Fatalf("fields = %+v", step.Fields)
	}
}

func TestSubmitReadsEachForm(t *testing.T) {
	want := map[cookies.MetaCookieName]string{
		"sessionid":  "100:fakeSession:9:AYfake",
		"csrftoken":  "fakeCSRF",
		"ds_user_id": "100",
		"rur":        testRUR,
	}
	header := `sessionid=100%3AfakeSession%3A9%3AAYfake; csrftoken=fakeCSRF; ds_user_id=100; rur=` + testRUR + `; wd=1280x720`
	for name, values := range map[string]map[string]string{
		"curl -b": {pasteField: "curl 'https://www.instagram.com/' \\\n  -H 'accept: text/html' \\\n  -b '" + header + "' \\\n  -H 'user-agent: Mozilla/5.0'\n"},
		"curl -H": {pasteField: `curl "https://www.instagram.com/" -H "Accept: */*" -H 'Cookie: ` + header + `' --compressed`},
		"JSON": {pasteField: `{"sessionid": "100%3AfakeSession%3A9%3AAYfake", "csrftoken": "fakeCSRF", "ds_user_id": "100",
			"rur": "\"CLN\\054100\\0541900000000:01fakefake\"", "wd": "1280x720"}`},
		"guided": {"sessionid": " 100:fakeSession:9:AYfake ", "csrftoken": "fakeCSRF", "ds_user_id": "100", "rur": testRUR, "mid": ""},
	} {
		t.Run(name, func(t *testing.T) {
			l, got := fakeLogin(nil)
			step, err := l.Submit(t.Context(), values)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(*got, want) {
				t.Errorf("cookies = %q, want %q", *got, want)
			}
			if step.Result == nil || step.Result.Me != testMe {
				t.Fatalf("result = %+v", step.Result)
			}
			var saved authData
			if err := json.Unmarshal(step.Result.Auth, &saved); err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(saved.Cookies, want) {
				t.Errorf("saved cookies = %q, want %q", saved.Cookies, want)
			}
		})
	}
}

func TestBadPastesFailWithoutQuotingThem(t *testing.T) {
	for name, paste := range map[string]string{
		"missing cookie":   `{"sessionid": "fakeSecretValue", "csrftoken": "fakeSecretValue"}`,
		"bad JSON":         `{"sessionid": "fakeSecretValue"`,
		"JSON number":      `{"sessionid": "fakeSecretValue", "ds_user_id": 100}`,
		"not curl":         `sessionid=fakeSecretValue`,
		"unclosed quote":   `curl -b 'sessionid=fakeSecretValue`,
		"curl, no cookies": `curl https://www.instagram.com/ -H 'x-fake: fakeSecretValue'`,
	} {
		t.Run(name, func(t *testing.T) {
			l, got := fakeLogin(nil)
			step, err := l.Submit(t.Context(), map[string]string{pasteField: paste})
			if err == nil || step.Result != nil {
				t.Fatalf("Submit = %+v, %v; want an error", step, err)
			}
			if strings.Contains(err.Error(), "fakeSecretValue") {
				t.Errorf("error quotes the paste: %v", err)
			}
			if *got != nil {
				t.Error("checked incomplete cookies with Instagram")
			}
		})
	}
	l, _ := fakeLogin(nil)
	_, err := l.Submit(t.Context(), map[string]string{pasteField: `{"sessionid": "x", "csrftoken": "y"}`})
	if err == nil || !strings.Contains(err.Error(), "ds_user_id") {
		t.Errorf("error = %v, want it to name ds_user_id", err)
	}
}

func TestFailedCheckSavesNothing(t *testing.T) {
	l, _ := fakeLogin(core.ErrSessionExpired)
	step, err := l.Submit(t.Context(), map[string]string{"sessionid": "a", "csrftoken": "b", "ds_user_id": "1"})
	if !errors.Is(err, core.ErrSessionExpired) || step.Result != nil {
		t.Fatalf("Submit = %+v, %v", step, err)
	}
}

func TestCoreError(t *testing.T) {
	for upstream, want := range map[error]error{
		httpclient.ErrRateLimited:                            core.ErrRateLimited,
		httpclient.ErrChallengeRequired:                      core.ErrVerificationRequired,
		httpclient.ErrCheckpointRequired:                     core.ErrVerificationRequired,
		httpclient.ErrConsentRequired:                        core.ErrConsentRequired,
		httpclient.ErrAccountSuspended:                       core.ErrAccountSuspended,
		httpclient.ErrTokenInvalidated:                       core.ErrSessionExpired,
		&types.ErrorResponse{ErrorCode: 1357053}:             core.ErrSessionExpired,
		fmt.Errorf("wrapped: %w", httpclient.ErrRateLimited): core.ErrRateLimited,
	} {
		got := coreError(upstream)
		if !errors.Is(got, want) || !errors.Is(got, upstream) {
			t.Errorf("coreError(%v) = %v, want it to wrap %v and the original", upstream, got, want)
		}
	}
	other := &types.ErrorResponse{ErrorCode: 1}
	for _, sentinel := range []error{core.ErrSessionExpired, core.ErrRateLimited, core.ErrVerificationRequired} {
		if errors.Is(coreError(other), sentinel) {
			t.Errorf("coreError(%v) is %v", other, sentinel)
		}
	}
}
