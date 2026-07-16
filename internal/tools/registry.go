package tools

import "sort"

// registered holds every diagnostic tool, kept sorted alphabetically by name. Each tool
// file registers itself from an init function, so adding a tool is just dropping in its
// file — there is no central list to update.
var registered []Tool

// byName indexes the registered tools by name for Lookup.
var byName = map[string]Tool{}

// register adds a tool to the package registry, inserting it so registered stays sorted by
// name. Tool files call it from an init function. It panics on an empty or duplicate name:
// both are programming errors that surface the first time the binary runs.
func register(t Tool) {
	name := t.Name()
	if name == "" {
		panic("tools: tool with empty name")
	}
	if _, dup := byName[name]; dup {
		panic("tools: duplicate tool name " + name)
	}
	byName[name] = t
	i := sort.Search(len(registered), func(i int) bool { return registered[i].Name() >= name })
	registered = append(registered, nil)
	copy(registered[i+1:], registered[i:])
	registered[i] = t
}

// Lookup returns the tool registered under name and whether one exists.
func Lookup(name string) (Tool, bool) {
	t, ok := byName[name]
	return t, ok
}

// Names returns the registered tool names.
func Names() []string {
	names := make([]string, len(registered))
	for i, t := range registered {
		names[i] = t.Name()
	}
	return names
}

// RemoteNames returns the names of the tools the server can run — those implementing
// RemoteTool. It drives the serve banner, which must not advertise client-only tools like
// help.
func RemoteNames() []string {
	var names []string
	for _, t := range registered {
		if _, ok := t.(RemoteTool); ok {
			names = append(names, t.Name())
		}
	}
	return names
}
