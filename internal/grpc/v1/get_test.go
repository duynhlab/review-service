package v1

import (
	"context"
	"errors"
	"testing"
	"time"

	reviewv1 "github.com/duynhlab/pkg/proto/review/v1"
	"github.com/duynhlab/review-service/internal/core/domain"
	logicv1 "github.com/duynhlab/review-service/internal/logic/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// listStub is a configurable ReviewLister double for GetProductReviews.
type listStub struct {
	reviews []domain.Review
	total   int
	err     error
}

func (s *listStub) ListReviews(_ context.Context, _ string, _, _ int) ([]domain.Review, int, error) {
	return s.reviews, s.total, s.err
}

func TestServer_GetProductReviews(t *testing.T) {
	t.Run("success maps domain to proto", func(t *testing.T) {
		created := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
		srv := NewServer(&listStub{reviews: []domain.Review{
			{ID: "9", ProductID: "1", UserID: "3", Rating: 5, Title: "Great", Comment: "Loved it", CreatedAt: &created},
		}, total: 1})

		resp, err := srv.GetProductReviews(context.Background(), &reviewv1.GetProductReviewsRequest{ProductId: "1"})
		if err != nil {
			t.Fatalf("got error %v, want nil", err)
		}
		got := resp.GetReviews()
		if len(got) != 1 {
			t.Fatalf("reviews len = %d, want 1", len(got))
		}
		r := got[0]
		if r.GetId() != "9" || r.GetProductId() != "1" || r.GetUserId() != "3" {
			t.Errorf("id/product/user = %q/%q/%q, want 9/1/3", r.GetId(), r.GetProductId(), r.GetUserId())
		}
		if r.GetRating() != 5 || r.GetTitle() != "Great" || r.GetComment() != "Loved it" {
			t.Errorf("rating/title/comment = %d/%q/%q", r.GetRating(), r.GetTitle(), r.GetComment())
		}
		if r.GetCreatedAt() != created.Format(time.RFC3339) {
			t.Errorf("created_at = %q, want %q", r.GetCreatedAt(), created.Format(time.RFC3339))
		}
	})

	t.Run("empty list returns empty response", func(t *testing.T) {
		srv := NewServer(&listStub{reviews: nil, total: 0})
		resp, err := srv.GetProductReviews(context.Background(), &reviewv1.GetProductReviewsRequest{ProductId: "1"})
		if err != nil {
			t.Fatalf("got error %v, want nil", err)
		}
		if len(resp.GetReviews()) != 0 {
			t.Errorf("reviews len = %d, want 0", len(resp.GetReviews()))
		}
	})

	t.Run("invalid input -> InvalidArgument", func(t *testing.T) {
		srv := NewServer(&listStub{err: logicv1.ErrInvalidInput})
		_, err := srv.GetProductReviews(context.Background(), &reviewv1.GetProductReviewsRequest{ProductId: "abc"})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("generic error -> Internal", func(t *testing.T) {
		srv := NewServer(&listStub{err: errors.New("db down")})
		_, err := srv.GetProductReviews(context.Background(), &reviewv1.GetProductReviewsRequest{ProductId: "1"})
		if status.Code(err) != codes.Internal {
			t.Fatalf("code = %v, want Internal", status.Code(err))
		}
	})

	t.Run("toProto leaves created_at empty when nil", func(t *testing.T) {
		srv := NewServer(&listStub{reviews: []domain.Review{{ID: "1", ProductID: "1", UserID: "1", Rating: 4}}, total: 1})
		resp, err := srv.GetProductReviews(context.Background(), &reviewv1.GetProductReviewsRequest{ProductId: "1"})
		if err != nil {
			t.Fatalf("got error %v, want nil", err)
		}
		if resp.GetReviews()[0].GetCreatedAt() != "" {
			t.Errorf("created_at = %q, want empty", resp.GetReviews()[0].GetCreatedAt())
		}
	})
}
