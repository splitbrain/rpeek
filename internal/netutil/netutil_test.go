package netutil

import "testing"

func TestNormalizeAddr(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"10.0.0.5", "10.0.0.5:7017", false},
		{"10.0.0.5:9000", "10.0.0.5:9000", false},
		{"example.com", "example.com:7017", false},
		{"example.com:80", "example.com:80", false},
		{"0.0.0.0", "0.0.0.0:7017", false},
		{"127.0.0.1", "127.0.0.1:7017", false},
		{"  10.0.0.5  ", "10.0.0.5:7017", false},
		{"localhost:", "localhost:7017", false},
		{"::1", "[::1]:7017", false},
		{"[::1]:7017", "[::1]:7017", false},
		{"2001:db8::1", "[2001:db8::1]:7017", false},
		{":7017", ":7017", false},
		{"", "", true},
		{"   ", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeAddr(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeAddr(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeAddr(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
