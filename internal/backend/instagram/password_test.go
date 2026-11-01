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
	"errors"
	"strings"
	"testing"

	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"maunium.net/go/mautrix/bridgev2"

	"termeownal/internal/core"
)

const testPassword = "fakePassword123"

// fakeWeb plays Instagram's side of a password login. Each func that's nil
// succeeds with nothing more to ask.
type fakeWeb struct {
	create   func() (*instameow.InstagramWebTwoFactorChallenge, error)
	consent  func() (*instameow.InstagramWebTwoFactorChallenge, error)
	code     func(code string) error
	platform func(input map[string]string) (*bridgev2.LoginStep, error)
	manager  func(input map[string]string) (*bridgev2.LoginStep, error)
}

func (f *fakeWeb) CreateInstagramWebSession(_ context.Context, id, pw string) (*instameow.InstagramWebTwoFactorChallenge, error) {
	if id != "meow.tester" || pw != testPassword {
		return nil, instameow.ErrInstagramWebCredentialsRejected
	}
	return call(f.create)
}

func (f *fakeWeb) ContinueInstagramWebSessionAfterCookieConsent(context.Context, string, string) (*instameow.InstagramWebTwoFactorChallenge, error) {
	return call(f.consent)
}

func (f *fakeWeb) CompleteInstagramWebSessionTwoFactor(_ context.Context, code string) error {
	if f.code == nil {
		return nil
	}
	return f.code(code)
}

func (f *fakeWeb) DoInstagramWebAuthPlatformSteps(_ context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if f.platform == nil {
		return nil, nil
	}
	return f.platform(input)
}

func (f *fakeWeb) DoInstagramWebAccountManagerSteps(_ context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if f.manager == nil {
		return nil, nil
	}
	return f.manager(input)
}

func (f *fakeWeb) GetCookies() *cookies.Cookies {
	c := &cookies.Cookies{Platform: types.Instagram}
	c.UpdateValues(map[cookies.MetaCookieName]string{"sessionid": "fakeSession", "csrftoken": "fakeCSRF", "ds_user_id": "100"})
	return c
}

func call(f func() (*instameow.InstagramWebTwoFactorChallenge, error)) (*instameow.InstagramWebTwoFactorChallenge, error) {
	if f == nil {
		return nil, nil
	}
	return f()
}

// passwordStart starts a password login against web and returns it with the
// number of clients it has made so far.
func passwordStart(t *testing.T, web *fakeWeb) (*Login, *int) {
	t.Helper()
	clients := new(int)
	l := &Login{
		check:     func(context.Context, *cookies.Cookies) (core.User, error) { return testMe, nil },
		newClient: func() webClient { *clients++; return web },
	}
	step, err := l.Submit(t.Context(), map[string]string{methodField: methodPassword})
	if err != nil || len(step.Fields) != 2 || step.Fields[0].ID != usernameField || !step.Fields[1].Secret {
		t.Fatalf("Submit(password) = %+v, %v", step, err)
	}
	return l, clients
}

var credentials = map[string]string{usernameField: " meow.tester ", passwordField: testPassword}

// submit submits values and fails the test on an error, or on a step that
// shows the password.
func submit(t *testing.T, l *Login, values map[string]string) core.LoginStep {
	t.Helper()
	step, err := l.Submit(t.Context(), values)
	if err != nil {
		t.Fatalf("Submit(%v) = %v", values, err)
	}
	if strings.Contains(step.Instructions, testPassword) {
		t.Fatalf("step shows the password: %q", step.Instructions)
	}
	return step
}

func TestPasswordLoginWithCodeAndAccountPicker(t *testing.T) {
	var codes []string
	var picked map[string]string
	l, _ := passwordStart(t, &fakeWeb{
		create: func() (*instameow.InstagramWebTwoFactorChallenge, error) {
			return &instameow.InstagramWebTwoFactorChallenge{TOTP: true}, nil
		},
		code: func(code string) error {
			if codes = append(codes, code); len(codes) == 1 {
				return instameow.ErrInstagramWebTwoFactorCodeRejected
			}
			return nil
		},
		manager: func(input map[string]string) (*bridgev2.LoginStep, error) {
			if input["account"] == "" {
				return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeUserInput, Instructions: "Choose an account.",
					UserInputParams: &bridgev2.LoginUserInputParams{Fields: []bridgev2.LoginInputDataField{
						{Type: bridgev2.LoginInputFieldTypeSelect, ID: "account", Name: "Account", Options: []string{"meow.one", "meow.two"}},
					}}}, nil
			}
			picked = input
			return nil, nil
		},
	})
	step := submit(t, l, map[string]string{usernameField: "meow.tester", passwordField: "wrong"})
	if !strings.Contains(step.Instructions, "didn't accept") || len(step.Fields) != 2 {
		t.Fatalf("wrong password: step = %+v", step)
	}
	step = submit(t, l, credentials)
	if len(step.Fields) != 1 || step.Fields[0].ID != codeField || !strings.Contains(step.Instructions, "authenticator") {
		t.Fatalf("code step = %+v", step)
	}
	step = submit(t, l, map[string]string{codeField: " 111111 "})
	if !strings.Contains(step.Instructions, "didn't accept") || step.Fields[0].ID != codeField {
		t.Fatalf("rejected code: step = %+v", step)
	}
	step = submit(t, l, map[string]string{codeField: "222222"})
	if len(step.Fields) != 1 || step.Fields[0].ID != "account" || len(step.Fields[0].Options) != 2 || step.Fields[0].Optional {
		t.Fatalf("account step = %+v", step)
	}
	step = submit(t, l, map[string]string{"account": "meow.two"})
	if step.Result == nil || step.Result.Me != testMe || picked["account"] != "meow.two" {
		t.Fatalf("result = %+v, picked %v", step.Result, picked)
	}
	if strings.Join(codes, ",") != "111111,222222" {
		t.Errorf("codes = %q", codes)
	}
	if strings.Contains(string(step.Result.Auth), testPassword) || !strings.Contains(string(step.Result.Auth), "fakeSession") {
		t.Errorf("auth = %s, want the cookies and not the password", step.Result.Auth)
	}
}

func TestPasswordLoginAfterCookieConsent(t *testing.T) {
	l, _ := passwordStart(t, &fakeWeb{create: func() (*instameow.InstagramWebTwoFactorChallenge, error) {
		return nil, instameow.ErrInstagramWebCookieConsentRequired
	}})
	if step := submit(t, l, credentials); step.Result == nil {
		t.Fatalf("step = %+v, want a result", step)
	}
}

func TestPasswordLoginEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"CAPTCHA":          {instameow.ErrInstagramWebCheckpointCAPTCHA, errBrowserOnly},
		"pending deletion": {instameow.ErrInstagramWebAccountPendingDeletion, nil},
		"unsupported":      {instameow.ErrInstagramWebCheckpointUnsupported, core.ErrVerificationRequired},
	} {
		t.Run(name, func(t *testing.T) {
			l, _ := passwordStart(t, &fakeWeb{create: func() (*instameow.InstagramWebTwoFactorChallenge, error) { return nil, tc.err }})
			step, err := l.Submit(t.Context(), credentials)
			if err == nil || step.Result != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Submit = %+v, %v; want %v", step, err, tc.want)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Errorf("error shows the password: %v", err)
			}
		})
	}
}

func TestPasswordLoginRetries(t *testing.T) {
	var err error
	l, clients := passwordStart(t, &fakeWeb{create: func() (*instameow.InstagramWebTwoFactorChallenge, error) { return nil, err }})
	err = instameow.ErrInstagramWebCheckpointRequestFailed
	if step := submit(t, l, credentials); len(step.Fields) != 2 || *clients != 2 {
		t.Fatalf("request failed: step = %+v, %d clients; want a new client", step, *clients)
	}
	err = instameow.ErrInstagramWebLoginRejected
	if step := submit(t, l, credentials); len(step.Fields) != 2 {
		t.Fatalf("first rejection: step = %+v", step)
	}
	if _, err := l.Submit(t.Context(), credentials); err == nil {
		t.Fatal("second rejection: no error")
	}
}

func TestPasswordLoginHidesChallengeURLs(t *testing.T) {
	for name, tc := range map[string]struct {
		url  string
		want error
	}{
		"challenge": {"https://www.instagram.com/challenge/fakeToken/?next=x", core.ErrVerificationRequired},
		"email":     {"https://www.instagram.com/accounts/update_risky_contactpoint/?token=fakeToken", nil},
	} {
		t.Run(name, func(t *testing.T) {
			l, _ := passwordStart(t, &fakeWeb{create: func() (*instameow.InstagramWebTwoFactorChallenge, error) {
				return &instameow.InstagramWebTwoFactorChallenge{ChallengeURL: tc.url}, nil
			}})
			_, err := l.Submit(t.Context(), credentials)
			if err == nil || strings.Contains(err.Error(), "fakeToken") || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Submit = %v; want %v without the URL", err, tc.want)
			}
		})
	}
}

func TestPasswordLoginAuthPlatform(t *testing.T) {
	var inputs []map[string]string
	l, _ := passwordStart(t, &fakeWeb{
		create: func() (*instameow.InstagramWebTwoFactorChallenge, error) {
			return &instameow.InstagramWebTwoFactorChallenge{AuthPlatform: true}, nil
		},
		platform: func(input map[string]string) (*bridgev2.LoginStep, error) {
			inputs = append(inputs, input)
			switch len(inputs) {
			case 1:
				return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeDisplayAndWait, Instructions: "Approve it in the app.",
					DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{Type: bridgev2.LoginDisplayTypeCode, Data: "42"}}, nil
			case 2:
				return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeUserInput, UserInputParams: &bridgev2.LoginUserInputParams{
					CanCancel: true,
					Fields:    []bridgev2.LoginInputDataField{{Type: bridgev2.LoginInputFieldType2FACode, ID: "verification_code", Name: "Verification code"}},
				}}, nil
			}
			return nil, nil
		},
	})
	step := submit(t, l, credentials)
	if len(step.Fields) != 0 || step.Result != nil || !strings.HasSuffix(step.Instructions, "Code: 42") {
		t.Fatalf("wait step = %+v", step)
	}
	step = submit(t, l, map[string]string{})
	if len(step.Fields) != 1 || !step.Fields[0].Optional || !strings.Contains(step.Instructions, "go back") {
		t.Fatalf("code step = %+v", step)
	}
	if step = submit(t, l, map[string]string{"verification_code": " "}); step.Result == nil {
		t.Fatalf("step = %+v, want a result", step)
	}
	if len(inputs) != 3 || inputs[0] != nil || inputs[2]["back"] != "true" {
		t.Errorf("inputs = %v; want nil, then {}, then back", inputs)
	}
}

func TestPasswordLoginStopsAtBrowserOnlySteps(t *testing.T) {
	for name, s := range map[string]*bridgev2.LoginStep{
		"CAPTCHA": {Type: bridgev2.LoginStepTypeUserInput, UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{{Type: bridgev2.LoginInputFieldTypeCaptchaCode, ID: "captcha"}}}},
		"QR":      {Type: bridgev2.LoginStepTypeDisplayAndWait, DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{Type: bridgev2.LoginDisplayTypeQR}},
		"cookies": {Type: bridgev2.LoginStepTypeCookies},
	} {
		t.Run(name, func(t *testing.T) {
			l, _ := passwordStart(t, &fakeWeb{manager: func(map[string]string) (*bridgev2.LoginStep, error) { return s, nil }})
			if _, err := l.Submit(t.Context(), credentials); !errors.Is(err, errBrowserOnly) {
				t.Fatalf("Submit = %v, want errBrowserOnly", err)
			}
		})
	}
}
