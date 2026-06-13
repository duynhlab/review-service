package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/duynhlab/review-service/internal/core/domain"
	logicv1 "github.com/duynhlab/review-service/internal/logic/v1"
	"github.com/duynhlab/review-service/middleware"
	"github.com/gin-gonic/gin"
)

// TestMain warms up the global OpenTelemetry tracer once before any subtest, so
// the lazy package-level tracer init in middleware.StartSpan happens
// single-threaded and avoids a benign data race under -race.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	middleware.GetTracer()
	os.Exit(m.Run())
}

// mockReviewRepo is a configurable domain.ReviewRepository double for web tests.
// Each method delegates to a function field; nil fields return safe zero values.
type mockReviewRepo struct {
	listFn   func(ctx context.Context, productID, limit, offset int) ([]domain.Review, error)
	countFn  func(ctx context.Context, productID int) (int, error)
	createFn func(ctx context.Context, review domain.Review) (*domain.Review, error)
	getFn    func(ctx context.Context, productID, userID int) (*domain.Review, error)
}

func (m *mockReviewRepo) ListReviewsByProduct(ctx context.Context, productID, limit, offset int) ([]domain.Review, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, productID, limit, offset)
}

func (m *mockReviewRepo) CountReviewsByProduct(ctx context.Context, productID int) (int, error) {
	if m.countFn == nil {
		return 0, nil
	}
	return m.countFn(ctx, productID)
}

func (m *mockReviewRepo) CreateReview(ctx context.Context, review domain.Review) (*domain.Review, error) {
	if m.createFn == nil {
		return nil, nil
	}
	return m.createFn(ctx, review)
}

func (m *mockReviewRepo) GetReviewByProductAndUser(ctx context.Context, productID, userID int) (*domain.Review, error) {
	if m.getFn == nil {
		return nil, nil
	}
	return m.getFn(ctx, productID, userID)
}

func newHandler(repo domain.ReviewRepository) *ReviewHandler {
	return NewReviewHandler(logicv1.NewReviewService(repo))
}

// newCtx builds a gin context for a GET request with no body.
func newCtx(method, target, userID string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	if userID != "" {
		c.Set("user_id", userID)
	}
	return c, rec
}

// ctxWithBody builds a gin context with a JSON request body.
func ctxWithBody(method, target, userID, body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userID != "" {
		c.Set("user_id", userID)
	}
	return c, rec
}

// decode parses the JSON response body into a map.
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestListReviews_Success(t *testing.T) {
	repo := &mockReviewRepo{
		countFn: func(_ context.Context, _ int) (int, error) { return 2, nil },
		listFn: func(_ context.Context, _, _, _ int) ([]domain.Review, error) {
			return []domain.Review{{ID: "1"}, {ID: "2"}}, nil
		},
	}
	c, rec := newCtx(http.MethodGet, "/review/v1/public/reviews?product_id=10&page=1&page_size=5", "")

	newHandler(repo).ListReviews(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decode(t, rec)
	if body["total_items"].(float64) != 2 {
		t.Errorf("total_items = %v, want 2", body["total_items"])
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 2 {
		t.Errorf("items = %v, want length 2", body["items"])
	}
}

func TestListReviews_MissingProductID(t *testing.T) {
	c, rec := newCtx(http.MethodGet, "/review/v1/public/reviews", "")
	newHandler(&mockReviewRepo{}).ListReviews(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestListReviews_InvalidProductID(t *testing.T) {
	// A non-numeric product_id surfaces ErrInvalidInput from the logic layer → 400.
	c, rec := newCtx(http.MethodGet, "/review/v1/public/reviews?product_id=abc", "")
	newHandler(&mockReviewRepo{}).ListReviews(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestListReviews_ServiceError(t *testing.T) {
	repo := &mockReviewRepo{
		countFn: func(_ context.Context, _ int) (int, error) { return 0, context.DeadlineExceeded },
	}
	c, rec := newCtx(http.MethodGet, "/review/v1/public/reviews?product_id=10", "")
	newHandler(repo).ListReviews(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "INTERNAL_ERROR" {
		t.Errorf("code = %v, want INTERNAL_ERROR", code)
	}
}

func TestCreateReview_Success(t *testing.T) {
	repo := &mockReviewRepo{
		getFn: func(_ context.Context, _, _ int) (*domain.Review, error) { return nil, nil },
		createFn: func(_ context.Context, r domain.Review) (*domain.Review, error) {
			r.ID = "100"
			return &r, nil
		},
	}
	body := `{"product_id":"10","rating":5,"comment":"Loved it"}`
	c, rec := ctxWithBody(http.MethodPost, "/review/v1/private/reviews", "20", body)

	newHandler(repo).CreateReview(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if id := decode(t, rec)["id"]; id != "100" {
		t.Errorf("id = %v, want 100", id)
	}
}

func TestCreateReview_BadJSON(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/review/v1/private/reviews", "20", "{")
	newHandler(&mockReviewRepo{}).CreateReview(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestCreateReview_ValidationError(t *testing.T) {
	// Missing required "comment" fails binding → 400.
	body := `{"product_id":"10","rating":5}`
	c, rec := ctxWithBody(http.MethodPost, "/review/v1/private/reviews", "20", body)
	newHandler(&mockReviewRepo{}).CreateReview(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestCreateReview_InvalidUserID(t *testing.T) {
	// A non-numeric user_id surfaces ErrInvalidInput from the logic layer → 400.
	body := `{"product_id":"10","rating":5,"comment":"ok"}`
	c, rec := ctxWithBody(http.MethodPost, "/review/v1/private/reviews", "not-numeric", body)
	newHandler(&mockReviewRepo{}).CreateReview(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestCreateReview_Duplicate(t *testing.T) {
	repo := &mockReviewRepo{
		getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
			return &domain.Review{ID: "55"}, nil // existing review
		},
	}
	body := `{"product_id":"10","rating":5,"comment":"dup"}`
	c, rec := ctxWithBody(http.MethodPost, "/review/v1/private/reviews", "20", body)

	newHandler(repo).CreateReview(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "CONFLICT" {
		t.Errorf("code = %v, want CONFLICT", code)
	}
}

func TestCreateReview_ServiceError(t *testing.T) {
	repo := &mockReviewRepo{
		getFn: func(_ context.Context, _, _ int) (*domain.Review, error) {
			return nil, context.DeadlineExceeded
		},
	}
	body := `{"product_id":"10","rating":5,"comment":"boom"}`
	c, rec := ctxWithBody(http.MethodPost, "/review/v1/private/reviews", "20", body)

	newHandler(repo).CreateReview(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "INTERNAL_ERROR" {
		t.Errorf("code = %v, want INTERNAL_ERROR", code)
	}
}
