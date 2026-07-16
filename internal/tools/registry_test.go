package tools

import (
	"sort"
	"testing"
)

func TestRegistryConsistent(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no tools registered")
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("Names() is not sorted alphabetically: %v", names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate tool name %q", name)
		}
		seen[name] = true
		tool, ok := Lookup(name)
		if !ok {
			t.Errorf("Lookup(%q) not found", name)
			continue
		}
		if tool.Name() != name {
			t.Errorf("Lookup(%q).Name() = %q", name, tool.Name())
		}
	}

	if _, ok := Lookup("definitely-not-a-tool"); ok {
		t.Error("Lookup of an unknown name should report not found")
	}
}
