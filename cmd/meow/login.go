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

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"termeownal/internal/core"
	"termeownal/internal/secrets"
	"termeownal/internal/store"
)

// loginHints say what to do about the errors a login can end with
// (AGENTS.md §6.6).
var loginHints = []struct {
	err  error
	hint string
}{
	{core.ErrSessionExpired, "Instagram didn't accept the cookies. Copy them again from a browser window that's still logged in."},
	{core.ErrRateLimited, "Instagram is rate-limiting this account; wait a while before retrying."},
	{core.ErrVerificationRequired, "Instagram wants to verify this login. Complete the check in the app or website, then run `meow login`."},
	{core.ErrConsentRequired, "Accept Instagram's pending terms, then retry."},
	{core.ErrAccountSuspended, "This account is suspended."},
}

// login runs a's login on c, then records the account in the cache at
// cachePath and saves the session in f. It saves nothing if the login fails.
func login(ctx context.Context, a core.Authenticator, c *console, f *secrets.File, cachePath string) error {
	step, err := a.Start(ctx)
	for err == nil && step.Result == nil {
		if step.Instructions != "" {
			c.printf("%s\n\n", step.Instructions)
		}
		values := map[string]string{}
		for _, field := range step.Fields {
			if values[field.ID], err = c.field(field); err != nil {
				return err
			}
		}
		step, err = a.Submit(ctx, values)
	}
	if err != nil {
		return err
	}
	me := step.Result.Me
	// The cache goes first: meow shows it before it syncs, so it must not
	// still hold another account's messages when the new session is saved.
	st, err := store.Open(ctx, cachePath)
	if err != nil {
		return err
	}
	err = st.SetMe(ctx, me)
	if closeErr := st.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := f.Create(core.Session{Auth: step.Result.Auth}); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	c.printf("Logged in as %s (@%s).\n", me.Name, me.Username)
	return nil
}

// loginHint says what to do after a failed login, or where to look.
func loginHint(err error, logPath string) string {
	for _, h := range loginHints {
		if errors.Is(err, h.err) {
			return h.hint
		}
	}
	if errors.Is(err, errCancelled) {
		return ""
	}
	return "The log is at " + logPath + "."
}

// logout deletes the saved session and its key and, with purge, the cache.
func logout(f *secrets.File, cachePath string, purge bool) error {
	err := f.Delete()
	if purge {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if e := os.Remove(cachePath + suffix); e != nil && !errors.Is(e, fs.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("delete cache: %w", e))
			}
		}
	}
	return err
}
