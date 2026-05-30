package domain

import (
	"context"
	"errors"
)

// ErrDuplicateReview indicates a unique-constraint violation when inserting a
// review for a (product_id, user_id) pair that already exists. The repository
// returns this so the logic layer can map it to a 409 race-safely.
var ErrDuplicateReview = errors.New("duplicate review")

// ReviewRepository defines the interface for review data access.
type ReviewRepository interface {
	// ListReviewsByProduct returns all reviews for a specific product.
	ListReviewsByProduct(ctx context.Context, productID int) ([]Review, error)

	// CreateReview creates a new review.
	CreateReview(ctx context.Context, review Review) (*Review, error)

	// GetReviewByProductAndUser checks if a review already exists for a product by a user.
	GetReviewByProductAndUser(ctx context.Context, productID, userID int) (*Review, error)
}
