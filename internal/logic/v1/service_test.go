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
	listFn   func(ctx context.Context, productID, limit, offset int) ([]domain.Review, error)
	countFn  func(ctx context.Context, productID int) (int, error)
	createFn func(ctx context.Context, review domain.Review) (*domain.Review, error)
	getFn    func(ctx context.Context, productID int, userID string) (*domain.Review, error)
}

func (m *mockReviewRepository) ListReviewsByProduct(ctx context.Context, productID, limit, offset int) ([]domain.Review, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, productID, limit, offset)
}

func (m *mockReviewRepository) CountReviewsByProduct(ctx context.Context, productID int) (int, error) {
	if m.countFn == nil {
		return 0, nil
	}
	return m.countFn(ctx, productID)
}

func (m *mockReviewRepository) CreateReview(ctx context.Context, review domain.Review) (*domain.Review, error) {
	if m.createFn == nil {
		return nil, nil
	}
	return m.createFn(ctx, review)
}

func (m *mockReviewRepository) GetReviewByProductAndUser(ctx context.Context, productID int, userID string) (*domain.Review, error) {
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
			UserID:    "a11ce000-0000-4000-8000-000000000001", // OIDC token subject (opaque string)
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
				getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) {
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
				getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) {
					return &domain.Review{ID: "55"}, nil // existing review found
				},
			},
			wantErr: v1.ErrDuplicateReview,
		},
		{
			name: "duplicate review race on insert",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) {
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
			// The user id is the OIDC token subject — opaque, so only
			// emptiness is invalid (non-numeric subjects are valid now).
			name:    "empty user id",
			req:     domain.CreateReviewRequest{ProductID: "10", UserID: "", Rating: 5, Comment: "x"},
			repo:    &mockReviewRepository{},
			wantErr: v1.ErrInvalidInput,
		},
		{
			name: "repo error on existence check",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) {
					return nil, errRepo
				},
			},
			wantErr: errRepo,
		},
		{
			name: "repo error on insert",
			req:  validReq(),
			repo: &mockReviewRepository{
				getFn: func(_ context.Context, _ int, _ string) (*domain.Review, error) {
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

// TestReviewService_CreateReview_SubjectRoundTrip is the regression test for
// the swallowed user-id conversion: a non-numeric OIDC subject must reach the
// repository layer verbatim, both on the duplicate pre-check and on the insert.
// Under the old code (`userID, _ := strconv.Atoi(review.UserID)` in the
// repository) a non-numeric subject silently became user_id=0 in the DB.
func TestReviewService_CreateReview_SubjectRoundTrip(t *testing.T) {
	t.Parallel()

	const subject = "f47ac10b-58cc-4372-a567-0e02b2c3d479" // non-numeric Keycloak `sub`

	var gotCheckUserID, gotInsertUserID string
	repo := &mockReviewRepository{
		getFn: func(_ context.Context, _ int, userID string) (*domain.Review, error) {
			gotCheckUserID = userID
			return nil, nil // no existing review
		},
		createFn: func(_ context.Context, r domain.Review) (*domain.Review, error) {
			gotInsertUserID = r.UserID
			r.ID = "100"
			return &r, nil
		},
	}

	svc := v1.NewReviewService(repo)
	created, err := svc.CreateReview(context.Background(), domain.CreateReviewRequest{
		ProductID: "10",
		UserID:    subject,
		Rating:    5,
		Comment:   "opaque subject",
	})
	if err != nil {
		t.Fatalf("CreateReview() unexpected error = %v", err)
	}

	if gotCheckUserID != subject {
		t.Errorf("duplicate pre-check received user id %q, want the exact subject %q", gotCheckUserID, subject)
	}
	if gotInsertUserID != subject {
		t.Errorf("repository insert received user id %q, want the exact subject %q (old code silently stored 0)", gotInsertUserID, subject)
	}
	if created.UserID != subject {
		t.Errorf("created review user id = %q, want %q", created.UserID, subject)
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
		wantTotal int
	}{
		{
			name:      "success with reviews",
			productID: "10",
			repo: &mockReviewRepository{
				countFn: func(_ context.Context, _ int) (int, error) {
					return 5, nil
				},
				listFn: func(_ context.Context, _, _, _ int) ([]domain.Review, error) {
					return []domain.Review{{ID: "1"}, {ID: "2"}}, nil
				},
			},
			wantCount: 2,
			wantTotal: 5,
		},
		{
			name:      "success empty",
			productID: "10",
			repo: &mockReviewRepository{
				listFn: func(_ context.Context, _, _, _ int) ([]domain.Review, error) {
					return []domain.Review{}, nil
				},
			},
			wantCount: 0,
			wantTotal: 0,
		},
		{
			name:      "non-numeric product id",
			productID: "abc",
			repo:      &mockReviewRepository{},
			wantErr:   v1.ErrInvalidInput,
		},
		{
			name:      "count repo error",
			productID: "10",
			repo: &mockReviewRepository{
				countFn: func(_ context.Context, _ int) (int, error) {
					return 0, errRepo
				},
			},
			wantErr: errRepo,
		},
		{
			name:      "repo error",
			productID: "10",
			repo: &mockReviewRepository{
				listFn: func(_ context.Context, _, _, _ int) ([]domain.Review, error) {
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
			got, total, err := svc.ListReviews(context.Background(), tt.productID, 20, 0)

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
			if total != tt.wantTotal {
				t.Errorf("ListReviews() total = %d, want %d", total, tt.wantTotal)
			}
		})
	}
}
