package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mwahlig/lazy-xcode/internal/store"
	"github.com/mwahlig/lazy-xcode/internal/tui"
	"github.com/mwahlig/lazy-xcode/internal/xcode"
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
	preferences, err := store.NewPreferences()
	if err != nil {
		return fmt.Errorf("load preferences: %w", err)
	}
	return tui.Run(ctx, directory, xcode.New(xcode.ExecRunner{}), preferences, containers)
}
