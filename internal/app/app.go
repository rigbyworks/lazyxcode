package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigbyworks/lazyxcode/internal/store"
	"github.com/rigbyworks/lazyxcode/internal/tui"
	"github.com/rigbyworks/lazyxcode/internal/xcode"
)

func Run(ctx context.Context) error {
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(directory); canonicalErr == nil {
		directory = canonical
	}
	containers, err := xcode.DiscoverContainers(directory)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return xcode.ErrNoContainers
	}
	client := xcode.New(xcode.ExecRunner{})
	if err := client.CheckEnvironment(ctx); err != nil {
		return err
	}
	preferences, err := store.NewPreferences()
	if err != nil {
		return fmt.Errorf("load preferences: %w", err)
	}
	return tui.Run(ctx, directory, client, preferences, containers)
}
