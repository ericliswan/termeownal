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
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"maunium.net/go/mautrix/bridgev2"

	"termeownal/internal/core"
)

// The password login's own fields. Upstream's steps bring their own IDs.
const (
	usernameField = "username"
	passwordField = "password"
	codeField     = "verification_code"
)

const passwordInstructions = `Log in with your Instagram username and password.
meow doesn't save the password, only the session Instagram returns, as a
browser would. Nothing is shown as you type the password.`

// errBrowserOnly ends a password login at a step only a browser can do.
var errBrowserOnly = errors.New("log in: Instagram wants a check meow can't show in a terminal, such as a CAPTCHA; run `meow login` again and choose cookies")

// webClient is the part of upstream's client that a password login uses.
type webClient interface {
	CreateInstagramWebSession(ctx context.Context, identifier, password string) (*instameow.InstagramWebTwoFactorChallenge, error)
	ContinueInstagramWebSessionAfterCookieConsent(ctx context.Context, identifier, password string) (*instameow.InstagramWebTwoFactorChallenge, error)
	CompleteInstagramWebSessionTwoFactor(ctx context.Context, code string) error
	DoInstagramWebAuthPlatformSteps(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error)
	DoInstagramWebAccountManagerSteps(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error)
	GetCookies() *cookies.Cookies
}

var _ webClient = (*instameow.Client)(nil)

// stage is where a password login is.
type stage int

const (
	askCredentials stage = iota
	askCode              // a code from a text, an email or an authenticator app
	authPlatform         // upstream's verification steps: pick a method, approve in the app
	pickAccount          // upstream's account picker, when one login has several accounts
)

// passwordLogin logs in through Instagram's website, as upstream's web
// password login does (igconnector/login_native.go). It never keeps the
// password: Submit passes it on and forgets it.
//
// shortcut: a challenge or checkpoint after the password ends the login with
// ErrVerificationRequired instead of trying upstream's CAA login; add it if
// people hit these often.
type passwordLogin struct {
	newClient func() webClient
	check     func(context.Context, *cookies.Cookies) (core.User, error)

	client    webClient
	stage     stage
	challenge *instameow.InstagramWebTwoFactorChallenge // at askCode
	// back is set when the current step from upstream can go back: an
	// answer left empty goes back.
	back bool
	// rejected counts the logins Instagram turned down.
	rejected int
}

// restart starts again with a new client and asks for the username and
// password, with note, if set, in place of the usual instructions.
func (p *passwordLogin) restart(note string) core.LoginStep {
	p.client, p.stage, p.challenge, p.back = p.newClient(), askCredentials, nil, false
	return credentialsStep(note)
}

func (p *passwordLogin) submit(ctx context.Context, values map[string]string) (core.LoginStep, error) {
	switch p.stage {
	case askCode:
		return p.submitCode(ctx, strings.TrimSpace(values[codeField]))
	case authPlatform:
		if p.back && allEmpty(values) {
			values = map[string]string{"back": "true"}
		}
		step, err := p.client.DoInstagramWebAuthPlatformSteps(ctx, values)
		return p.afterAuthPlatform(ctx, step, err)
	case pickAccount:
		return p.pickAccount(ctx, values)
	}
	return p.submitCredentials(ctx, strings.TrimSpace(values[usernameField]), values[passwordField])
}

func (p *passwordLogin) submitCredentials(ctx context.Context, username, password string) (core.LoginStep, error) {
	if username == "" || password == "" {
		return credentialsStep("Enter both your username and your password."), nil
	}
	challenge, err := p.client.CreateInstagramWebSession(ctx, username, password)
	if errors.Is(err, instameow.ErrInstagramWebCookieConsentRequired) {
		challenge, err = p.client.ContinueInstagramWebSessionAfterCookieConsent(ctx, username, password)
	}
	switch {
	case err == nil:
	case errors.Is(err, instameow.ErrInstagramWebCheckpointRequestFailed):
		return p.restart("The request didn't go through. Try again."), nil
	case errors.Is(err, instameow.ErrInstagramWebCheckpointCAPTCHA):
		return core.LoginStep{}, errBrowserOnly
	case errors.Is(err, instameow.ErrInstagramWebLoginRejected):
		return p.rejectedLogin()
	case errors.Is(err, instameow.ErrInstagramWebCredentialsRejected):
		return credentialsStep("Instagram didn't accept that username or password. Try again."), nil
	case errors.Is(err, instameow.ErrInstagramWebRegistrationOffered):
		return credentialsStep("Instagram offered to make a new account with those details. Use the username or email of your account."), nil
	case errors.Is(err, instameow.ErrInstagramWebAccountPendingDeletion):
		return core.LoginStep{}, errors.New("log in: Instagram says this account is due to be deleted; review that in the Instagram app")
	default:
		return core.LoginStep{}, fmt.Errorf("log in: %w", coreError(err))
	}
	switch {
	case challenge == nil:
		return p.pickAccount(ctx, map[string]string{})
	case challenge.ChallengeURL != "":
		return core.LoginStep{}, challengeError(challenge.ChallengeURL)
	case challenge.AuthPlatform:
		p.stage = authPlatform
		step, err := p.client.DoInstagramWebAuthPlatformSteps(ctx, nil)
		return p.afterAuthPlatform(ctx, step, err)
	}
	p.stage, p.challenge = askCode, challenge
	return codeStep(challenge, ""), nil
}

// rejectedLogin asks for the username and password again the first time
// Instagram turns the login down, and gives up the second time.
func (p *passwordLogin) rejectedLogin() (core.LoginStep, error) {
	p.rejected++
	if p.rejected > 1 {
		return core.LoginStep{}, errors.New("log in: Instagram keeps turning down this login; run `meow login` again and choose cookies")
	}
	p.stage, p.challenge, p.back = askCredentials, nil, false
	return credentialsStep("Instagram couldn't sign you in. Check your account in the Instagram app, then try again."), nil
}

func (p *passwordLogin) submitCode(ctx context.Context, code string) (core.LoginStep, error) {
	if code == "" {
		return codeStep(p.challenge, ""), nil
	}
	err := p.client.CompleteInstagramWebSessionTwoFactor(ctx, code)
	switch {
	case err == nil:
		return p.pickAccount(ctx, map[string]string{})
	case errors.Is(err, instameow.ErrInstagramWebCheckpointRequestFailed):
		return codeStep(p.challenge, "The request didn't go through. Enter a new code."), nil
	case errors.Is(err, instameow.ErrInstagramWebTwoFactorCodeResent):
		return codeStep(p.challenge, "Instagram didn't accept that code and sent a new one by text. Enter it when it arrives."), nil
	case errors.Is(err, instameow.ErrInstagramWebTwoFactorCodeRejected):
		return codeStep(p.challenge, "Instagram didn't accept that code. Enter the newest one."), nil
	}
	return core.LoginStep{}, fmt.Errorf("check code: %w", coreError(err))
}

func (p *passwordLogin) afterAuthPlatform(ctx context.Context, step *bridgev2.LoginStep, err error) (core.LoginStep, error) {
	if u, ok := instameow.InstagramWebChallengeURL(err); ok {
		return core.LoginStep{}, challengeError(u)
	}
	switch {
	case errors.Is(err, instameow.ErrInstagramWebLoginRejected):
		return p.rejectedLogin()
	case errors.Is(err, instameow.ErrInstagramWebCheckpointCAPTCHA):
		return core.LoginStep{}, errBrowserOnly
	case errors.Is(err, instameow.ErrInstagramAuthPlatformApprovalExpired):
		return core.LoginStep{}, errors.New("verify login: the approval expired; run `meow login` again")
	case errors.Is(err, httpclient.ErrRateLimited), errors.Is(err, httpclient.ErrAccountSuspended),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, instameow.ErrInstagramWebCheckpointRequestFailed):
		return core.LoginStep{}, fmt.Errorf("verify login: %w", coreError(err))
	case err != nil:
		// Upstream treats any other error here as a check it can't do.
		return core.LoginStep{}, fmt.Errorf("verify login: %w: %w", core.ErrVerificationRequired, err)
	case step != nil:
		return p.fromUpstream(step)
	}
	return p.pickAccount(ctx, nil)
}

// pickAccount runs upstream's account picker, which finishes the login
// straight away when there's only one account.
func (p *passwordLogin) pickAccount(ctx context.Context, values map[string]string) (core.LoginStep, error) {
	p.stage = pickAccount
	step, err := p.client.DoInstagramWebAccountManagerSteps(ctx, values)
	if u, ok := instameow.InstagramWebRedirectChallengeURL(err); ok {
		return core.LoginStep{}, challengeError(u)
	}
	switch {
	case errors.Is(err, instameow.ErrInstagramAccountManagerTwoFactorRequired):
		return p.restart("That account uses two-factor authentication. Log in with its own username and password."), nil
	case errors.Is(err, instameow.ErrInstagramAccountManagerSwitchFailed):
		return p.restart("Instagram didn't let meow switch to that account. Log in with its own username and password."), nil
	case err != nil:
		return core.LoginStep{}, fmt.Errorf("choose account: %w", coreError(err))
	case step != nil:
		return p.fromUpstream(step)
	}
	c := p.client.GetCookies()
	p.client = nil
	return result(ctx, c, p.check)
}

// fromUpstream converts one of upstream's steps. It ends the login on a
// step a terminal can't show, such as a CAPTCHA or a web page.
func (p *passwordLogin) fromUpstream(s *bridgev2.LoginStep) (core.LoginStep, error) {
	step := core.LoginStep{Instructions: s.Instructions}
	p.back = false
	switch {
	case s.Type == bridgev2.LoginStepTypeUserInput && s.UserInputParams != nil:
		p.back = s.UserInputParams.CanCancel
		for _, f := range s.UserInputParams.Fields {
			if f.Type == bridgev2.LoginInputFieldTypeCaptchaCode {
				return core.LoginStep{}, errBrowserOnly
			}
			step.Fields = append(step.Fields, core.LoginField{
				ID:       f.ID,
				Label:    f.Name,
				Secret:   f.Type == bridgev2.LoginInputFieldTypePassword,
				Optional: f.Optional || p.back,
				Options:  f.Options,
			})
		}
		if p.back {
			step.Instructions += "\nPress Enter without typing anything to go back."
		}
	case s.Type == bridgev2.LoginStepTypeDisplayAndWait && s.DisplayAndWaitParams != nil:
		// A step with no fields waits: the next Submit returns once it's over.
		// shortcut: CanCancel ("try another way") is ignored during a wait; offer it if approvals get stuck.
		switch d := s.DisplayAndWaitParams; d.Type {
		case bridgev2.LoginDisplayTypeCode:
			step.Instructions += "\n\nCode: " + d.Data
		case bridgev2.LoginDisplayTypeNothing:
		default:
			return core.LoginStep{}, errBrowserOnly
		}
	default:
		return core.LoginStep{}, errBrowserOnly
	}
	return step, nil
}

// challengeError is the error for a check Instagram wants done on its
// website. It never includes the URL, which carries tokens.
func challengeError(rawURL string) error {
	if u, err := url.Parse(rawURL); err == nil && strings.Trim(u.Path, "/") == "accounts/update_risky_contactpoint" {
		return errors.New("log in: Instagram wants you to add a new email address to your account; do that in the Instagram app or on instagram.com, then run `meow login` again")
	}
	return fmt.Errorf("log in: %w: Instagram wants a check on its website", core.ErrVerificationRequired)
}

func credentialsStep(note string) core.LoginStep {
	return core.LoginStep{
		Instructions: cmp.Or(note, passwordInstructions),
		Fields: []core.LoginField{
			{ID: usernameField, Label: "Email or username"},
			{ID: passwordField, Label: "Password", Secret: true},
		},
	}
}

// codeStep asks for the code challenge asks for, with note, if set, in place
// of the usual instructions.
func codeStep(challenge *instameow.InstagramWebTwoFactorChallenge, note string) core.LoginStep {
	if note == "" {
		switch {
		case challenge.TOTP:
			note = "Enter the code from your authenticator app."
		case challenge.Email:
			note = "Enter the code Instagram sent to your email."
		case challenge.SMS || challenge.WhatsApp:
			note = "Enter the code Instagram sent you."
		default:
			note = "Enter your Instagram verification code."
		}
	}
	return core.LoginStep{Instructions: note, Fields: []core.LoginField{{ID: codeField, Label: "Verification code"}}}
}

func allEmpty(values map[string]string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
