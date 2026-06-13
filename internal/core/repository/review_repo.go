package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/duynhlab/review-service/internal/core/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgUniqueViolation is the PostgreSQL SQLSTATE for a unique_violation error.
const pgUniqueViolation = "23505"

type pgxReviewRepository struct {
	pool *pgxpool.Pool
}

// NewReviewRepository creates a new instance of ReviewRepository.
func NewReviewRepository(pool *pgxpool.Pool) domain.ReviewRepository {
	return &pgxReviewRepository{pool: pool}
}

func (r *pgxReviewRepository) ListReviewsByProduct(ctx context.Context, productID, limit, offset int) ([]domain.Review, error) {
	query := `SELECT id, product_id, user_id, rating, title, comment, created_at FROM reviews WHERE product_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.pool.Query(ctx, query, productID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query reviews: %w", err)
	}
	defer rows.Close()

	var reviews []domain.Review
	for rows.Next() {
		var reviewID, dbProductID, userID int
		var rating int
		var title, comment *string
		var createdAt *time.Time

		if err := rows.Scan(&reviewID, &dbProductID, &userID, &rating, &title, &comment, &createdAt); err != nil {
			return nil, fmt.Errorf("scan review row: %w", err)
		}

		review := domain.Review{
			ID:        strconv.Itoa(reviewID),
			ProductID: strconv.Itoa(dbProductID),
			UserID:    strconv.Itoa(userID),
			Rating:    rating,
			CreatedAt: createdAt,
		}
		if title != nil {
			review.Title = *title
		}
		if comment != nil {
			review.Comment = *comment
		}

		reviews = append(reviews, review)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return reviews, nil
}

func (r *pgxReviewRepository) CountReviewsByProduct(ctx context.Context, productID int) (int, error) {
	query := `SELECT COUNT(*) FROM reviews WHERE product_id = $1`
	var total int
	if err := r.pool.QueryRow(ctx, query, productID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count reviews: %w", err)
	}
	return total, nil
}

func (r *pgxReviewRepository) CreateReview(ctx context.Context, review domain.Review) (*domain.Review, error) {
	prodID, _ := strconv.Atoi(review.ProductID)
	userID, _ := strconv.Atoi(review.UserID)

	query := `INSERT INTO reviews (product_id, user_id, rating, title, comment) VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`

	var reviewID int
	var createdAt time.Time

	err := r.pool.QueryRow(ctx, query, prodID, userID, review.Rating, review.Title, review.Comment).Scan(&reviewID, &createdAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return nil, domain.ErrDuplicateReview
		}
		return nil, fmt.Errorf("insert review: %w", err)
	}

	review.ID = strconv.Itoa(reviewID)
	review.CreatedAt = &createdAt
	return &review, nil
}

func (r *pgxReviewRepository) GetReviewByProductAndUser(ctx context.Context, productID, userID int) (*domain.Review, error) {
	query := `SELECT id FROM reviews WHERE product_id = $1 AND user_id = $2`
	var id int
	err := r.pool.QueryRow(ctx, query, productID, userID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("check existing review: %w", err)
	}

	return &domain.Review{ID: strconv.Itoa(id)}, nil
}
