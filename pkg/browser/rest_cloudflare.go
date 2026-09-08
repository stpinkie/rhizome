// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// CloudflareClient is a stateless client for Cloudflare Browser Rendering
// REST endpoints (snapshot / screenshot / content / markdown).
type CloudflareClient struct {
	accountID string
	token     string
	base      string
	client    *http.Client
}

// NewCloudflareClient builds a client from the backend config; requires
// account_id and api_key (token with "Browser Rendering - Edit" permission).
func NewCloudflareClient(cfg config.BrowserBackendConfig) (*CloudflareClient, error) {
	account := strings.TrimSpace(cfg.AccountID)
	if account == "" {
		return nil, fmt.Errorf("account_id is required for the cloudflare backend")
	}
	token := strings.TrimSpace(cfg.APIKey.String())
	if token == "" {
		return nil, fmt.Errorf("api_key is required for the cloudflare backend")
	}
	base := defaultBase(cfg, "https://api.cloudflare.com/client/v4")
	return &CloudflareClient{
		accountID: account,
		token:     token,
		base:      base,
		client:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *CloudflareClient) endpoint(action string) string {
	return fmt.Sprintf("%s/accounts/%s/browser-rendering/%s", c.base, c.accountID, action)
}

func (c *CloudflareClient) post(
	ctx context.Context,
	action string,
	payload map[string]any,
) (map[string]any, error) {
	return postJSON(ctx, c.client, c.endpoint(action),
		map[string]string{"Authorization": "Bearer " + c.token}, payload)
}

// Snapshot returns a page snapshot (accessibility tree + content) for url.
func (c *CloudflareClient) Snapshot(ctx context.Context, url string) (string, error) {
	out, err := c.post(ctx, "snapshot", map[string]any{"url": url})
	if err != nil {
		return "", fmt.Errorf("cloudflare snapshot: %w", err)
	}
	// The API wraps results under "result".
	if res, ok := out["result"].(map[string]any); ok {
		parts := []string{}
		for _, k := range []string{"accessibility_tree", "content", "markdown"} {
			if v := stringField(res, k); v != "" {
				parts = append(parts, v)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n"), nil
		}
	}
	return fmt.Sprintf("%v", out), nil
}

// Screenshot captures a PNG screenshot of url into outPath.
func (c *CloudflareClient) Screenshot(ctx context.Context, url, outPath string) (string, error) {
	if strings.TrimSpace(outPath) == "" {
		return "", fmt.Errorf("screenshot output path is required")
	}
	out, err := c.post(ctx, "screenshot", map[string]any{"url": url})
	if err != nil {
		return "", fmt.Errorf("cloudflare screenshot: %w", err)
	}
	if res, ok := out["result"].(map[string]any); ok {
		if b64 := stringField(res, "screenshot", "image", "data"); b64 != "" {
			raw, decErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(b64, "data:image/png;base64,"))
			if decErr != nil {
				return "", fmt.Errorf("decode screenshot: %w", decErr)
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(outPath, raw, 0o600); err != nil {
				return "", err
			}
			return "screenshot saved to " + outPath, nil
		}
	}
	return "", fmt.Errorf("cloudflare screenshot: no image data in response")
}
