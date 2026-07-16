package tools

import (
	"context"
	"strings"
	"testing"
)

func TestHelpTool(t *testing.T) {
	// help is client-only: a LocalTool but not a RemoteTool.
	var lt LocalTool = helpTool{}
	if _, ok := Tool(helpTool{}).(RemoteTool); ok {
		t.Error("help must not implement RemoteTool")
	}

	// No topic lists the registry, including help itself and other tools.
	general, err := lt.Local(context.Background(), Env{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"help", "version", "grep"} {
		if !strings.Contains(general.Output, name) {
			t.Errorf("general help missing tool %q:\n%s", name, general.Output)
		}
	}

	// A tool topic renders that tool's usage.
	grep, err := lt.Local(context.Background(), Env{}, mustRaw(t, helpArgs{Topic: "grep"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grep.Output, "grep <path>") {
		t.Errorf("grep help missing its usage synopsis:\n%s", grep.Output)
	}

	// An unknown topic is an error.
	if _, err := lt.Local(context.Background(), Env{}, mustRaw(t, helpArgs{Topic: "bogus"})); err == nil {
		t.Error("help for an unknown topic should error")
	}
}
