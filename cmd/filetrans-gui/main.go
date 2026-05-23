package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"filetrans/backend/config"
	"filetrans/backend/webui"
)

const version = "0.1.0"

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" || arg == "version" {
			fmt.Printf("filetrans-gui %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
			return
		}
	}

	cfg := config.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := webui.New(cfg)

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start GUI server: %v\n", err)
		os.Exit(1)
	}

	addr := srv.Addr()
	fmt.Printf("filetrans GUI running at %s\n", addr)
	openBrowser(addr)

	<-ctx.Done()
	fmt.Println("\nShutting down.")
	srv.Shutdown()
}
