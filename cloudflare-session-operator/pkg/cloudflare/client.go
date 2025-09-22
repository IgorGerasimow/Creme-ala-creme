package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Client defines the minimal surface used by the operator to interact with Cloudflare.
type Client interface {
	EnsureSession(ctx context.Context, sessionID string) (bool, error)
	EnsureRoute(ctx context.Context, sessionID, endpoint string) error
	DeleteRoute(ctx context.Context, sessionID string) error
}

// APIClient is a lightweight implementation of Client built on top of the Cloudflare REST API.
type APIClient struct {
	HTTPClient *http.Client
	AccountID  string
	APIToken   string
}

// NewClientFromEnv creates a Client using environment variables for configuration.
// Expected environment variables:
//   - CLOUDFLARE_ACCOUNT_ID
//   - CLOUDFLARE_API_TOKEN
func NewClientFromEnv() Client {
	return &APIClient{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		AccountID:  os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		APIToken:   os.Getenv("CLOUDFLARE_API_TOKEN"),
	}
}

func (c *APIClient) EnsureSession(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("sessionID is empty")
	}
	if c.APIToken == "" || c.AccountID == "" {
		// Without credentials we assume session exists but log that Cloudflare integration is disabled.
		return true, nil
	}

	// TODO: integrate with actual Cloudflare session validation endpoint.
	return true, nil
}

func (c *APIClient) EnsureRoute(ctx context.Context, sessionID, endpoint string) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID is empty")
	}
	if endpoint == "" {
		return fmt.Errorf("endpoint is empty")
	}
	if c.APIToken == "" || c.AccountID == "" {
		return nil
	}

	// TODO: integrate with Cloudflare Workers KV or Load Balancer API.
	return nil
}

func (c *APIClient) DeleteRoute(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	if c.APIToken == "" || c.AccountID == "" {
		return nil
	}

	// TODO: delete Cloudflare route once API integration is implemented.
	return nil
}
