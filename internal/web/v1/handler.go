package v1

import (
	"errors"
	"net/http"

	"github.com/duynhlab/review-service/internal/core/domain"
	logicv1 "github.com/duynhlab/review-service/internal/logic/v1"
	"github.com/duynhlab/review-service/middleware"
	"github.com/duynhlab/pkg/httpx"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type ReviewHandler struct {
	service *logicv1.ReviewService
}

func NewReviewHandler(service *logicv1.ReviewService) *ReviewHandler {
	return &ReviewHandler{service: service}
}

func (h *ReviewHandler) ListReviews(c *gin.Context) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	// Parse product_id from query string (required)
	productID := c.Query("product_id")
	if productID == "" {
		span.SetAttributes(attribute.Bool("request.valid", false))
		zapLogger.Error("Missing product_id query parameter")
		httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "product_id query parameter is required")
		return
	}
	span.SetAttributes(attribute.String("product.id", productID))

	page, pageSize := httpx.ParsePage(c)
	reviews, total, err := h.service.ListReviews(ctx, productID, pageSize, httpx.Offset(page, pageSize))
	if err != nil {
		span.RecordError(err)
		zapLogger.Error("Failed to list reviews", zap.Error(err), zap.String("product_id", productID))
		if errors.Is(err, logicv1.ErrInvalidInput) {
			httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "Invalid product_id")
			return
		}
		httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, "Internal server error")
		return
	}

	zapLogger.Info("Reviews listed", zap.Int("count", len(reviews)), zap.String("product_id", productID))
	c.JSON(http.StatusOK, httpx.NewPaginated(reviews, page, pageSize, total))
}

func (h *ReviewHandler) CreateReview(c *gin.Context) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	var req domain.CreateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		span.SetAttributes(attribute.Bool("request.valid", false))
		span.RecordError(err)
		zapLogger.Error("Invalid request", zap.Error(err))
		httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "invalid request body")
		return
	}

	// Identity is taken from the authenticated context, never the request body.
	req.UserID = c.GetString("user_id")

	span.SetAttributes(attribute.Bool("request.valid", true))
	review, err := h.service.CreateReview(ctx, req)
	if err != nil {
		span.RecordError(err)
		zapLogger.Error("Failed to create review", zap.Error(err))

		switch {
		case errors.Is(err, logicv1.ErrInvalidInput):
			httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "Invalid input")
		case errors.Is(err, logicv1.ErrInvalidRating):
			httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "Invalid rating (must be 1-5)")
		case errors.Is(err, logicv1.ErrDuplicateReview):
			httpx.RespondError(c, http.StatusConflict, httpx.CodeConflict, "Review already exists")
		default:
			httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, "Internal server error")
		}
		return
	}

	zapLogger.Info("Review created", zap.String("review_id", review.ID))
	c.JSON(http.StatusCreated, review)
}
