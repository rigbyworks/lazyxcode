package xcode

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
	Stream(context.Context, io.Writer, string, ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := command(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		output := append(stdout.Bytes(), stderr.Bytes()...)
		return output, err
	}
	return stdout.Bytes(), nil
}

func (ExecRunner) Stream(ctx context.Context, writer io.Writer, name string, args ...string) error {
	cmd := command(ctx, name, args...)
	cmd.Stdout = writer
	cmd.Stderr = writer
	return cmd.Run()
}

func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "NSUnbufferedIO=YES", "SIMCTL_CHILD_NSUnbufferedIO=YES", "DEVICECTL_CHILD_NSUnbufferedIO=YES")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
	return cmd
}
