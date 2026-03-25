// cmd/grpc/main.go
// Entry point for the gRPC server.
// Run alongside (or instead of) the HTTP server in cmd/main.go.
package main

import (
	"log"

	"go-omnivore-rag/config"
	"go-omnivore-rag/internal/grpcserver"
)

func main() {
	cfg := config.Load()

	port := cfg.GRPCPort
	if port == "" {
		port = "9000"
	}

	s := grpcserver.New(cfg)
	if err := s.ListenAndServe(port); err != nil {
		log.Fatalf("gRPC server: %v", err)
	}
}
