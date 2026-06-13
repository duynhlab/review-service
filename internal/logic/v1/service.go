package v1

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/duynhlab/review-service/internal/core/domain"
	"github.com/duynhlab/review-service/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type ReviewService struct {
	repo domain.ReviewRepository
}

func NewReviewService(repo domain.ReviewRepository) *ReviewService {
	return &ReviewService{repo: repo}
}

func (s *ReviewService) ListReviews(ctx context.Context, productID string, limit, offset int) ([]domain.Review, int, error) {
	ctx, span := middleware.StartSpan(ctx, "review.list", trace.WithAttributes(
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
	ctx, span := middleware.StartSpan(ctx, "review.create", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("product.id", req.ProductID),
	))
	defer span.End()

	// Validate rating range
	if req.Rating < 1 || req.Rating > 5 {
		span.SetAttributes(attribute.Bool("review.created", false))
		return nil, fmt.Errorf("create review for product %q with rating %d: %w", req.ProductID, req.Rating, ErrInvalidRating)
	}

	// Convert IDs to int
	productID, err := strconv.Atoi(req.ProductID)
	if err != nil {
		return nil, fmt.Errorf("invalid product id %q: %w", req.ProductID, ErrInvalidInput)
	}
	userID, err := strconv.Atoi(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id %q: %w", req.UserID, ErrInvalidInput)
	}

	// Check for duplicate review
	existing, err := s.repo.GetReviewByProductAndUser(ctx, productID, userID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("check existing review: %w", err)
	}
	if existing != nil {
		span.SetAttributes(attribute.Bool("review.created", false))
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
			return nil, fmt.Errorf("create review for product %q: %w", req.ProductID, ErrDuplicateReview)
		}
		return nil, fmt.Errorf("insert review: %w", err)
	}

	span.SetAttributes(
		attribute.String("review.id", createdReview.ID),
		attribute.Bool("review.created", true),
	)
	span.AddEvent("review.created")

	return createdReview, nil
}
