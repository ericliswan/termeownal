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

package termeownal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// adapterDir is the only directory allowed to import upstream (AGENTS.md rule 1).
const adapterDir = "internal/backend/instagram"

var upstreamPrefixes = []string{"go.mau.fi/mautrix-meta", "maunium.net/go/mautrix"}

// boundaryViolations returns "file: import" for every upstream import
// under root that is outside adapterDir.
func boundaryViolations(t *testing.T, root string) []string {
	t.Helper()
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == adapterDir || d.Name() == "testdata" || (rel != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, prefix := range upstreamPrefixes {
				if p == prefix || strings.HasPrefix(p, prefix+"/") {
					bad = append(bad, rel+": "+p)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return bad
}

func TestImportBoundary(t *testing.T) {
	for _, v := range boundaryViolations(t, ".") {
		t.Errorf("upstream import outside %s: %s", adapterDir, v)
	}
}

func TestImportBoundaryDetectsViolations(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/ui/bad.go", "package ui\nimport _ \"go.mau.fi/mautrix-meta/pkg/instameow\"\n")
	write("internal/app/bad.go", "package app\nimport _ \"maunium.net/go/mautrix/bridgev2\"\n")
	write("internal/app/ok.go", "package app\nimport _ \"go.mau.fi/util/random\"\n")
	write(adapterDir+"/client.go", "package instagram\nimport _ \"go.mau.fi/mautrix-meta/pkg/instameow\"\n")

	got := boundaryViolations(t, root)
	want := []string{
		"internal/app/bad.go: maunium.net/go/mautrix/bridgev2",
		"internal/ui/bad.go: go.mau.fi/mautrix-meta/pkg/instameow",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("violations:\n got %q\nwant %q", got, want)
	}
}
