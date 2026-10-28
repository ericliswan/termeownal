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

package core

import "errors"

// Errors a Backend or Authenticator wraps so callers can branch with
// errors.Is and show the right message (ROADMAP §6.4).
var (
	// ErrSessionExpired means the user must log in again.
	ErrSessionExpired = errors.New("session expired")
	// ErrRateLimited means wait before trying again.
	ErrRateLimited = errors.New("rate limited")
	// ErrVerificationRequired means the service wants the user to confirm
	// the login in its app or website.
	ErrVerificationRequired = errors.New("login verification required")
	// ErrConsentRequired means the user must accept pending terms first.
	ErrConsentRequired = errors.New("terms must be accepted")
	// ErrAccountSuspended means the account can't be used.
	ErrAccountSuspended = errors.New("account suspended")
)
