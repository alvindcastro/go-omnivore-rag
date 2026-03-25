// internal/grpcserver/server.go
// gRPC server wiring. Run `buf generate` first to produce gen/go/omnivore/v1/*.
package grpcserver

import (
	"fmt"
	"log"
	"net"

	"go-omnivore-rag/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	// Uncomment after running `buf generate`:
	// omnivorev1 "go-omnivore-rag/gen/go/omnivore/v1"
)

// Server wraps a gRPC server with all service handlers registered.
type Server struct {
	cfg    *config.Config
	server *grpc.Server
}

// New creates and configures the gRPC server.
func New(cfg *config.Config) *Server {
	s := grpc.NewServer()

	// reflection lets grpcurl / gRPC UI discover services without a .proto file
	reflection.Register(s)

	// TODO: uncomment after `buf generate` and implement handler types below
	// omnivorev1.RegisterSystemServiceServer(s, &systemHandler{cfg: cfg})
	// omnivorev1.RegisterBannerServiceServer(s, &bannerHandler{cfg: cfg})
	// omnivorev1.RegisterSOPServiceServer(s, &sopHandler{cfg: cfg})

	return &Server{cfg: cfg, server: s}
}

// ListenAndServe starts the gRPC listener on the given port (e.g. "9000").
func (s *Server) ListenAndServe(port string) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	log.Printf("gRPC server listening on :%s", port)
	return s.server.Serve(lis)
}

// GracefulStop drains in-flight RPCs then stops.
func (s *Server) GracefulStop() {
	s.server.GracefulStop()
}
