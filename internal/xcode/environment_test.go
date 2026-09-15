package xcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the supported host platform")
	}
	developer := t.TempDir()
	bin := filepath.Join(developer, "usr", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "xcodebuild"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, macOS, xcode, path, want string
		failure                        bool
	}{
		{"minimum", "15.0", "Xcode 16.3\nBuild version test", developer, "", false},
		{"current", "27.0", "Xcode 27.0\nBuild version test", developer, "", false},
		{"old macOS", "14.7", "", developer, "macOS 15", false},
		{"old Xcode", "15.0", "Xcode 16.2", developer, "Xcode 16.3", false},
		{"malformed version", "15.0", "unexpected output", developer, "Xcode 16.3", false},
		{"relative selection", "15.0", "", "relative", "invalid Xcode", false},
		{"command line tools only", "15.0", "", t.TempDir(), "full Xcode", false},
		{"selection failure", "15.0", "", developer, "locate Xcode", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{outputs: map[string][]byte{
				"sw_vers": []byte(tc.macOS), "xcode-select": []byte(tc.path), "xcodebuild": []byte(tc.xcode),
			}}
			if tc.failure {
				r.errors = map[string]error{"xcode-select": errors.New("failed")}
			}
			err := New(r).CheckEnvironment(context.Background())
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
