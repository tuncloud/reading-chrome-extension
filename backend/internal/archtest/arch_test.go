// Package archtest enforces the clean-architecture dependency rule on non-test code.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const module = "reading-tracker/backend"

// packageImports maps "internal/<dir>" to the imports of its non-test .go files.
func packageImports(t *testing.T) map[string][]string {
	t.Helper()
	root := ".." // backend/internal
	pkgs := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "internal/" + filepath.ToSlash(rel)
		if _, ok := pkgs[pkg]; !ok {
			pkgs[pkg] = nil
		}
		for _, imp := range f.Imports {
			pkgs[pkg] = append(pkgs[pkg], strings.Trim(imp.Path.Value, `"`))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

func under(p, prefix string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }

func isStdlib(imp string) bool { return !strings.Contains(strings.SplitN(imp, "/", 2)[0], ".") }

// adapterRoot returns "internal/adapter/<name>" for any package inside that adapter.
func adapterRoot(pkg string) string {
	parts := strings.SplitN(pkg, "/", 4)
	if len(parts) < 3 {
		return pkg
	}
	return strings.Join(parts[:3], "/")
}

// violation explains why pkg may not import imp, or returns "".
func violation(pkg, imp string) string {
	target, internal := strings.CutPrefix(imp, module+"/")
	switch {
	case internal && under(target, "internal/wire") && !under(pkg, "internal/wire"):
		return "only cmd/* may import wire"
	case under(pkg, "internal/domain"):
		if internal && !under(target, "internal/domain") {
			return "domain may import only domain"
		}
		if !internal && !isStdlib(imp) {
			return "domain may import only the standard library"
		}
	case under(pkg, "internal/usecase"):
		if internal && !(under(target, "internal/domain") || under(target, "internal/usecase") ||
			under(target, "internal/platform/crypto") || under(target, "internal/platform/textutil")) {
			return "use cases may import only domain, usecase and platform/{crypto,textutil}"
		}
		if !internal && !isStdlib(imp) && imp != "github.com/oklog/ulid/v2" {
			return "use cases may not import frameworks or SDKs"
		}
	case under(pkg, "internal/adapter"):
		if internal && under(target, "internal/adapter") && adapterRoot(target) != adapterRoot(pkg) {
			return "an adapter may not import another adapter"
		}
		if internal && under(target, "internal/wire") {
			return "adapters may not import wire"
		}
	case under(pkg, "internal/platform"):
		if internal && !under(target, "internal/platform") {
			return "platform may import only platform"
		}
	}
	return ""
}

func TestDependencyRule(t *testing.T) {
	for pkg, imps := range packageImports(t) {
		for _, imp := range imps {
			if v := violation(pkg, imp); v != "" {
				t.Errorf("%s imports %s: %s", pkg, imp, v)
			}
		}
	}
}

func TestViolationRules(t *testing.T) {
	cases := []struct {
		pkg, imp string
		bad      bool
	}{
		{"internal/domain", "time", false},
		{"internal/domain", "github.com/aws/aws-sdk-go-v2/aws", true},
		{"internal/domain", module + "/internal/platform/crypto", true},
		{"internal/usecase/auth", module + "/internal/domain", false},
		{"internal/usecase/auth", module + "/internal/usecase/port", false},
		{"internal/usecase/auth", module + "/internal/platform/crypto", false},
		{"internal/usecase/auth", module + "/internal/platform/config", true},
		{"internal/usecase/auth", module + "/internal/adapter/dynamo", true},
		{"internal/usecase/auth", "github.com/labstack/echo/v4", true},
		{"internal/usecase/auth", "github.com/oklog/ulid/v2", false},
		{"internal/adapter/httpapi", module + "/internal/usecase/auth", false},
		{"internal/adapter/httpapi", module + "/internal/adapter/dynamo", true},
		{"internal/adapter/notion/notiontest", module + "/internal/adapter/notion", false},
		{"internal/adapter/queue", module + "/internal/wire", true},
		{"internal/usecase/setup", module + "/internal/wire", true},
		{"internal/domain", module + "/internal/wire", true},
		{"internal/platform/config", module + "/internal/platform/crypto", false},
		{"internal/platform/config", module + "/internal/domain", true},
		{"internal/wire", module + "/internal/adapter/dynamo", false},
	}
	for _, c := range cases {
		if got := violation(c.pkg, c.imp) != ""; got != c.bad {
			t.Errorf("violation(%s, %s) = %v, want %v", c.pkg, c.imp, got, c.bad)
		}
	}
}

// Every production package lives in a layer directory.
func TestOnlyLayerDirectories(t *testing.T) {
	layers := []string{"internal/domain", "internal/usecase", "internal/adapter", "internal/platform", "internal/wire"}
	for pkg := range packageImports(t) {
		ok := false
		for _, l := range layers {
			if under(pkg, l) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s is outside the layer directories %v", pkg, layers)
		}
	}
}
