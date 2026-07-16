package tools

// All is the ordered set of every diagnostic tool. Adding a tool is a new file plus one
// entry here. An explicit ordered slice, rather than init-time self-registration, keeps
// the full tool set greppable in one place and gives a deterministic order for help
// output and the serve banner.
var All = []Tool{
	helpTool{}, hostname{}, versionTool{}, list{}, read{}, grep{}, tail{}, stat{}, ps{}, disk{}, journal{}, serveTool{},
}

// byName indexes All by tool name, built once at package initialization.
var byName = func() map[string]Tool {
	m := make(map[string]Tool, len(All))
	for _, t := range All {
		m[t.Name()] = t
	}
	return m
}()

// Lookup returns the tool registered under name and whether one exists.
func Lookup(name string) (Tool, bool) {
	t, ok := byName[name]
	return t, ok
}

// Names returns the tool names in All order.
func Names() []string {
	names := make([]string, len(All))
	for i, t := range All {
		names[i] = t.Name()
	}
	return names
}

// RemoteNames returns, in All order, the names of the tools the server can run — those
// implementing RemoteTool. It drives the serve banner, which must not advertise
// client-only tools like help.
func RemoteNames() []string {
	var names []string
	for _, t := range All {
		if _, ok := t.(RemoteTool); ok {
			names = append(names, t.Name())
		}
	}
	return names
}
