package tools

import "testing"

func TestRegistryConsistent(t *testing.T) {
	names := Names()
	if len(names) != len(All) {
		t.Fatalf("Names() has %d entries, All has %d", len(names), len(All))
	}
	seen := map[string]bool{}
	for i, name := range names {
		if name != All[i].Name() {
			t.Errorf("Names()[%d] = %q, All[%d].Name() = %q", i, name, i, All[i].Name())
		}
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
