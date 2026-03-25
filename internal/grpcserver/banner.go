// internal/grpcserver/banner.go
// Implements BannerServiceServer (ask, ingest, blob, summarize).
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
// type bannerHandler struct {
// 	omnivorev1.UnimplementedBannerServiceServer
// 	cfg      *config.Config
// 	openai   *azure.OpenAIClient
// 	search   *azure.SearchClient
// 	pipeline *rag.Pipeline
// }
//
// func (h *bannerHandler) Ask(_ context.Context, req *omnivorev1.BannerAskRequest) (*omnivorev1.AskResponse, error) {
// 	topK := int(req.TopK)
// 	if topK == 0 {
// 		topK = h.cfg.TopKDefault
// 	}
// 	resp, err := h.pipeline.Ask(rag.AskRequest{
// 		Question:         req.Question,
// 		TopK:             topK,
// 		VersionFilter:    req.VersionFilter,
// 		ModuleFilter:     req.ModuleFilter,
// 		YearFilter:       req.YearFilter,
// 		SourceTypeFilter: "banner",
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
// 	return toAskResponse(resp), nil
// }
//
// func (h *bannerHandler) Ingest(_ context.Context, req *omnivorev1.BannerIngestRequest) (*omnivorev1.IngestResponse, error) {
// 	docsPath := req.DocsPath
// 	if docsPath == "" {
// 		docsPath = "data/docs/banner"
// 	}
// 	pagesPerBatch := int(req.PagesPerBatch)
// 	if pagesPerBatch == 0 {
// 		pagesPerBatch = 10
// 	}
// 	result, err := ingest.Run(h.cfg, docsPath, req.Overwrite, pagesPerBatch, int(req.StartPage), int(req.EndPage))
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &omnivorev1.IngestResponse{
// 		Status:              result.Status,
// 		DocumentsProcessed:  int32(result.DocumentsProcessed),
// 		ChunksIndexed:       int32(result.ChunksIndexed),
// 		Message:             result.Message,
// 	}, nil
// }
//
// // toAskResponse converts a rag.AskResponse to the proto type.
// func toAskResponse(r *rag.AskResponse) *omnivorev1.AskResponse {
// 	resp := &omnivorev1.AskResponse{
// 		Answer:         r.Answer,
// 		RetrievalCount: int32(r.RetrievalCount),
// 	}
// 	for _, s := range r.Sources {
// 		resp.Sources = append(resp.Sources, &omnivorev1.SourceChunk{
// 			Filename:      s.Filename,
// 			PageNumber:    int32(s.PageNumber),
// 			ChunkText:     s.ChunkText,
// 			Score:         float32(s.Score),
// 			SourceType:    s.SourceType,
// 			SopNumber:     s.SOPNumber,
// 			DocumentTitle: s.DocumentTitle,
// 			BannerModule:  s.BannerModule,
// 			BannerVersion: s.BannerVersion,
// 		})
// 	}
// 	return resp
// }
