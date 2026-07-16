package v1

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Business metrics for review, answering the on-call questions that plain RED
// metrics cannot:
//  1. What is the star-rating distribution of new reviews?   → reviews.rating
//  2. How often are duplicate reviews rejected?              → reviews.duplicate_rejected.total
//  3. Are gRPC review reads being silently truncated?        → grpc.reviews_truncated.total
//
// Instruments ride the global OTel MeterProvider that obsx.SetupObservability
// installs (RFC-0014 OTLP pipeline → collector → VictoriaMetrics). Before that
// setup the global provider is a no-op, so package-init here is safe. Names are
// OTel-style; the collector renders them as reviews_rating (histogram),
// reviews_duplicate_rejected_total, and grpc_reviews_truncated_total.
//
// None of these carry labels (RFC-0017 D-9): the rating is the histogram's own
// value dimension, and the two counters are single global signals — no ids, no
// free-form text.
var (
	meter = otel.Meter("review-service")

	// ratingHistogram records the star rating of each successful review create.
	// Bucket boundaries are the five discrete star values, so each bucket maps to
	// one rating and the distribution reads directly off the histogram.
	ratingHistogram, _ = meter.Int64Histogram("reviews.rating",
		metric.WithDescription("Star rating of newly created reviews (domain distribution)"),
		metric.WithUnit("1"),
		metric.WithExplicitBucketBoundaries(1, 2, 3, 4, 5))

	duplicateRejectedCounter, _ = meter.Int64Counter("reviews.duplicate_rejected.total",
		metric.WithDescription("Review creates rejected as duplicates (pre-check + unique-violation race)"))

	// Fires when a response fills the page cap. This over-counts by one edge
	// case: a product with exactly the cap and no dropped reviews still counts,
	// since a full page is indistinguishable from a truncated one.
	reviewsTruncatedCounter, _ = meter.Int64Counter("grpc.reviews_truncated.total",
		metric.WithDescription("GetProductReviews responses that filled the page cap (possible silent data loss)"))
)

// recordReviewRating records one successful review's star rating. Rating is a
// bounded 1-5 value (validated in CreateReview and enforced by the DB CHECK),
// so it always lands in a defined bucket.
func recordReviewRating(ctx context.Context, rating int) {
	ratingHistogram.Record(ctx, int64(rating))
}

// recordDuplicateRejected counts one rejected duplicate review. Called from both
// rejection paths in CreateReview: the pre-check that finds an existing review,
// and the unique-constraint violation that a concurrent insert trips.
func recordDuplicateRejected(ctx context.Context) {
	duplicateRejectedCounter.Add(ctx, 1)
}

// RecordReviewsTruncated counts one gRPC GetProductReviews response that hit the
// page cap and may have dropped reviews. Exported because the truncation site
// lives in the gRPC transport (internal/grpc/v1), one layer out from logic.
func RecordReviewsTruncated(ctx context.Context) {
	reviewsTruncatedCounter.Add(ctx, 1)
}
