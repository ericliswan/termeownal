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
	"context"
	"errors"

	"termeownal/internal/core"
)

// authPrefix marks AuthData made by Authenticator. It never contains the
// password.
const authPrefix = "fake-session:"

// WrongPassword is the one password Authenticator rejects, for testing the
// failure path.
const WrongPassword = "wrong"

// Authenticator is a one-step fake login: any username and any password
// except WrongPassword.
type Authenticator struct{}

var _ core.Authenticator = Authenticator{}

// Start asks for a username and password.
func (Authenticator) Start(context.Context) (core.LoginStep, error) {
	return core.LoginStep{
		Instructions: "Fake login: any username and password work (except \"" + WrongPassword + "\").",
		Fields: []core.LoginField{
			{ID: "username", Label: "Username"},
			{ID: "password", Label: "Password", Secret: true},
		},
	}, nil
}

// Submit checks the values and finishes the login.
func (Authenticator) Submit(_ context.Context, values map[string]string) (core.LoginStep, error) {
	user, pass := values["username"], values["password"]
	switch {
	case user == "" || pass == "":
		return core.LoginStep{}, errors.New("fake: username and password are required")
	case pass == WrongPassword:
		return core.LoginStep{}, errors.New("fake: wrong password")
	}
	return core.LoginStep{Result: &core.LoginResult{Me: me, Auth: core.AuthData(authPrefix + user)}}, nil
}
