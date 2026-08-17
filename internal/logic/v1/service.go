package v1

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/duynhlab/pkg/obsx"
	"github.com/duynhlab/review-service/internal/core/domain"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tracerScope is the OpenTelemetry instrumentation scope for this package's
// spans: it names the CODE that creates them, which is why it is a package path
// and not the service name. Deployment identity travels separately as
// service.name on the Resource.
const tracerScope = "github.com/duynhlab/review-service/internal/logic/v1"

type ReviewService struct {
	repo domain.ReviewRepository
}

func NewReviewService(repo domain.ReviewRepository) *ReviewService {
	return &ReviewService{repo: repo}
}

func (s *ReviewService) ListReviews(ctx context.Context, productID string, limit, offset int) ([]domain.Review, int, error) {
	ctx, span := obsx.StartSpan(ctx, tracerScope, "review.list", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("product.id", productID),
	))
	defer span.End()

	// Convert productID to int
	prodID, err := strconv.Atoi(productID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid product_id %q: %w", productID, ErrInvalidInput)
	}

	total, err := s.repo.CountReviewsByProduct(ctx, prodID)
	if err != nil {
		span.RecordError(err)
		return nil, 0, fmt.Errorf("count reviews by product: %w", err)
	}

	reviews, err := s.repo.ListReviewsByProduct(ctx, prodID, limit, offset)
	if err != nil {
		span.RecordError(err)
		return nil, 0, fmt.Errorf("list reviews by product: %w", err)
	}

	span.SetAttributes(attribute.Int("reviews.count", len(reviews)))
	return reviews, total, nil
}

func (s *ReviewService) CreateReview(ctx context.Context, req domain.CreateReviewRequest) (*domain.Review, error) {
	ctx, span := obsx.StartSpan(ctx, tracerScope, "review.create", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("product.id", req.ProductID),
	))
	defer span.End()

	// Validate rating range
	if req.Rating < 1 || req.Rating > 5 {
		span.SetAttributes(attribute.Bool("review.created", false))
		return nil, fmt.Errorf("create review for product %q with rating %d: %w", req.ProductID, req.Rating, ErrInvalidRating)
	}

	// Products are SERIAL-keyed, so the product id must be numeric.
	productID, err := strconv.Atoi(req.ProductID)
	if err != nil {
		return nil, fmt.Errorf("invalid product id %q: %w", req.ProductID, ErrInvalidInput)
	}
	// The user id is the OIDC token subject — an opaque string, so only
	// emptiness is invalid.
	if req.UserID == "" {
		return nil, fmt.Errorf("user id is required: %w", ErrInvalidInput)
	}

	// Check for duplicate review
	existing, err := s.repo.GetReviewByProductAndUser(ctx, productID, req.UserID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("check existing review: %w", err)
	}
	if existing != nil {
		span.SetAttributes(attribute.Bool("review.created", false))
		recordDuplicateRejected(ctx)
		return nil, fmt.Errorf("create review for product %q: %w", req.ProductID, ErrDuplicateReview)
	}

	review := domain.Review{
		ProductID: req.ProductID,
		UserID:    req.UserID,
		Rating:    req.Rating,
		Title:     req.Title,
		Comment:   req.Comment,
	}

	createdReview, err := s.repo.CreateReview(ctx, review)
	if err != nil {
		span.RecordError(err)
		// Race-safe duplicate detection: the unique constraint may reject a
		// concurrent insert that slipped past the pre-check above.
		if errors.Is(err, domain.ErrDuplicateReview) {
			span.SetAttributes(attribute.Bool("review.created", false))
			recordDuplicateRejected(ctx)
			return nil, fmt.Errorf("create review for product %q: %w", req.ProductID, ErrDuplicateReview)
		}
		return nil, fmt.Errorf("insert review: %w", err)
	}

	span.SetAttributes(
		attribute.String("review.id", createdReview.ID),
		attribute.Bool("review.created", true),
	)
	span.AddEvent("review.created")
	recordReviewRating(ctx, createdReview.Rating)

	return createdReview, nil
}
