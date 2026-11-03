package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fenyuluoshang/codex-personuse-proxy-tool/internal/gateway"
)

func main() {
	configPath := flag.String("config", "gateway.json", "path to gateway JSON configuration")
	flag.Parse()
	c, err := gateway.LoadConfig(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err.Error())
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	g, err := gateway.New(c, logger)
	if err != nil {
		slog.Error("cannot create gateway", "error", err.Error())
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := g.Shutdown(shutdownCtx); err != nil {
			logger.Error("gateway shutdown failed", "error", err.Error())
		}
	}()
	logger.Info("gateway listening", "listen", c.Listen, "upstream", c.Upstream)
	if err := g.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
		logger.Error("gateway stopped", "error", err.Error())
		os.Exit(1)
	}
}
