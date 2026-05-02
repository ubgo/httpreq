// Package httpreq is a small JSON-over-HTTP client convenience layer on
// top of net/http.
//
// The standard library is fine but verbose for the most common service
// call: marshal a request body, set headers, send with a timeout, decode
// a JSON response, and turn non-2xx into an error. This package collapses
// that into a single options-pattern call:
//
//	var resp UserResponse
//	httpResp, err := httpreq.Do(ctx, "https://api/users",
//	    httpreq.WithMethod(http.MethodPost),
//	    httpreq.WithJSONBody(req),
//	    httpreq.WithBearerToken(token),
//	    httpreq.WithTimeout(5*time.Second),
//	    httpreq.WithResponseInto(&resp),
//	)
//
// What's deliberately NOT here:
//
//   - Retries, circuit breaking, rate limiting. Use a dedicated transport
//     or library for those concerns.
//   - GraphQL helpers, Shopify-specific extensions, etc. The lace.io
//     ancestor of this package mixed those in; they have been removed.
//   - A global default client. Pass [WithHTTPClient] explicitly if you
//     want to share connection pools.
package httpreq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HTTPError is returned when the response status is outside 2xx. The raw
// response body is captured in Body so callers can decode their own error
// shape if needed.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       []byte
}

func (e *HTTPError) Error() string {
	if len(e.Body) > 0 {
		return fmt.Sprintf("httpreq: %s: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("httpreq: %s", e.Status)
}

// Option configures a single request.
type Option func(*request) error

type request struct {
	method      string
	headers     http.Header
	queryParams url.Values
	body        io.Reader
	timeout     time.Duration
	client      *http.Client
	respInto    any
}

// WithMethod sets the HTTP method. Default: GET. Methods are not
// validated against [http.Method*] constants — the request goes out as
// you specify it.
func WithMethod(m string) Option {
	return func(r *request) error {
		r.method = m
		return nil
	}
}

// WithHeader sets a single header. Repeat the option to set multiple. To
// set repeated values for the same header (e.g. multi-value Accept), call
// it once per value.
func WithHeader(key, value string) Option {
	return func(r *request) error {
		r.headers.Add(key, value)
		return nil
	}
}

// WithBearerToken sets `Authorization: Bearer <token>`. No-op if the
// token is empty.
func WithBearerToken(token string) Option {
	return func(r *request) error {
		if token == "" {
			return nil
		}
		r.headers.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// WithQueryParam appends a URL query parameter. Repeat for multiple
// values of the same key.
func WithQueryParam(key, value string) Option {
	return func(r *request) error {
		r.queryParams.Add(key, value)
		return nil
	}
}

// WithJSONBody marshals body as JSON and sets Content-Type:
// application/json. Pass nil to clear a previously set body.
func WithJSONBody(body any) Option {
	return func(r *request) error {
		if body == nil {
			r.body = nil
			return nil
		}
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("httpreq: marshal body: %w", err)
		}
		r.body = bytes.NewReader(data)
		r.headers.Set("Content-Type", "application/json")
		return nil
	}
}

// WithRawBody sends body bytes verbatim. Caller must set Content-Type via
// [WithHeader] if it matters. Useful for form posts, protobuf, or
// pre-encoded payloads.
func WithRawBody(body []byte) Option {
	return func(r *request) error {
		r.body = bytes.NewReader(body)
		return nil
	}
}

// WithTimeout caps the total request time including dialing, headers,
// body, and response reading. Default: 30 seconds. Pass 0 to use the
// caller's context deadline alone.
func WithTimeout(d time.Duration) Option {
	return func(r *request) error {
		r.timeout = d
		return nil
	}
}

// WithHTTPClient overrides the underlying [*http.Client]. Use this to
// share a connection pool across requests or to install custom
// middleware via a transport. WithTimeout still applies — it sets
// Client.Timeout if the supplied client has none.
func WithHTTPClient(c *http.Client) Option {
	return func(r *request) error {
		if c == nil {
			return errors.New("httpreq: nil http client")
		}
		r.client = c
		return nil
	}
}

// WithResponseInto JSON-decodes the response body into v. v must be a
// pointer. Skip this option to discard the body.
func WithResponseInto(v any) Option {
	return func(r *request) error {
		r.respInto = v
		return nil
	}
}

// Do builds and sends the request. The returned *http.Response has its
// Body already drained and closed; the body bytes have been routed into
// the option supplied via [WithResponseInto], if any.
//
// Non-2xx responses return an *HTTPError so the caller can inspect the
// raw body. JSON decode errors are wrapped and returned.
func Do(ctx context.Context, endpoint string, opts ...Option) (*http.Response, error) {
	r := &request{
		method:      http.MethodGet,
		headers:     http.Header{},
		queryParams: url.Values{},
		timeout:     30 * time.Second,
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}

	finalURL, err := buildURL(endpoint, r.queryParams)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, r.method, finalURL, r.body)
	if err != nil {
		return nil, fmt.Errorf("httpreq: new request: %w", err)
	}
	req.Header = r.headers

	client := r.client
	if client == nil {
		client = &http.Client{Timeout: r.timeout}
	} else if client.Timeout == 0 && r.timeout > 0 {
		clone := *client
		clone.Timeout = r.timeout
		client = &clone
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpreq: do: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return res, fmt.Errorf("httpreq: read body: %w", err)
	}

	if res.StatusCode >= 300 {
		return res, &HTTPError{
			StatusCode: res.StatusCode,
			Status:     res.Status,
			Body:       body,
		}
	}

	if r.respInto != nil && len(body) > 0 {
		if err := json.Unmarshal(body, r.respInto); err != nil {
			return res, fmt.Errorf("httpreq: decode response: %w", err)
		}
	}

	return res, nil
}

func buildURL(endpoint string, q url.Values) (string, error) {
	if len(q) == 0 {
		return endpoint, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("httpreq: parse url: %w", err)
	}
	existing := u.Query()
	for k, vs := range q {
		for _, v := range vs {
			existing.Add(k, v)
		}
	}
	u.RawQuery = existing.Encode()
	return u.String(), nil
}
