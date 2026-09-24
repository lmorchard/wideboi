package main

import "testing"

func TestProfilePath(t *testing.T) {
	cases := []struct {
		prefix, component, kind string
		pid                     int
		want                    string
	}{
		{"/tmp/wb", "server", "cpu", 12345, "/tmp/wb.server.cpu.12345.pprof"},
		{"/tmp/wb", "server", "mem", 12345, "/tmp/wb.server.mem.12345.pprof"},
		{"/tmp/wb", "client", "cpu", 7, "/tmp/wb.client.cpu.7.pprof"},
		{"wbm", "client", "mem", 42, "wbm.client.mem.42.pprof"},
	}
	for _, c := range cases {
		if got := profilePath(c.prefix, c.component, c.kind, c.pid); got != c.want {
			t.Errorf("profilePath(%q, %q, %q, %d) = %q, want %q", c.prefix, c.component, c.kind, c.pid, got, c.want)
		}
	}
}
