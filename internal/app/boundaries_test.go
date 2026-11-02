package app

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/deepashearaion/manage-crm/internal/"

// shared packages any module may import.
var shared = map[string]bool{"platform": true, "testutil": true}

// Modules talk to each other only through interfaces or events wired in
// internal/app, never by importing one another's packages.
func TestModulesDoNotImportEachOther(t *testing.T) {
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		module := e.Name()
		if !e.IsDir() || shared[module] || module == "app" || module == "apitest" {
			continue
		}
		err := filepath.WalkDir(filepath.Join("..", module), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if !strings.HasPrefix(p, modulePath) {
					continue
				}
				target := strings.SplitN(strings.TrimPrefix(p, modulePath), "/", 2)[0]
				if target != module && !shared[target] {
					t.Errorf("%s imports %s: module %q must not depend on module %q", path, p, module, target)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
