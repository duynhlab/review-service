package v1_test

import (
	"context"
	"testing"

	"github.com/duynhlab/review-service/internal/core/domain"
	v1 "github.com/duynhlab/review-service/internal/logic/v1"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectHistogram sums the count and value-sum of an int64 histogram by name.
func collectHistogram(t *testing.T, reader sdkmetric.Reader, name string) (count uint64, sum int64) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[int64])
			if !ok {
				t.Fatalf("%s is %T, want Histogram[int64]", name, m.Data)
			}
			for _, dp := range h.DataPoints {
				count += dp.Count
				sum += dp.Sum
			}
		}
	}
	return count, sum
}

// collectNoLabelCounter sums an int64 counter (no label dimensions) by name.
func collectNoLabelCounter(t *testing.T, reader sdkmetric.Reader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is %T, want Sum[int64]", name, m.Data)
			}
			for _, dp := range s.DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

// TestReviewBusinessMetrics drives the review business instruments on a single
// MeterProvider. The OTel global delegate is first-wins, so exactly one provider
// is installed per test binary; this test is intentionally NOT parallel so it
// records and asserts before the t.Parallel() service tests resume and pollute
// the shared reader. Values are asserted cumulatively.
//
// Runs at the default -count=1 only: -count>1 re-executes in the same process,
// where first-wins delegation keeps the instruments bound to the first
// iteration's reader, so later iterations observe an empty fresh reader.
func TestReviewBusinessMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

	ctx := context.Background()

	okRepo := func() *mockReviewRepository {
		return &mockReviewRepository{
			getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) { return nil, nil },
			createFn: func(_ context.Context, r domain.Review) (*domain.Review, error) {
				r.ID = "1"
				return &r, nil
			},
		}
	}

	// Three successful creates with known ratings → histogram count 3, sum 12.
	for _, rating := range []int{5, 4, 3} {
		svc := v1.NewReviewService(okRepo())
		if _, err := svc.CreateReview(ctx, domain.CreateReviewRequest{
			ProductID: "10", UserID: "20", Rating: rating, Comment: "x",
		}); err != nil {
			t.Fatalf("CreateReview rating=%d: %v", rating, err)
		}
	}
	if count, sum := collectHistogram(t, reader, "reviews.rating"); count != 3 || sum != 12 {
		t.Errorf("reviews.rating count/sum = %d/%d, want 3/12", count, sum)
	}

	// Duplicate pre-check rejection → counter increments.
	preCheckDup := v1.NewReviewService(&mockReviewRepository{
		getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) {
			return &domain.Review{ID: "55"}, nil
		},
	})
	if _, err := preCheckDup.CreateReview(ctx, domain.CreateReviewRequest{
		ProductID: "10", UserID: "20", Rating: 5, Comment: "x",
	}); err == nil {
		t.Fatal("expected duplicate pre-check error")
	}

	// Unique-violation race on insert → counter increments again.
	raceDup := v1.NewReviewService(&mockReviewRepository{
		getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) { return nil, nil },
		createFn: func(_ context.Context, _ domain.Review) (*domain.Review, error) {
			return nil, domain.ErrDuplicateReview
		},
	})
	if _, err := raceDup.CreateReview(ctx, domain.CreateReviewRequest{
		ProductID: "10", UserID: "20", Rating: 5, Comment: "x",
	}); err == nil {
		t.Fatal("expected duplicate race error")
	}

	if got := collectNoLabelCounter(t, reader, "reviews.duplicate_rejected.total"); got != 2 {
		t.Errorf("reviews.duplicate_rejected.total = %d, want 2", got)
	}

	// Exported recorder called from the gRPC transport increments its counter.
	v1.RecordReviewsTruncated(ctx)
	v1.RecordReviewsTruncated(ctx)
	if got := collectNoLabelCounter(t, reader, "grpc.reviews_truncated.total"); got != 2 {
		t.Errorf("grpc.reviews_truncated.total = %d, want 2", got)
	}

	// A rating-validation failure must NOT touch either instrument.
	badRating := v1.NewReviewService(&mockReviewRepository{})
	if _, err := badRating.CreateReview(ctx, domain.CreateReviewRequest{
		ProductID: "10", UserID: "20", Rating: 6, Comment: "x",
	}); err == nil {
		t.Fatal("expected invalid-rating error")
	}
	if count, _ := collectHistogram(t, reader, "reviews.rating"); count != 3 {
		t.Errorf("reviews.rating count = %d after invalid rating, want 3 (no record)", count)
	}
}
