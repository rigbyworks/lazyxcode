package xcode

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigbyworks/lazyxcode/internal/model"
)

// Opt in explicitly: this test creates and deletes its own simulator.
func TestReleaseSmoke(t *testing.T) {
	if os.Getenv("LAZYXCODE_RELEASE_SMOKE") != "1" {
		t.Skip("set LAZYXCODE_RELEASE_SMOKE=1 to exercise installed Xcode")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Minute)
	defer cancel()
	root := t.TempDir()
	if out, err := exec.CommandContext(ctx, "python3", "../../scripts/create-smoke-project.py", root).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s: %v", out, err)
	}
	runner := ExecRunner{}
	client := New(runner)
	if err := client.CheckEnvironment(ctx); err != nil {
		t.Fatal(err)
	}
	args := []string{"simctl", "create", "lazyxcode release smoke", "com.apple.CoreSimulator.SimDeviceType.iPhone-16"}
	if runtime := os.Getenv("LAZYXCODE_SMOKE_RUNTIME"); runtime != "" {
		args = append(args, runtime)
	}
	out, err := runner.Output(ctx, "xcrun", args...)
	if err != nil {
		t.Fatalf("create simulator: %s: %v", out, err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = runner.Output(cleanup, "xcrun", "simctl", "shutdown", id)
		if out, err := runner.Output(cleanup, "xcrun", "simctl", "delete", id); err != nil {
			t.Errorf("delete smoke simulator: %s: %v", out, err)
		}
	})
	target := model.Simulator{ID: id}
	container := model.Container{Kind: model.Project, Path: filepath.Join(root, "Smoke.xcodeproj")}
	derived := filepath.Join(root, "DerivedData")
	var output bytes.Buffer
	t.Log("Building disposable app")
	if err := client.Build(ctx, &output, container, "Smoke", target, derived); err != nil {
		t.Fatalf("build: %v\n%s", err, &output)
	}
	t.Log("Build complete; locating product")
	product, err := client.Product(ctx, container, "Smoke", target, derived)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Booting simulator")
	if err := client.Boot(ctx, target); err != nil {
		t.Fatal(err)
	}
	t.Log("Simulator booted; installing app")
	if err := client.Install(ctx, target, product); err != nil {
		t.Fatal(err)
	}
	t.Log("App installed; launching")
	launch, stop := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { err := client.Launch(launch, writer, target, product); writer.Close(); done <- err }()
	ready := make(chan bool, 1)
	go func() {
		var log strings.Builder
		chunk := make([]byte, 1024)
		for {
			n, err := reader.Read(chunk)
			log.Write(chunk[:n])
			if strings.Contains(log.String(), "LAZYXCODE_SMOKE_READY") {
				ready <- true
				io.Copy(io.Discard, reader)
				return
			}
			if err != nil {
				ready <- false
				return
			}
		}
	}()
	select {
	case ok := <-ready:
		stop()
		<-done
		reader.Close()
		if !ok {
			t.Fatal("app exited without readiness output")
		}
	case <-time.After(3 * time.Minute):
		stop()
		reader.Close()
		<-done
		t.Fatal("app did not launch within three minutes")
	}
	t.Log("App launched; testing and inspecting results")
	output.Reset()
	bundle := filepath.Join(root, "Tests.xcresult")
	if err := client.Test(ctx, &output, container, "Smoke", target, derived, model.TestOptions{ResultBundlePath: bundle, Coverage: true}); err != nil {
		t.Fatalf("test: %v\n%s", err, &output)
	}
	results, err := client.TestResults(ctx, bundle)
	if err != nil || len(results) != 1 || results[0].Result != "Passed" {
		t.Fatalf("results=%+v error=%v", results, err)
	}
	if _, err := client.TestDetails(ctx, bundle, results[0].ID); err != nil {
		t.Fatal(err)
	}
	coverage, err := client.Coverage(ctx, bundle)
	if err != nil || len(coverage.Targets) == 0 {
		t.Fatalf("coverage=%+v error=%v", coverage, err)
	}
	t.Log("Build, boot, install, attached launch, tests, results, and coverage passed")
}
