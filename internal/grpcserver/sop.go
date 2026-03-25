// internal/grpcserver/sop.go
// Implements SOPServiceServer (ask, ingest, list).
// Uncomment the body after running `buf generate`.
package grpcserver

// import (
// 	"context"
//
// 	"go-omnivore-rag/config"
// 	"go-omnivore-rag/internal/azure"
// 	"go-omnivore-rag/internal/ingest"
// 	"go-omnivore-rag/internal/rag"
// 	omnivorev1 "go-omnivore-rag/gen/go/omnivore/v1"
// )
//
// type sopHandler struct {
// 	omnivorev1.UnimplementedSOPServiceServer
// 	cfg      *config.Config
// 	search   *azure.SearchClient
// 	pipeline *rag.Pipeline
// }
//
// func (h *sopHandler) Ask(_ context.Context, req *omnivorev1.SOPAskRequest) (*omnivorev1.AskResponse, error) {
// 	topK := int(req.TopK)
// 	if topK == 0 {
// 		topK = h.cfg.TopKDefault
// 	}
// 	resp, err := h.pipeline.Ask(rag.AskRequest{
// 		Question:         req.Question,
// 		TopK:             topK,
// 		SourceTypeFilter: "sop",
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
// 	return toAskResponse(resp), nil
// }
//
// func (h *sopHandler) Ingest(_ context.Context, req *omnivorev1.SOPIngestRequest) (*omnivorev1.IngestResponse, error) {
// 	result, err := ingest.Run(h.cfg, "data/docs/sop", req.Overwrite, 0, 0, 0)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &omnivorev1.IngestResponse{
// 		Status:             result.Status,
// 		DocumentsProcessed: int32(result.DocumentsProcessed),
// 		ChunksIndexed:      int32(result.ChunksIndexed),
// 		Message:            result.Message,
// 	}, nil
// }
//
// func (h *sopHandler) List(_ context.Context, _ *omnivorev1.SOPListRequest) (*omnivorev1.SOPListResponse, error) {
// 	entries, err := h.search.ListSOPs()
// 	if err != nil {
// 		return nil, err
// 	}
// 	resp := &omnivorev1.SOPListResponse{Count: int32(len(entries))}
// 	for _, e := range entries {
// 		resp.Sops = append(resp.Sops, &omnivorev1.SOPEntry{
// 			SopNumber:  e.SOPNumber,
// 			Title:      e.Title,
// 			ChunkCount: int32(e.ChunkCount),
// 		})
// 	}
// 	return resp, nil
// }
