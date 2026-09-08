package xcode

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mwahlig/lazy-xcode/internal/model"
)

type Client struct {
	runner Runner
}

func New(runner Runner) *Client { return &Client{runner: runner} }

func DiscoverContainers(dir string) ([]model.Container, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read launch directory: %w", err)
	}
	var containers []model.Container
	for _, entry := range entries {
		name := entry.Name()
		kind := model.ContainerKind("")
		switch {
		case strings.HasSuffix(name, ".xcworkspace"):
			kind = model.Workspace
		case strings.HasSuffix(name, ".xcodeproj"):
			kind = model.Project
		default:
			continue
		}
		containers = append(containers, model.Container{Kind: kind, Name: name, Path: filepath.Join(abs, name)})
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Kind != containers[j].Kind {
			return containers[i].Kind == model.Workspace
		}
		return strings.ToLower(containers[i].Name) < strings.ToLower(containers[j].Name)
	})
	return containers, nil
}

func containerArgs(container model.Container) []string {
	if container.Kind == model.Workspace {
		return []string{"-workspace", container.Path}
	}
	return []string{"-project", container.Path}
}

func (c *Client) ListSchemes(ctx context.Context, container model.Container) ([]string, error) {
	args := append(containerArgs(container), "-list", "-json")
	out, err := c.runner.Output(ctx, "xcodebuild", args...)
	if err != nil {
		return nil, commandError("list schemes", out, err)
	}
	var payload struct {
		Workspace struct {
			Schemes []string `json:"schemes"`
		} `json:"workspace"`
		Project struct {
			Schemes []string `json:"schemes"`
		} `json:"project"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("parse scheme list: %w", err)
	}
	schemes := payload.Workspace.Schemes
	if container.Kind == model.Project {
		schemes = payload.Project.Schemes
	}
	sort.Strings(schemes)
	return schemes, nil
}

func (c *Client) ListTestTargets(container model.Container, scheme string) ([]model.TestTarget, error) {
	schemePath, err := findScheme(container, scheme)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(schemePath)
	if err != nil {
		return nil, fmt.Errorf("read scheme tests: %w", err)
	}
	var document struct {
		TestAction struct {
			Plans []struct {
				Reference string `xml:"reference,attr"`
			} `xml:"TestPlans>TestPlanReference"`
			Testables []struct {
				Skipped   string `xml:"skipped,attr"`
				Reference struct {
					ID   string `xml:"BlueprintIdentifier,attr"`
					Name string `xml:"BlueprintName,attr"`
				} `xml:"BuildableReference"`
			} `xml:"Testables>TestableReference"`
		} `xml:"TestAction"`
	}
	if err := xml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse scheme tests: %w", err)
	}
	root := filepath.Dir(container.Path)
	projectFiles := findProjectFiles(root)
	projects := make([][]byte, 0, len(projectFiles))
	for _, path := range projectFiles {
		if content, readErr := os.ReadFile(path); readErr == nil {
			projects = append(projects, content)
		}
	}
	references := make([]struct{ ID, Name string }, 0, len(document.TestAction.Testables))
	for _, testable := range document.TestAction.Testables {
		if strings.EqualFold(testable.Skipped, "YES") {
			continue
		}
		references = append(references, struct{ ID, Name string }{testable.Reference.ID, testable.Reference.Name})
	}
	for _, plan := range document.TestAction.Plans {
		path := strings.TrimPrefix(plan.Reference, "container:")
		if path == plan.Reference {
			continue
		}
		planData, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			continue
		}
		var payload struct {
			Targets []struct {
				Enabled *bool `json:"enabled"`
				Target  struct {
					ID   string `json:"identifier"`
					Name string `json:"name"`
				} `json:"target"`
			} `json:"testTargets"`
		}
		if json.Unmarshal(planData, &payload) == nil {
			for _, target := range payload.Targets {
				if target.Enabled != nil && !*target.Enabled {
					continue
				}
				references = append(references, struct{ ID, Name string }{target.Target.ID, target.Target.Name})
			}
		}
	}
	seen := map[string]bool{}
	var targets []model.TestTarget
	for _, reference := range references {
		name := reference.Name
		if name == "" || seen[name] {
			continue
		}
		kind := testTargetKind(reference.ID, name, projects)
		seen[name] = true
		targets = append(targets, model.TestTarget{Name: name, Kind: kind})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind == model.TestUnit
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, nil
}

func findProjectFiles(root string) []string {
	var result []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".build", "DerivedData", "SourcePackages":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "project.pbxproj" && strings.HasSuffix(filepath.Dir(path), ".xcodeproj") {
			result = append(result, path)
		}
		return nil
	})
	return result
}

func findScheme(container model.Container, scheme string) (string, error) {
	direct := filepath.Join(container.Path, "xcshareddata", "xcschemes", scheme+".xcscheme")
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	}
	userSchemes, _ := filepath.Glob(filepath.Join(container.Path, "xcuserdata", "*", "xcschemes", scheme+".xcscheme"))
	if len(userSchemes) > 0 {
		return userSchemes[0], nil
	}
	root := filepath.Dir(container.Path)
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == "DerivedData" || entry.Name() == ".git") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == scheme+".xcscheme" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find scheme tests: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("test metadata unavailable for scheme %s", scheme)
	}
	return found, nil
}

func testTargetKind(identifier, name string, projects [][]byte) model.TestKind {
	if identifier != "" {
		for _, project := range projects {
			remaining := project
			marker := []byte(identifier + " /*")
			for {
				start := bytes.Index(remaining, marker)
				if start < 0 {
					break
				}
				remaining = remaining[start:]
				end := bytes.Index(remaining, []byte("\n\t\t};"))
				if end < 0 {
					break
				}
				block := remaining[:end]
				if bytes.Contains(block, []byte("isa = PBXNativeTarget;")) {
					if bytes.Contains(block, []byte("com.apple.product-type.bundle.ui-testing")) {
						return model.TestUI
					}
					if bytes.Contains(block, []byte("com.apple.product-type.bundle.unit-test")) {
						return model.TestUnit
					}
				}
				remaining = remaining[len(marker):]
			}
		}
	}
	if strings.Contains(strings.ToLower(name), "uitest") {
		return model.TestUI
	}
	return model.TestUnit
}

func commandError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, message)
}

type simctlDevice struct {
	UDID                 string `json:"udid"`
	Name                 string `json:"name"`
	State                string `json:"state"`
	IsAvailable          bool   `json:"isAvailable"`
	DeviceTypeIdentifier string `json:"deviceTypeIdentifier"`
}

func (c *Client) ListSimulators(ctx context.Context, container model.Container, scheme string) ([]model.Simulator, error) {
	destinationArgs := append(containerArgs(container), "-scheme", scheme, "-showdestinations")
	destinationOutput, err := c.runner.Output(ctx, "xcodebuild", destinationArgs...)
	if err != nil {
		return nil, commandError("list destinations", destinationOutput, err)
	}
	simctlOutput, err := c.runner.Output(ctx, "xcrun", "simctl", "list", "devices", "available", "--json")
	if err != nil {
		return nil, commandError("list simulators", simctlOutput, err)
	}
	available, err := parseSimctlDevices(simctlOutput)
	if err != nil {
		return nil, err
	}
	return parseDestinations(destinationOutput, available), nil
}

func parseSimctlDevices(data []byte) (map[string]simctlDevice, error) {
	var payload struct {
		Devices map[string][]simctlDevice `json:"devices"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse simulator list: %w", err)
	}
	result := map[string]simctlDevice{}
	for _, devices := range payload.Devices {
		for _, device := range devices {
			if device.IsAvailable {
				result[device.UDID] = device
			}
		}
	}
	return result, nil
}

func parseDestinations(data []byte, available map[string]simctlDevice) []model.Simulator {
	var result []model.Simulator
	inAvailable := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Available destinations") || strings.HasPrefix(line, "Destinations compatible") {
			inAvailable = true
			continue
		}
		if strings.HasPrefix(line, "Ineligible destinations") || strings.HasPrefix(line, "Unavailable destinations") || strings.HasPrefix(line, "Destinations incompatible") {
			inAvailable = false
			continue
		}
		if !inAvailable || !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		fields := parseDestinationFields(strings.TrimSuffix(strings.TrimPrefix(line, "{"), "}"))
		id, platform := fields["id"], fields["platform"]
		if id == "" || strings.Contains(id, ":placeholder") {
			continue
		}
		if strings.Contains(platform, "Simulator") {
			device, ok := available[id]
			if !ok {
				continue
			}
			result = append(result, model.Simulator{
				ID: id, Name: fields["name"], OS: fields["OS"], Platform: platform,
				State: device.State, DeviceType: device.DeviceTypeIdentifier,
			})
			continue
		}
		if platform == "macOS" {
			result = append(result, model.Simulator{
				ID: id, Name: fields["name"], OS: fields["OS"], Platform: platform, State: "Local",
			})
			continue
		}
		if platform != "iOS" && platform != "tvOS" && platform != "watchOS" && platform != "visionOS" {
			continue
		}
		result = append(result, model.Simulator{
			ID: id, Name: fields["name"], OS: fields["OS"], Platform: platform,
			State: "Connected", Physical: true,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Platform != result[j].Platform {
			return result[i].Platform < result[j].Platform
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].OS > result[j].OS
	})
	return result
}

func parseDestinationFields(line string) map[string]string {
	fields := map[string]string{}
	for _, item := range strings.Split(line, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) == 2 {
			fields[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return fields
}

func (c *Client) Build(ctx context.Context, writer io.Writer, container model.Container, scheme string, simulator model.Simulator, derivedData string) error {
	args := append(containerArgs(container), "-scheme", scheme, "-destination", "id="+simulator.ID, "-derivedDataPath", derivedData, "-showBuildTimingSummary", "build")
	return c.runner.Stream(ctx, writer, "xcodebuild", args...)
}

func (c *Client) Test(ctx context.Context, writer io.Writer, container model.Container, scheme string, simulator model.Simulator, derivedData string, options model.TestOptions) error {
	args := append(containerArgs(container), "-scheme", scheme, "-destination", "id="+simulator.ID, "-derivedDataPath", derivedData, "-showBuildTimingSummary")
	for _, target := range options.Targets {
		args = append(args, "-only-testing:"+target)
	}
	if options.EnumerationPath != "" {
		args = append(args, "-enumerate-tests", "-test-enumeration-style", "flat", "-test-enumeration-format", "json", "-test-enumeration-output-path", options.EnumerationPath)
	} else {
		if options.ResultBundlePath != "" {
			args = append(args, "-resultBundlePath", options.ResultBundlePath)
		}
		coverage := "NO"
		if options.Coverage {
			coverage = "YES"
		}
		args = append(args, "-enableCodeCoverage", coverage)
	}
	args = append(args, "test")
	return c.runner.Stream(ctx, writer, "xcodebuild", args...)
}

func (c *Client) Product(ctx context.Context, container model.Container, scheme string, simulator model.Simulator, derivedData string) (model.Product, error) {
	args := append(containerArgs(container), "-scheme", scheme, "-destination", "id="+simulator.ID, "-derivedDataPath", derivedData, "-showBuildSettings", "-json")
	out, err := c.runner.Output(ctx, "xcodebuild", args...)
	if err != nil {
		return model.Product{}, commandError("resolve app product", out, err)
	}
	var payload []struct {
		BuildSettings map[string]string `json:"buildSettings"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return model.Product{}, fmt.Errorf("parse build settings: %w", err)
	}
	var products []model.Product
	for _, target := range payload {
		settings := target.BuildSettings
		if settings["WRAPPER_EXTENSION"] != "app" || settings["SKIP_INSTALL"] == "YES" || settings["PRODUCT_BUNDLE_IDENTIFIER"] == "" || settings["FULL_PRODUCT_NAME"] == "" {
			continue
		}
		products = append(products, model.Product{
			AppPath:  filepath.Join(settings["TARGET_BUILD_DIR"], settings["FULL_PRODUCT_NAME"]),
			BundleID: settings["PRODUCT_BUNDLE_IDENTIFIER"],
		})
	}
	if len(products) != 1 {
		return model.Product{}, fmt.Errorf("expected one runnable app product, found %d", len(products))
	}
	return products[0], nil
}

func (c *Client) Boot(ctx context.Context, simulator model.Simulator) error {
	if !simulator.IsSimulator() {
		return nil
	}
	out, err := c.runner.Output(ctx, "xcrun", "simctl", "boot", simulator.ID)
	if err != nil && !strings.Contains(string(out), "current state: Booted") {
		return commandError("boot simulator", out, err)
	}
	if out, err := c.runner.Output(ctx, "open", "-a", "Simulator"); err != nil {
		return commandError("open Simulator", out, err)
	}
	out, err = c.runner.Output(ctx, "xcrun", "simctl", "bootstatus", simulator.ID, "-b")
	if err != nil {
		return commandError("wait for simulator", out, err)
	}
	return nil
}

func (c *Client) Install(ctx context.Context, simulator model.Simulator, product model.Product) error {
	if simulator.IsMac() {
		return nil
	}
	if simulator.Physical {
		out, err := c.runner.Output(ctx, "xcrun", "devicectl", "device", "install", "app", "--device", simulator.ID, product.AppPath)
		if err != nil {
			return commandError("install app on device", out, err)
		}
		return nil
	}
	out, err := c.runner.Output(ctx, "xcrun", "simctl", "install", simulator.ID, product.AppPath)
	if err != nil {
		return commandError("install app", out, err)
	}
	return nil
}

func (c *Client) Launch(ctx context.Context, writer io.Writer, simulator model.Simulator, product model.Product) error {
	if simulator.IsMac() {
		out, err := c.runner.Output(ctx, "open", product.AppPath)
		if err != nil {
			return commandError("launch app on Mac", out, err)
		}
		return nil
	}
	if simulator.Physical {
		if err := c.runner.Stream(ctx, writer, "xcrun", "devicectl", "device", "process", "launch", "--console", "--terminate-existing", "--device", simulator.ID, product.BundleID); err != nil {
			return fmt.Errorf("launch app on device: %w", err)
		}
		return nil
	}
	if err := c.runner.Stream(ctx, writer, "xcrun", "simctl", "launch", "--console-pty", "--terminate-running-process", simulator.ID, product.BundleID); err != nil {
		return fmt.Errorf("launch app: %w", err)
	}
	return nil
}

var ErrNoContainers = errors.New("no .xcworkspace or .xcodeproj found in the current directory")
