package tako_test

// This test locks the SDK's exported surface. It renders every exported
// symbol — including exported methods on exported types — to a stable
// textual form and compares it to testdata/sdk_lock.golden. Any addition,
// removal, or re-shaping of the API fails here first; regenerate the
// snapshot deliberately with:
//
//	go test . -run TestSDKLock -update

import (
	"flag"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

var update = flag.Bool("update", false, "rewrite testdata/sdk_lock.golden")

func TestSDKLock(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes}
	pkgs, err := packages.Load(cfg, "github.com/gsandoval/tako")
	if err != nil {
		t.Fatalf("loading the tako package: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 || len(pkgs) != 1 {
		t.Fatal("the tako package did not load cleanly")
	}
	pkg := pkgs[0].Types

	qual := types.RelativeTo(pkg)
	var lines []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		if tn, ok := obj.(*types.TypeName); ok && !tn.IsAlias() {
			lines = append(lines, renderTypeDecl(tn, qual))
			for m := range methodsOf(tn.Type()) {
				lines = append(lines, "    "+types.ObjectString(m, qual))
			}
		} else {
			lines = append(lines, types.ObjectString(obj, qual))
		}
	}
	slices.Sort(lines)
	got := strings.Join(lines, "\n") + "\n"

	golden := filepath.Join("testdata", "sdk_lock.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", golden, err)
	}
	if got != string(want) {
		t.Fatalf("exported SDK surface differs from %s.\n"+
			"API changes are their own task; if this one is deliberate, rerun with -update and call it out in the task log.\n%s",
			golden, diff(string(want), got))
	}
}

// renderTypeDecl renders a type declaration with unexported struct fields
// pruned, so the snapshot locks only the user-visible surface and internal
// field refactors don't trip it.
func renderTypeDecl(tn *types.TypeName, qual types.Qualifier) string {
	named := tn.Type().(*types.Named)
	var b strings.Builder
	b.WriteString("type ")
	b.WriteString(tn.Name())
	if tps := named.TypeParams(); tps != nil {
		b.WriteString("[")
		for i := range tps.Len() {
			if i > 0 {
				b.WriteString(", ")
			}
			tp := tps.At(i)
			fmt.Fprintf(&b, "%s %s", tp.Obj().Name(), types.TypeString(tp.Constraint(), qual))
		}
		b.WriteString("]")
	}
	b.WriteString(" ")
	under := named.Underlying()
	if st, ok := under.(*types.Struct); ok {
		var fields []*types.Var
		for i := range st.NumFields() {
			if f := st.Field(i); f.Exported() {
				fields = append(fields, f)
			}
		}
		under = types.NewStruct(fields, nil)
	}
	b.WriteString(types.TypeString(under, qual))
	return b.String()
}

// methodsOf yields the exported methods in the method sets of T and *T,
// deduplicated, in deterministic order.
func methodsOf(typ types.Type) func(yield func(*types.Func) bool) {
	seen := map[string]*types.Func{}
	for _, t := range []types.Type{typ, types.NewPointer(typ)} {
		ms := types.NewMethodSet(t)
		for sel := range ms.Methods() {
			if m := sel.Obj().(*types.Func); m.Exported() {
				seen[m.Name()] = m
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return func(yield func(*types.Func) bool) {
		for _, name := range names {
			if !yield(seen[name]) {
				return
			}
		}
	}
}

func diff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var b strings.Builder
	for _, l := range gotLines {
		if !slices.Contains(wantLines, l) && l != "" {
			fmt.Fprintf(&b, "+ %s\n", l)
		}
	}
	for _, l := range wantLines {
		if !slices.Contains(gotLines, l) && l != "" {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	return b.String()
}
