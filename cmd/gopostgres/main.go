package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gopostgres/internal/catalog"
	"gopostgres/internal/server"
)

func main() {
	config := server.Config{
		Port:           5432,
		MaxConnections: 100000,
	}

	cat := catalog.NewCatalog()
	s := server.NewServer(config, cat)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("received signal, shutting down", "signal", sig)
		s.Shutdown()
	}()

	if err := s.Start(); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
