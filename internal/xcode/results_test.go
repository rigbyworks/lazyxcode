package xcode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rigbyworks/lazyxcode/internal/model"
)

func TestResultIdentifiersDisambiguateTargetsAndKeepParameterizedTestsTogether(t *testing.T) {
	payload := `{"testNodes":[{"nodeType":"Test Plan","name":"Plan","children":[
 {"nodeType":"Unit test bundle","name":"One","children":[{"nodeType":"Test Suite","name":"Checks","children":[{"nodeType":"Test Case","name":"testA()","nodeIdentifier":"Checks/testA()","nodeIdentifierURL":"test://host/Plan/One/Checks/testA","result":"Failed"}]}]},
 {"nodeType":"Unit test bundle","name":"Two","children":[{"nodeType":"Test Case","name":"testA()","nodeIdentifier":"Checks/testA()","nodeIdentifierURL":"test://host/Plan/Two/Checks/testA","result":"Passed"},{"nodeType":"Test Case","name":"value(x:)","nodeIdentifier":"value(x:)","nodeIdentifierURL":"test://host/Plan/Two/value(x:)","result":"Failed","children":[{"nodeType":"Arguments","name":"1","result":"Failed"}]}]}]}]}`
	runner := &fakeRunner{outputs: map[string][]byte{"test-results tests": []byte(payload)}}
	tests, err := New(runner).TestResults(context.Background(), "/tmp/Results.xcresult")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, test := range tests {
		ids = append(ids, test.Identifier)
	}
	if !reflect.DeepEqual(ids, []string{"One/Checks/testA()", "Two/Checks/testA()", "Two/value(x:)"}) {
		t.Fatalf("filters = %v", ids)
	}
	if tests[0].ID == tests[1].ID {
		t.Fatal("result lookups lost target identity")
	}
}

func TestDetailsPreserveFailureSourceAndAllRuns(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"test-details": []byte(`{"testName":"testA","testResult":"Failed","duration":"1s","testRuns":[{"name":"Phone","result":"Failed","children":[{"name":"Expected true","nodeType":"Failure Message","sourceLocation":{"filePath":"/src/A.swift","lineNumber":42}}]},{"name":"Tablet","result":"Passed"}]}`)}}
	details, err := New(runner).TestDetails(context.Background(), "bundle", "test://host/id")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Expected true", "/src/A.swift:42", "Phone", "Tablet", "Failed"} {
		if !strings.Contains(details, want) {
			t.Fatalf("missing %q in %s", want, details)
		}
	}
	if !strings.Contains(runner.calls[0], "--test-id test://host/id") {
		t.Fatal(runner.calls)
	}
}

func TestActivitiesHandleRepeatedRunsAndAttachments(t *testing.T) {
	for _, runs := range []string{
		`{"device":{"deviceName":"Phone"},"activities":[{"title":"Tap button","isAssociatedWithFailure":true,"attachments":[{"name":"Screenshot.png"}]}]}`,
		`[{"device":{"deviceName":"Phone"},"activities":[{"title":"Tap button","isAssociatedWithFailure":true,"attachments":[{"name":"Screenshot.png"}]}]}]`,
	} {
		client := New(&fakeRunner{outputs: map[string][]byte{"activities": []byte(`{"testRuns":` + runs + `}`)}})
		text, err := client.TestActivities(context.Background(), "bundle", "id")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Phone", "Tap button [failure]", "Screenshot.png"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %s in %s", want, text)
			}
		}
	}
}

func TestReadEnumerationIgnoresDisabledTestsAndDeduplicatesConfigurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tests.json")
	data := `{"errors":[],"values":[{"enabledTests":[{"identifier":"AppTests/B/test()"},{"identifier":"AppTests/A/test()"}],"disabledTests":[{"identifier":"AppTests/C/test()"}]},{"enabledTests":[{"identifier":"AppTests/A/test()"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	tests, err := ReadEnumeratedTests(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) != 2 || tests[0].Identifier != "AppTests/A/test()" {
		t.Fatalf("tests = %+v", tests)
	}
	if err := os.WriteFile(path, []byte(`{"errors":[{"message":"missing host"}],"values":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnumeratedTests(path); err == nil {
		t.Fatal("discovery error ignored")
	}
}

func TestTestCommandRetainsBundleAndSelectsExactTest(t *testing.T) {
	runner := &fakeRunner{}
	err := New(runner).Test(context.Background(), io.Discard, model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, "App", model.Simulator{ID: "MAC"}, "/tmp/dd", model.TestOptions{Targets: []string{"AppTests/Checks/testA()"}, ResultBundlePath: "/tmp/run/Results.xcresult", Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-resultBundlePath /tmp/run/Results.xcresult", "-enableCodeCoverage YES", "-only-testing:AppTests/Checks/testA()"} {
		if !strings.Contains(runner.calls[0], want) {
			t.Fatalf("missing %s in %s", want, runner.calls[0])
		}
	}
}

func TestDiscoveryBuildsAndEnumeratesWithoutExecutingTests(t *testing.T) {
	runner := &fakeRunner{}
	err := New(runner).Test(context.Background(), io.Discard, model.Container{}, "App", model.Simulator{ID: "MAC"}, "dd", model.TestOptions{EnumerationPath: "/tmp/tests.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-enumerate-tests", "-test-enumeration-format json", "-test-enumeration-style flat", "-test-enumeration-output-path /tmp/tests.json"} {
		if !strings.Contains(runner.calls[0], want) {
			t.Fatal(runner.calls)
		}
	}
	if strings.Contains(runner.calls[0], "-resultBundlePath") {
		t.Fatal("discovery shouldn't claim to produce test results")
	}
}

func TestCoverageAndComparisonUseBeforeAfterOrdering(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"view --report": []byte(`{"lineCoverage":0.5,"coveredLines":1,"executableLines":2,"targets":[{"name":"App","files":[{"path":"/src/A.swift","functions":[{"name":"f()","lineNumber":3}]}]}]}`), "diff": []byte(`{"lineCoverageDelta":{"lineCoverageDelta":0.1},"targetDeltas":[{"name":"App","lineCoverageDelta":{"lineCoverageDelta":0.1}}]}`)}}
	c := New(runner)
	report, err := c.Coverage(context.Background(), "after.xcresult")
	if err != nil {
		t.Fatal(err)
	}
	if report.LineCoverage != 0.5 || report.Targets[0].Files[0].Functions[0].Name != "f()" {
		t.Fatalf("report = %+v", report)
	}
	diff, err := c.CompareCoverage(context.Background(), "before.xcresult", "after.xcresult")
	if err != nil || !strings.Contains(diff, "+10.00 pp") || runner.calls[1] != "xcrun xccov diff --json before.xcresult after.xcresult" {
		t.Fatalf("diff = %s, %v, %v", diff, err, runner.calls)
	}
}

func TestResultToolFailuresRemainVisible(t *testing.T) {
	c := New(&fakeRunner{errors: map[string]error{"xcresulttool": errors.New("unsupported"), "xccov": errors.New("missing coverage")}})
	if _, err := c.TestResults(context.Background(), "old"); err == nil {
		t.Fatal("missing results error")
	}
	if _, err := c.Coverage(context.Background(), "no-coverage"); err == nil {
		t.Fatal("missing coverage error")
	}
}

func TestCoverageDifferenceIncludesAddedRemovedAndUnchangedItems(t *testing.T) {
	text, err := formatCoverageDifference([]byte(`{"lineCoverageDelta":{"lineCoverageDelta":0,"coveredLinesDelta":2,"executableLinesDelta":4},"addedTargets":[{"name":"NewTarget"}],"removedTargets":["OldTarget"],"targetDeltas":[{"name":"App","addedFiles":[{"path":"/src/New.swift"}],"removedFiles":[{"documentLocation":"/src/Old.swift"}],"fileDeltas":[{"documentLocation":"/src/A.swift","addedFunctions":[{"name":"added()"}],"removedFunctions":["removed()"]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+0.00 pp", "+2 covered", "NewTarget", "OldTarget", "/src/New.swift", "/src/Old.swift", "added()", "removed()"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}
