package v1

import (
	"context"
	"testing"

	reviewv1 "github.com/duynhlab/pkg/proto/review/v1"
	"github.com/duynhlab/review-service/internal/core/domain"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestServer_GetProductReviews_TruncationLogged covers the two branches added
// with the optional logger: NewServer accepting a supplied logger, and the
// result-truncation Warn emitted when the page limit is hit.
func TestServer_GetProductReviews_TruncationLogged(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	reviews := make([]domain.Review, grpcReviewLimit)
	for i := range reviews {
		reviews[i] = domain.Review{ID: "x", ProductID: "1", UserID: "1", Rating: 5}
	}
	srv := NewServer(&listStub{reviews: reviews, total: grpcReviewLimit}, logger)

	resp, err := srv.GetProductReviews(context.Background(), &reviewv1.GetProductReviewsRequest{ProductId: "1"})
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if len(resp.GetReviews()) != grpcReviewLimit {
		t.Fatalf("reviews len = %d, want %d", len(resp.GetReviews()), grpcReviewLimit)
	}
	if logs.FilterMessageSnippet("truncated").Len() == 0 {
		t.Error("expected a truncation warning to be logged")
	}
}
