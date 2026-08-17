package xcode

import (
	"context"
	"encoding/json"
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
		if strings.HasPrefix(line, "Available destinations") {
			inAvailable = true
			continue
		}
		if strings.HasPrefix(line, "Ineligible destinations") || strings.HasPrefix(line, "Unavailable destinations") {
			inAvailable = false
			continue
		}
		if !inAvailable || !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		fields := parseDestinationFields(strings.TrimSuffix(strings.TrimPrefix(line, "{"), "}"))
		id, platform := fields["id"], fields["platform"]
		device, ok := available[id]
		if !ok || !strings.Contains(platform, "Simulator") {
			continue
		}
		result = append(result, model.Simulator{
			ID: id, Name: fields["name"], OS: fields["OS"], Platform: platform,
			State: device.State, DeviceType: device.DeviceTypeIdentifier,
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
	args := append(containerArgs(container), "-scheme", scheme, "-destination", "id="+simulator.ID, "-derivedDataPath", derivedData, "build")
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
	out, err := c.runner.Output(ctx, "xcrun", "simctl", "install", simulator.ID, product.AppPath)
	if err != nil {
		return commandError("install app", out, err)
	}
	return nil
}

func (c *Client) Launch(ctx context.Context, simulator model.Simulator, product model.Product) error {
	out, err := c.runner.Output(ctx, "xcrun", "simctl", "launch", simulator.ID, product.BundleID)
	if err != nil {
		return commandError("launch app", out, err)
	}
	return nil
}

var ErrNoContainers = errors.New("no .xcworkspace or .xcodeproj found in the current directory")
