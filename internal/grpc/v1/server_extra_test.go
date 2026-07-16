package v1

import (
	"context"
	"testing"

	reviewv1 "github.com/duynhlab/pkg/proto/review/v1"
	"github.com/duynhlab/review-service/internal/core/domain"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestServer_GetProductReviews_TruncationLogged covers the branches added with
// the optional logger and the RFC-0017 W2 metric: NewServer accepting a supplied
// logger, and the result-truncation Warn + grpc.reviews_truncated.total counter
// emitted when the page limit is hit. The metric read installs the sole
// MeterProvider for this test binary (OTel global delegate is first-wins), so
// this runs at the default -count=1 only; -count>1 re-executes in-process and
// later iterations see an empty fresh reader.
func TestServer_GetProductReviews_TruncationLogged(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

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
	if got := truncatedCount(t, reader); got != 1 {
		t.Errorf("grpc.reviews_truncated.total = %d, want 1", got)
	}
}

// truncatedCount reads the grpc.reviews_truncated.total counter value.
func truncatedCount(t *testing.T, reader sdkmetric.Reader) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "grpc.reviews_truncated.total" {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("counter is %T, want Sum[int64]", m.Data)
			}
			for _, dp := range s.DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}
