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

import (
	"fmt"
	"strings"
	"testing"
)

func TestAuthDataIsRedacted(t *testing.T) {
	const secret = "sessionid=not-a-real-cookie"
	s := Session{Auth: AuthData(secret), SeqID: 42}
	r := LoginResult{Auth: AuthData(secret)}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
		for _, arg := range []any{s.Auth, s, &s, r, &r} {
			got := fmt.Sprintf(verb, arg)
			if strings.Contains(got, secret) || strings.Contains(got, fmt.Sprintf("%x", secret)) ||
				strings.Contains(got, fmt.Sprintf("%X", secret)) {
				t.Errorf("Sprintf(%q, %T) leaks the secret: %s", verb, arg, got)
			}
		}
	}
}
