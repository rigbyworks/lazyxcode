package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mwahlig/lazy-xcode/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "lazy-xcode:", err)
		os.Exit(1)
	}
}
