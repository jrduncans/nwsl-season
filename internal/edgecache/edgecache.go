// Package edgecache names the cache tags pages carry and purges them from
// Cloudflare's edge cache when the data behind them changes.
//
// Production's Cloudflare cache rule stores each page for as long as its
// Cache-Control allows. Pages carry a Cache-Tag header (which Cloudflare
// strips before responses reach visitors), and the scheduler purges the
// affected tags after a sync changes data, so pages can be cached long without
// showing stale data. See README "Runtime and deployment".
package edgecache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Cache tags are zone-wide, and the zone serves other apps, so every tag
// names this app.
const (
	// TagPage is on every page the edge may cache.
	TagPage = "nwsl-page"
	// TagLive is on pages that current-season data can change: current and
	// future season pages, and pages outside a season such as Explore and the
	// sitemap.
	TagLive = "nwsl-live"
)

// DefaultAPIBaseURL is Cloudflare's API root.
const DefaultAPIBaseURL = "https://api.cloudflare.com/client/v4"

const maxErrorBodyBytes = 4 << 10

// Purger removes every cached response carrying any of the given tags.
type Purger interface {
	PurgeTags(ctx context.Context, tags []string) error
}

// Cloudflare purges by cache tag through Cloudflare's API. The token needs only
// the Zone > Cache Purge > Purge permission on the zone.
type Cloudflare struct {
	ZoneID string
	Token  string
	// BaseURL overrides DefaultAPIBaseURL. Tests point it at a fake.
	BaseURL string
	// HTTPClient sends the request. Nil uses http.DefaultClient; the caller's
	// context bounds each purge.
	HTTPClient *http.Client
}

// PurgeTags asks Cloudflare to purge every cached response tagged with one of
// tags. A response Cloudflare doesn't confirm with "success": true is an
// error.
func (c Cloudflare) PurgeTags(ctx context.Context, tags []string) error {
	if c.ZoneID == "" || c.Token == "" {
		return errors.New("cloudflare cache purge needs a zone ID and an API token")
	}
	if len(tags) == 0 {
		return nil
	}
	body, err := json.Marshal(struct {
		Tags []string `json:"tags"`
	}{tags})
	if err != nil {
		return fmt.Errorf("encode cloudflare cache purge request: %w", err)
	}
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	endpoint := strings.TrimSuffix(baseURL, "/") + "/zones/" + c.ZoneID + "/purge_cache"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build cloudflare cache purge request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("send cloudflare cache purge request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("read cloudflare cache purge response (HTTP %d): %w", response.StatusCode, err)
	}
	var result struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	decodeErr := json.Unmarshal(responseBody, &result)
	if response.StatusCode == http.StatusOK && decodeErr == nil && result.Success {
		return nil
	}
	if decodeErr == nil && len(result.Errors) > 0 {
		messages := make([]string, 0, len(result.Errors))
		for _, apiError := range result.Errors {
			messages = append(messages, fmt.Sprintf("%d: %s", apiError.Code, apiError.Message))
		}
		return fmt.Errorf("cloudflare cache purge failed (HTTP %d): %s", response.StatusCode, strings.Join(messages, "; "))
	}
	return fmt.Errorf("cloudflare cache purge failed (HTTP %d): %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
}

func (c Cloudflare) httpClient() *http.Client {
	base := c.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	// Trace the purge like other downstream calls. Copying preserves caller
	// settings such as test-server TLS configuration.
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = otelhttp.NewTransport(transport,
		otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			return "HTTP " + request.Method + " cloudflare purge_cache"
		}),
	)
	return &client
}
