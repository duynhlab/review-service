package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AuthUser represents the user info returned from the auth service.
type AuthUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// AuthClient handles communication with the auth service.
type AuthClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAuthClient creates a new auth client with a 5s-timeout HTTP client.
func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// GetMe retrieves user info from the auth service using the token.
// The caller's request context is propagated for cancellation and tracing.
func (c *AuthClient) GetMe(ctx context.Context, token string) (*AuthUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/auth/v1/private/me", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request auth service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("invalid or expired token")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth service error: %d - %s", resp.StatusCode, string(body))
	}

	var user AuthUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &user, nil
}

// AuthMiddleware validates the bearer token via the auth service and sets
// "user_id" in the gin context on success. It fails closed: any missing or
// malformed Authorization header, or any GetMe error, results in a 401.
func AuthMiddleware(authClient *AuthClient) gin.HandlerFunc {
	const bearerPrefix = "Bearer "
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if len(authHeader) <= len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		token := authHeader[len(bearerPrefix):]

		authUser, err := authClient.GetMe(c.Request.Context(), token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		c.Set("user_id", authUser.ID)
		c.Next()
	}
}
