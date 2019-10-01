package backoff

import (
	"context"
	"net/http"
	"strconv"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

// HTTPClient is the interface of an http client that is compatible with
// *http.Client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// RetryConfig configures the backoff-retry behaviour.
type RetryConfig struct {
	MinWait    time.Duration
	MaxWait    time.Duration
	MaxRetries int
	CheckRetry retryablehttp.CheckRetry
	Backoff    retryablehttp.Backoff
}

// DefaultRetryConfig is the default config for retrying http requests.
var DefaultRetryConfig = RetryConfig{
	MinWait:    1 * time.Second,
	MaxWait:    30 * time.Second,
	MaxRetries: 4,
	CheckRetry: DefaultRetryPolicy,
	Backoff:    DefaultBackoff,
}

// retryableClient wraps *retryablehttp.Client to be compatible with the
// HTTPClient interface.
type retryableClient struct {
	*retryablehttp.Client
}

// WithRetries wraps httpClient with backoff-retry logic.
func WithRetries(httpClient *http.Client, config *RetryConfig) HTTPClient {
	cfg := DefaultRetryConfig
	if config != nil {
		cfg = *config
	}

	c := &retryableClient{
		&retryablehttp.Client{
			HTTPClient:   httpClient,
			RetryWaitMin: cfg.MinWait,
			RetryWaitMax: cfg.MaxWait,
			RetryMax:     cfg.MaxRetries,
			CheckRetry:   cfg.CheckRetry,
			Backoff:      cfg.Backoff,
		},
	}

	return c
}

// Do implements HTTPClient. It is an adapter for *retryablehttp.Client.Do and
// takes care of wrapping the *http.Request with the custom
// *retyablehttp.Request type.
func (c *retryableClient) Do(req *http.Request) (*http.Response, error) {
	wrappedReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return nil, err
	}

	return c.Client.Do(wrappedReq)
}

// DefaultRetryPolicy provides a callback for retryablehttp.Client.CheckRetry, which
// will retry on connection errors, server errors and request throttling.
func DefaultRetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// do not retry on context.Canceled or context.DeadlineExceeded
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	if err != nil {
		return true, err
	}

	// Check the response code. We retry on 500-range responses to allow the
	// server time to recover, as 500's are typically not permanent errors and
	// may relate to outages on the server side. This will catch invalid
	// response codes as well, like 0 and 999. It will also catch 429
	// ToManyRequests responses.
	if resp.StatusCode == 0 || resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode != 501) {
		return true, nil
	}

	return false, nil
}

// DefaultBackoff provides a callback for retryablehttp.Client.Backoff which will
// perform exponential backoff based on the attempt number and limited by the
// provided minimum and maximum durations. On 429 responses it will try to
// parse the Retry-After header use that value as backoff. Will fallback to
// exponential backoff if the Retry-After header is not present or cannot be
// parsed.
func DefaultBackoff(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
	retryAfter, ok := getRetryAfter(resp)
	if ok && retryAfter > 0 {
		if retryAfter > max {
			return max
		}

		return retryAfter
	}

	return retryablehttp.DefaultBackoff(min, max, attemptNum, resp)
}

// getRetryAfter obtains the timeout from the Retry-After header if set. The
// second return value is true if a valid Retry-After value was found.
func getRetryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil || resp.Header == nil {
		return 0, false
	}

	retryAfter := resp.Header.Get("Retry-After")

	seconds, err := strconv.ParseInt(retryAfter, 10, 64)
	if err == nil {
		timeout := time.Duration(seconds) * time.Second

		return timeout, true
	}

	return 0, false
}