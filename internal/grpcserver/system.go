// internal/grpcserver/system.go
// Implements SystemServiceServer (health, index stats, create index).
// Uncomment the body after running `buf generate`.
package grpcserver

// import (
// 	"context"
//
// 	"go-omnivore-rag/config"
// 	"go-omnivore-rag/internal/azure"
// 	omnivorev1 "go-omnivore-rag/gen/go/omnivore/v1"
// )
//
// type systemHandler struct {
// 	omnivorev1.UnimplementedSystemServiceServer
// 	cfg    *config.Config
// 	search *azure.SearchClient
// }
//
// func (h *systemHandler) Health(_ context.Context, _ *omnivorev1.HealthRequest) (*omnivorev1.HealthResponse, error) {
// 	return &omnivorev1.HealthResponse{
// 		Status:         "healthy",
// 		IndexName:      h.cfg.AzureSearchIndexName,
// 		ChatModel:      h.cfg.AzureOpenAIChatDeployment,
// 		EmbeddingModel: h.cfg.AzureOpenAIEmbeddingDeployment,
// 	}, nil
// }
//
// func (h *systemHandler) IndexStats(_ context.Context, _ *omnivorev1.IndexStatsRequest) (*omnivorev1.IndexStatsResponse, error) {
// 	count, err := h.search.GetDocumentCount()
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &omnivorev1.IndexStatsResponse{
// 		IndexName:     h.cfg.AzureSearchIndexName,
// 		DocumentCount: count,
// 		Status:        "ready",
// 	}, nil
// }
//
// func (h *systemHandler) CreateIndex(_ context.Context, _ *omnivorev1.CreateIndexRequest) (*omnivorev1.CreateIndexResponse, error) {
// 	if err := h.search.CreateIndex(); err != nil {
// 		return nil, err
// 	}
// 	return &omnivorev1.CreateIndexResponse{
// 		Status:    "created",
// 		IndexName: h.cfg.AzureSearchIndexName,
// 	}, nil
// }
