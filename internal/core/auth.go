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

import "context"

// Authenticator runs an interactive login, one step at a time. Start returns
// the first step; Submit answers the current step and returns the next one,
// until a step has Result set.
type Authenticator interface {
	Start(ctx context.Context) (LoginStep, error)
	// Submit takes the current step's values keyed by LoginField.ID.
	Submit(ctx context.Context, values map[string]string) (LoginStep, error)
}

// LoginStep is one prompt in a login.
type LoginStep struct {
	// Instructions are shown before the fields.
	Instructions string
	// Fields are the values to ask for, in order. Empty on the final step.
	Fields []LoginField
	// Result is set on the final step.
	Result *LoginResult
}

// LoginField is one value to ask the user for.
type LoginField struct {
	// ID is the key for this value in Submit.
	ID    string
	Label string
	// Secret values must be read with echo off.
	Secret   bool
	Optional bool
}

// LoginResult is what a successful login produces.
type LoginResult struct {
	Me   User
	Auth AuthData
}
