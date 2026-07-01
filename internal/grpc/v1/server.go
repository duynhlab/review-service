// Package v1 implements the gRPC transport for review, version 1. It is a thin
// adapter over the logic layer (mirroring internal/web/v1) so the gRPC and HTTP
// paths share the same business logic and return identical data.
package v1

import (
	"context"
	"errors"
	"time"

	reviewv1 "github.com/duynhlab/pkg/proto/review/v1"
	"github.com/duynhlab/review-service/internal/core/domain"
	logicv1 "github.com/duynhlab/review-service/internal/logic/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReviewLister is the logic-layer dependency the gRPC server needs.
// *logicv1.ReviewService satisfies it.
type ReviewLister interface {
	ListReviews(ctx context.Context, productID string, limit, offset int) ([]domain.Review, int, error)
}

// grpcReviewLimit bounds the page the gRPC path requests. The proto has no
// pagination fields and GetProductReviews must return every review, so we ask
// for a single large page (offset 0) rather than maintaining a separate
// unpaginated logic method.
const grpcReviewLimit = 10000

// Server implements reviewv1.ReviewServiceServer.
type Server struct {
	reviewv1.UnimplementedReviewServiceServer

	svc    ReviewLister
	logger *zap.Logger
}

// NewServer creates a gRPC ReviewService server backed by the logic service. An
// optional logger may be supplied (used to surface result truncation); when
// omitted it defaults to a no-op logger.
func NewServer(svc ReviewLister, logger ...*zap.Logger) *Server {
	l := zap.NewNop()
	if len(logger) > 0 && logger[0] != nil {
		l = logger[0]
	}
	return &Server{svc: svc, logger: l}
}

// GetProductReviews mirrors GET /review/v1/public/reviews?product_id=…, returning
// every review for the given product.
func (s *Server) GetProductReviews(
	ctx context.Context,
	req *reviewv1.GetProductReviewsRequest,
) (*reviewv1.GetProductReviewsResponse, error) {
	reviews, _, err := s.svc.ListReviews(ctx, req.GetProductId(), grpcReviewLimit, 0)
	if err != nil {
		if errors.Is(err, logicv1.ErrInvalidInput) {
			return nil, status.Error(codes.InvalidArgument, "invalid product_id")
		}
		return nil, status.Error(codes.Internal, "failed to list reviews")
	}

	// The proto has no pagination, so a product with more than grpcReviewLimit
	// reviews is silently truncated. Log it so the truncation is observable.
	if len(reviews) >= grpcReviewLimit {
		s.logger.Warn("GetProductReviews result hit page limit; results may be truncated",
			zap.String("product_id", req.GetProductId()),
			zap.Int("limit", grpcReviewLimit),
		)
	}

	out := make([]*reviewv1.Review, 0, len(reviews))
	for i := range reviews {
		out = append(out, toProto(&reviews[i]))
	}
	return &reviewv1.GetProductReviewsResponse{Reviews: out}, nil
}

// toProto maps a domain review to its protobuf representation. CreatedAt is
// rendered as RFC3339 (empty when unset).
func toProto(r *domain.Review) *reviewv1.Review {
	createdAt := ""
	if r.CreatedAt != nil {
		createdAt = r.CreatedAt.Format(time.RFC3339)
	}
	return &reviewv1.Review{
		Id:        r.ID,
		ProductId: r.ProductID,
		UserId:    r.UserID,
		Rating:    int32(r.Rating), //nolint:gosec // G115: rating is a bounded 1-5 value (DB CHECK), no overflow
		Title:     r.Title,
		Comment:   r.Comment,
		CreatedAt: createdAt,
	}
}
