package build

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressWriterRecordsInterleavedBuildSteps(t *testing.T) {
	var output bytes.Buffer
	now := time.Unix(100, 0)
	writer := newProgressWriter(&output, func() time.Time { return now })

	write := func(line string, advance time.Duration) {
		now = now.Add(advance)
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	write("ComputeTargetDep", time.Second)
	write("endencyGraph\n", time.Second)
	write("SwiftCompile normal arm64 App.swift\n", 2*time.Second)
	write("CompileAssetCatalog /tmp/App.app Assets.xcassets\n", time.Second)
	write("Ld /tmp/App.app/App normal\n", 3*time.Second)
	now = now.Add(4 * time.Second)
	writer.Finish()

	text := output.String()
	for _, expected := range []string{
		"ComputeTargetDependencyGraph\n",
		progressMarker + " start 102000 1 Plan build",
		progressMarker + " done 2000 1 Plan build",
		progressMarker + " start 104000 4 Compile sources",
		progressMarker + " done 1000 4 Compile sources",
		progressMarker + " start 105000 3 Compile resources",
		progressMarker + " done 3000 3 Compile resources",
		progressMarker + " start 108000 5 Link",
		progressMarker + " done 4000 5 Link",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("progress output missing %q:\n%s", expected, text)
		}
	}
	if !strings.Contains(text, "ComputeTargetDependencyGraph\n"+progressMarker) {
		t.Fatalf("progress marker split an Xcode output line:\n%s", text)
	}
}

func TestProgressWriterSeparatesFinalMarkerFromPartialLine(t *testing.T) {
	var output bytes.Buffer
	now := time.Unix(100, 0)
	writer := newProgressWriter(&output, func() time.Time { return now })
	_, _ = writer.Write([]byte("SwiftCompile normal arm64 App.swift\ntrailing output without newline"))
	now = now.Add(time.Second)
	writer.Finish()
	if !strings.Contains(output.String(), "trailing output without newline\n"+progressMarker+" done") {
		t.Fatalf("final marker corrupted partial output: %q", output.String())
	}
}

func TestBuildStepRecognizesCommonXcodeCommands(t *testing.T) {
	tests := map[string]string{
		"Prepare packages":                        "Resolve packages",
		"CreateBuildDescription":                  "Prepare build",
		"CompileAssetCatalogVariant thinned /tmp": "Compile resources",
		"SwiftDriver App normal arm64":            "Compile sources",
		"CodeSign /tmp/App.app":                   "Sign",
		"Validate /tmp/App.app":                   "Validate",
	}
	for line, expected := range tests {
		step := buildStep(line)
		if step < 0 || buildStepDefinitions[step].name != expected {
			t.Fatalf("buildStep(%q) = %d, want %q", line, step, expected)
		}
	}
}

func TestBuildStepIgnoresEarlyPackagingCommands(t *testing.T) {
	for _, line := range []string{
		"ProcessProductPackaging /tmp/App.entitlements /tmp/App.xcent",
		"ProcessInfoPlistFile /tmp/App.app/Info.plist Info.plist",
	} {
		if step := buildStep(line); step != -1 {
			t.Fatalf("buildStep(%q) = %d, want ignored", line, step)
		}
	}
}
