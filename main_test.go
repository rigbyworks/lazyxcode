package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommandLine(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		code   int
		want   string
		starts bool
	}{
		{[]string{"--help"}, 0, "Usage: lazyxcode", false},
		{[]string{"--version"}, 0, "lazyxcode dev", false},
		{[]string{"--unknown"}, 2, "unknown argument", false},
		{[]string{"project"}, 2, "unknown argument", false},
		{nil, 1, "no project", true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			started := false
			start := func(context.Context) error { started = true; return errors.New("no project") }
			code := run(context.Background(), tc.args, &stdout, &stderr, start)
			if code != tc.code || started != tc.starts || !strings.Contains(stdout.String()+stderr.String(), tc.want) {
				t.Fatalf("code=%d started=%v stdout=%q stderr=%q", code, started, &stdout, &stderr)
			}
		})
	}
}
