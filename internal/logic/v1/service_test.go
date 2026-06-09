package v1_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/duynhlab/review-service/internal/core/domain"
	v1 "github.com/duynhlab/review-service/internal/logic/v1"
	"github.com/duynhlab/review-service/middleware"
)

// TestMain warms up the global OpenTelemetry tracer once, before any parallel
// subtest runs. The logic layer calls middleware.StartSpan, whose GetTracer
// lazily writes a package-level tracer on first use; doing that single-threaded
// here avoids a benign data race on that lazy initialisation under -race.
func TestMain(m *testing.M) {
	middleware.GetTracer()
	os.Exit(m.Run())
}

// mockReviewRepository is a configurable in-memory stub of
// domain.ReviewRepository. Each method delegates to a function field so tests
// can tailor behaviour per case; nil fields return safe zero values.
type mockReviewRepository struct {
	listFn   func(ctx context.Context, productID int) ([]domain.Review, error)
	createFn func(ctx context.Context, review domain.Review) (*domain.Review, error)
	getFn    func(ctx context.Context, productID, userID int) (*domain.Review, error)
}

func (m *mockReviewRepository) ListReviewsByProduct(ctx context.Context, productID int) ([]domain.Review, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, productID)
}

func (m *mockReviewRepository) CreateReview(ctx context.Context, review domain.Review) (*domain.Review, error) {
	if m.createFn == nil {
		return nil, nil
	}
	return m.createFn(ctx, review)
}

func (m *mockReviewRepository) GetReviewByProductAndUser(ctx context.Context, productID, userID int) (*domain.Review, error) {
	if m.getFn == nil {
		return nil, nil
	}
	return m.getFn(ctx, productID, userID)
}

var errRepo = errors.New("repo boom")

func TestReviewService_CreateReview(t *testing.T) {
	t.Parallel()

	validReq := func() domain.CreateReviewRequest {
		return domain.CreateReviewRequest{
			ProductID: "10",
			UserID:    "20",
			Rating:    5,
			Title:     "Great",
			Comment:   "Loved it",
		}
	}

	tests := []struct {
		name    string
		req     domain.CreateReviewRequest
		repo    *mockReviewRepository
		wantErr error // sentinel expected via errors.Is; nil means success
		wantID  string
	}{
		{
			name: "success",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
					return nil, nil // no existing review
				},
				createFn: func(_ context.Context, r domain.Review) (*domain.Review, error) {
					now := time.Now()
					r.ID = "100"
					r.CreatedAt = &now
					return &r, nil
				},
			},
			wantErr: nil,
			wantID:  "100",
		},
		{
			name: "duplicate review pre-check",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
					return &domain.Review{ID: "55"}, nil // existing review found
				},
			},
			wantErr: v1.ErrDuplicateReview,
		},
		{
			name: "duplicate review race on insert",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
					return nil, nil // pre-check passes
				},
				createFn: func(_ context.Context, _ domain.Review) (*domain.Review, error) {
					return nil, domain.ErrDuplicateReview // unique constraint rejects
				},
			},
			wantErr: v1.ErrDuplicateReview,
		},
		{
			name:    "rating too low",
			req:     domain.CreateReviewRequest{ProductID: "10", UserID: "20", Rating: 0, Comment: "x"},
			repo:    &mockReviewRepository{},
			wantErr: v1.ErrInvalidRating,
		},
		{
			name:    "rating too high",
			req:     domain.CreateReviewRequest{ProductID: "10", UserID: "20", Rating: 6, Comment: "x"},
			repo:    &mockReviewRepository{},
			wantErr: v1.ErrInvalidRating,
		},
		{
			name:    "non-numeric product id",
			req:     domain.CreateReviewRequest{ProductID: "abc", UserID: "20", Rating: 5, Comment: "x"},
			repo:    &mockReviewRepository{},
			wantErr: v1.ErrInvalidInput,
		},
		{
			name:    "non-numeric user id",
			req:     domain.CreateReviewRequest{ProductID: "10", UserID: "xyz", Rating: 5, Comment: "x"},
			repo:    &mockReviewRepository{},
			wantErr: v1.ErrInvalidInput,
		},
		{
			name: "repo error on existence check",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
					return nil, errRepo
				},
			},
			wantErr: errRepo,
		},
		{
			name: "repo error on insert",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
					return nil, nil
				},
				createFn: func(_ context.Context, _ domain.Review) (*domain.Review, error) {
					return nil, errRepo
				},
			},
			wantErr: errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := v1.NewReviewService(tt.repo)
			got, err := svc.CreateReview(context.Background(), tt.req)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CreateReview() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("CreateReview() review = %v, want nil on error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("CreateReview() unexpected error = %v", err)
			}
			if got == nil {
				t.Fatal("CreateReview() review = nil, want non-nil")
			}
			if got.ID != tt.wantID {
				t.Errorf("CreateReview() ID = %q, want %q", got.ID, tt.wantID)
			}
			if got.ProductID != tt.req.ProductID || got.Rating != tt.req.Rating {
				t.Errorf("CreateReview() = %+v, did not carry request fields", got)
			}
		})
	}
}

func TestReviewService_ListReviews(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		productID string
		repo      *mockReviewRepository
		wantErr   error
		wantCount int
	}{
		{
			name:      "success with reviews",
			productID: "10",
			repo: &mockReviewRepository{
				listFn: func(_ context.Context, _ int) ([]domain.Review, error) {
					return []domain.Review{{ID: "1"}, {ID: "2"}}, nil
				},
			},
			wantCount: 2,
		},
		{
			name:      "success empty",
			productID: "10",
			repo: &mockReviewRepository{
				listFn: func(_ context.Context, _ int) ([]domain.Review, error) {
					return []domain.Review{}, nil
				},
			},
			wantCount: 0,
		},
		{
			name:      "non-numeric product id",
			productID: "abc",
			repo:      &mockReviewRepository{},
			wantErr:   v1.ErrInvalidInput,
		},
		{
			name:      "repo error",
			productID: "10",
			repo: &mockReviewRepository{
				listFn: func(_ context.Context, _ int) ([]domain.Review, error) {
					return nil, errRepo
				},
			},
			wantErr: errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := v1.NewReviewService(tt.repo)
			got, err := svc.ListReviews(context.Background(), tt.productID)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ListReviews() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("ListReviews() = %v, want nil on error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("ListReviews() unexpected error = %v", err)
			}
			if len(got) != tt.wantCount {
				t.Errorf("ListReviews() count = %d, want %d", len(got), tt.wantCount)
			}
		})
	}
}
