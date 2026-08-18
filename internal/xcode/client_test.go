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

	"github.com/mwahlig/lazy-xcode/internal/model"
)

type fakeRunner struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []string
}

func (r *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	var output []byte
	for fragment, candidate := range r.outputs {
		if strings.Contains(call, fragment) {
			output = candidate
			break
		}
	}
	for fragment, err := range r.errors {
		if strings.Contains(call, fragment) {
			return output, err
		}
	}
	return output, nil
}

func (r *fakeRunner) Stream(_ context.Context, writer io.Writer, name string, args ...string) error {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	_, _ = io.WriteString(writer, "build output\n")
	return nil
}

func TestDiscoverContainersPrefersWorkspaces(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Zeta.xcodeproj", "Alpha.xcworkspace", "notes.txt"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	containers, err := DiscoverContainers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 || containers[0].Kind != model.Workspace || containers[0].Name != "Alpha.xcworkspace" {
		t.Fatalf("unexpected containers: %#v", containers)
	}
}

func TestListSchemesUsesContainerJSON(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"-list -json": []byte(`{"workspace":{"schemes":["Zeta","Alpha"]}}`)}}
	client := New(runner)
	schemes, err := client.ListSchemes(context.Background(), model.Container{Kind: model.Workspace, Path: "/tmp/App.xcworkspace"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schemes, []string{"Alpha", "Zeta"}) {
		t.Fatalf("schemes = %#v", schemes)
	}
}

func TestListTestTargetsUsesSchemeReferencesAndProductTypes(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "App.xcodeproj")
	if err := os.MkdirAll(filepath.Join(project, "xcshareddata", "xcschemes"), 0o755); err != nil {
		t.Fatal(err)
	}
	scheme := `<Scheme><TestAction><TestPlans><TestPlanReference reference="container:App.xctestplan"/></TestPlans><Testables>
<TestableReference><BuildableReference BlueprintIdentifier="UNIT-ID" BlueprintName="AppTests"/></TestableReference>
<TestableReference><BuildableReference BlueprintIdentifier="UI-ID" BlueprintName="AppUITests"/></TestableReference>
<TestableReference skipped="YES"><BuildableReference BlueprintIdentifier="SKIP-ID" BlueprintName="SkippedTests"/></TestableReference>
</Testables></TestAction></Scheme>`
	if err := os.WriteFile(filepath.Join(project, "xcshareddata", "xcschemes", "App.xcscheme"), []byte(scheme), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := `{"testTargets":[{"target":{"identifier":"PACKAGE-ID","name":"PackageTests"}},{"enabled":false,"target":{"identifier":"OFF-ID","name":"DisabledTests"}}]}`
	if err := os.WriteFile(filepath.Join(root, "App.xctestplan"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	pbx := `UNIT-ID /* AppTests */ = {
			isa = PBXNativeTarget;
			name = AppTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
		UI-ID /* VisualChecks */ = {
			isa = PBXNativeTarget;
			name = VisualChecks;
			productType = "com.apple.product-type.bundle.ui-testing";
		};`
	if err := os.WriteFile(filepath.Join(project, "project.pbxproj"), []byte(pbx), 0o600); err != nil {
		t.Fatal(err)
	}
	targets, err := New(&fakeRunner{}).ListTestTargets(model.Container{Kind: model.Project, Path: project}, "App")
	if err != nil {
		t.Fatal(err)
	}
	want := []model.TestTarget{{Name: "AppTests", Kind: model.TestUnit}, {Name: "PackageTests", Kind: model.TestUnit}, {Name: "AppUITests", Kind: model.TestUI}}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
}

func TestDestinationsAreLimitedToEligibleAvailableSimulators(t *testing.T) {
	showDestinations := []byte(`
	Available destinations for the "App" scheme:
		{ platform:iOS Simulator, arch:arm64, id:AAAA-BBBB, OS:26.0, name:iPhone 17 Pro }
		{ platform:iOS, id:dvtdevice-DVTiPhonePlaceholder-iphoneos:placeholder, name:Any iOS Device }
		{ platform:iOS Simulator, id:CCCC-DDDD, OS:25.4, name:iPad Pro 13-inch }
	Ineligible destinations for the "App" scheme:
		{ platform:iOS Simulator, id:EEEE-FFFF, OS:24.0, name:iPhone SE, error:iOS 24 is lower than deployment target }
`)
	simctl := []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
		{"udid":"AAAA-BBBB","name":"iPhone 17 Pro","state":"Booted","isAvailable":true,"deviceTypeIdentifier":"phone"},
		{"udid":"CCCC-DDDD","name":"iPad Pro 13-inch","state":"Shutdown","isAvailable":false,"deviceTypeIdentifier":"tablet"}
	]}}`)
	available, err := parseSimctlDevices(simctl)
	if err != nil {
		t.Fatal(err)
	}
	got := parseDestinations(showDestinations, available)
	if len(got) != 1 || got[0].ID != "AAAA-BBBB" || got[0].State != "Booted" {
		t.Fatalf("destinations = %#v", got)
	}
}

func TestProductRequiresOneRunnableApplication(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"-showBuildSettings -json": []byte(`[
		{"buildSettings":{"WRAPPER_EXTENSION":"app","SKIP_INSTALL":"NO","PRODUCT_BUNDLE_IDENTIFIER":"com.example.app","TARGET_BUILD_DIR":"/tmp/build","FULL_PRODUCT_NAME":"App.app"}},
		{"buildSettings":{"WRAPPER_EXTENSION":"appex","PRODUCT_BUNDLE_IDENTIFIER":"com.example.extension"}}
	]`)}}
	client := New(runner)
	product, err := client.Product(context.Background(), model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, "App", model.Simulator{ID: "AAAA"}, "/tmp/derived")
	if err != nil {
		t.Fatal(err)
	}
	if product.AppPath != "/tmp/build/App.app" || product.BundleID != "com.example.app" {
		t.Fatalf("product = %#v", product)
	}
}

func TestBuildCommandUsesDestinationAndManagedDerivedData(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	var output strings.Builder
	err := client.Build(context.Background(), &output, model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, "App", model.Simulator{ID: "AAAA"}, "/tmp/cache")
	if err != nil {
		t.Fatal(err)
	}
	call := runner.calls[0]
	for _, required := range []string{"-project /tmp/App.xcodeproj", "-scheme App", "-destination id=AAAA", "-derivedDataPath /tmp/cache", "-showBuildTimingSummary", "build"} {
		if !strings.Contains(call, required) {
			t.Fatalf("command %q missing %q", call, required)
		}
	}
}

func TestTestCommandFiltersSelectedTargets(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	var output strings.Builder
	err := client.Test(context.Background(), &output, model.Container{Kind: model.Workspace, Path: "/tmp/App.xcworkspace"}, "App", model.Simulator{ID: "AAAA"}, "/tmp/cache", []string{"AppTests", "ModelTests"})
	if err != nil {
		t.Fatal(err)
	}
	call := runner.calls[0]
	for _, required := range []string{
		"-workspace /tmp/App.xcworkspace", "-scheme App", "-destination id=AAAA", "-derivedDataPath /tmp/cache",
		"-only-testing:AppTests", "-only-testing:ModelTests", "-showBuildTimingSummary", "test",
	} {
		if !strings.Contains(call, required) {
			t.Fatalf("command %q missing %q", call, required)
		}
	}
}

func TestBootOpensSimulatorWhenDeviceIsAlreadyBooted(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{"simctl boot AAAA": []byte("Unable to boot device in current state: Booted")},
		errors:  map[string]error{"simctl boot AAAA": errors.New("exit status 149")},
	}
	client := New(runner)
	if err := client.Boot(context.Background(), model.Simulator{ID: "AAAA", State: "Booted"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"xcrun simctl boot AAAA",
		"open -a Simulator",
		"xcrun simctl bootstatus AAAA -b",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestBootOpensSimulatorBeforeWaitingForNewDevice(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.Boot(context.Background(), model.Simulator{ID: "AAAA", State: "Shutdown"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"xcrun simctl boot AAAA",
		"open -a Simulator",
		"xcrun simctl bootstatus AAAA -b",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}
