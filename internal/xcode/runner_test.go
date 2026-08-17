package xcode

import (
	"context"
	"strings"
	"testing"
)

func TestExecRunnerKeepsSuccessfulDiagnosticsOutOfStructuredOutput(t *testing.T) {
	output, err := (ExecRunner{}).Output(context.Background(), "/bin/sh", "-c", "printf '{\"ok\":true}'; printf 'diagnostic' >&2")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"ok":true}` {
		t.Fatalf("output = %q", output)
	}
}

func TestExecRunnerIncludesDiagnosticsOnFailure(t *testing.T) {
	output, err := (ExecRunner{}).Output(context.Background(), "/bin/sh", "-c", "printf 'failure detail' >&2; exit 2")
	if err == nil {
		t.Fatal("expected command failure")
	}
	if !strings.Contains(string(output), "failure detail") {
		t.Fatalf("output = %q", output)
	}
}
