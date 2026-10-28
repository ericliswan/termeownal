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
package secrets

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// lockName is the lock file in the data directory. It's never deleted:
// removing a file another process has locked would let a third lock a new one.
const lockName = "meow.lock"

// ErrLocked means another meow process is using the data directory.
var ErrLocked = errors.New("secrets: another meow is running")

// Lock takes the lock on dir, so one meow process at a time uses the session
// file in it. Closing the result, or the process ending, releases it.
func Lock(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(dir, lockName))
}
