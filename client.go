// Package nemau is the Go client for the Nemau Cloud public API (/api/v1), the
// same API the web console uses.
//
// Writes are asynchronous. A create, update or delete answers 202 with a task
// id, and the platform does the work in the background. Every write method
// returns a [TaskRef]; [Client.WaitFor] waits until that task finishes.
// Methods suffixed AndWait do both.
//
// The organization (tenant) comes from the credential, and a call cannot leave
// it. The project comes from the WithProject option, then NEMAU_PROJECT_ID,
// then the credential; [Client.ForProject] returns a client for another
// project. A service account pinned to a project cannot leave it: the API
// answers 403.
//
// The package has no dependencies outside the standard library.
package nemau

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public gateway. Override it with NEMAU_API_URL or
// [WithBaseURL] for a staging environment or a local docker-compose stack
// (http://localhost:8000).
const DefaultBaseURL = "https://api.nemau-cloud.com"

// apiPrefix is the API version prefix. It is a constant, not an option, because
// every path in this package is written for v1.
const apiPrefix = "/api/v1"

// Version is reported in the User-Agent so the platform can correlate client
// releases with request patterns.
const Version = "0.5.0"

// Client is a Nemau Cloud API client. It is safe for concurrent use.
//
// Build one with [NewClient]; the zero value is not usable.
type Client struct {
	baseURL      *url.URL
	httpClient   *http.Client
	credential   Credential
	userAgent    string
	retry        RetryPolicy
	pollInterval time.Duration
	// projectID overrides the credential's own project on every request. Empty
	// means "whatever the credential carries".
	projectID string

	// Service handles, grouped like the API paths: /api/v1/compute/... is
	// under Compute, and so on.
	Compute      *ComputeService
	Network      *NetworkService
	LoadBalancer *LoadBalancerService
	Storage      *StorageService
	Kubernetes   *KubernetesService
	DNS          *DNSService
	Metering     *MeteringService
	Regions      *RegionsService
	Tasks        *TasksService
	Projects     *ProjectsService
	Quotas       *QuotasService
}

// ProjectScopeHeader names the project a request acts on. Sent only when a
// project is selected; otherwise the credential's own scope applies.
const ProjectScopeHeader = "X-Project-Id"

// WithProject scopes every request to one project, overriding the project the
// credential itself carries.
//
// It does not persist anything server-side, so two clients built from the same
// credential can target two projects concurrently. A service account pinned to
// a project cannot be moved off it: the API answers 403.
func WithProject(projectID string) Option {
	return func(c *Client) error {
		c.projectID = strings.TrimSpace(projectID)
		return nil
	}
}

// ForProject returns a copy of the client scoped to another project.
//
// The original client is left unchanged, so calls already running on it keep
// their project. The copy shares the HTTP client and the credential.
func (c *Client) ForProject(projectID string) *Client {
	clone := *c
	clone.projectID = strings.TrimSpace(projectID)
	clone.attachServices()
	return &clone
}

// Project returns the selected project id, empty when the credential's own
// scope applies.
func (c *Client) Project() string { return c.projectID }

// Option configures a [Client].
type Option func(*Client) error

// WithBaseURL points the client at a specific gateway origin. The /api/v1
// prefix is added by the client, so pass only the origin.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		u, err := url.Parse(strings.TrimRight(raw, "/"))
		if err != nil {
			return fmt.Errorf("nemau: invalid base URL %q: %w", raw, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("nemau: base URL %q must be absolute (scheme://host)", raw)
		}
		c.baseURL = u
		return nil
	}
}

// WithCredential sets the credential presented on every request.
func WithCredential(cred Credential) Option {
	return func(c *Client) error {
		c.credential = cred
		return nil
	}
}

// WithHTTPClient supplies the underlying *http.Client. Use it to set a proxy,
// a custom TLS config, or a global timeout.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("nemau: http client must not be nil")
		}
		c.httpClient = hc
		return nil
	}
}

// WithUserAgent prepends a caller identifier to the SDK's own User-Agent.
func WithUserAgent(ua string) Option {
	return func(c *Client) error {
		if ua != "" {
			c.userAgent = ua + " " + c.userAgent
		}
		return nil
	}
}

// WithRetry replaces the retry policy. A zero MaxAttempts disables retries.
func WithRetry(p RetryPolicy) Option {
	return func(c *Client) error {
		c.retry = p
		return nil
	}
}

// WithPollInterval sets how often [Client.WaitForTask] re-reads a task.
func WithPollInterval(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("nemau: poll interval must be positive")
		}
		c.pollInterval = d
		return nil
	}
}

// NewClient builds a client. With no options it reads NEMAU_API_URL and the
// credential environment variables described in [CredentialFromEnv]; explicit
// options always win over the environment.
//
// A missing credential is not an error: some read-only endpoints, such as
// /regions, need no authentication. Authenticated calls then fail with a 401
// [APIError].
func NewClient(opts ...Option) (*Client, error) {
	c := &Client{
		httpClient:   &http.Client{Timeout: 60 * time.Second},
		userAgent:    "nemau-sdk-go/" + Version,
		retry:        DefaultRetryPolicy(),
		pollInterval: 2 * time.Second,
	}

	base := DefaultBaseURL
	if env := envOrEmpty("NEMAU_API_URL"); env != "" {
		base = env
	}
	if err := WithBaseURL(base)(c); err != nil {
		return nil, err
	}
	if cred := CredentialFromEnv(); cred != nil {
		c.credential = cred
	}
	c.projectID = envOrEmpty(EnvProject)

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	c.attachServices()
	return c, nil
}

// attachServices binds the service handles to c. It is called on construction
// and on every ForProject copy, so that the copy's handles use the copy's
// project.
func (c *Client) attachServices() {
	c.Compute = &ComputeService{client: c}
	c.Network = &NetworkService{client: c}
	c.LoadBalancer = &LoadBalancerService{client: c}
	c.Storage = &StorageService{client: c}
	c.Kubernetes = &KubernetesService{client: c}
	c.DNS = &DNSService{client: c}
	c.Metering = &MeteringService{client: c}
	c.Regions = &RegionsService{client: c}
	c.Tasks = &TasksService{client: c}
	c.Projects = &ProjectsService{client: c}
	c.Quotas = &QuotasService{client: c}
}

// BaseURL returns the configured gateway origin.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// PollInterval returns the interval [Client.WaitForTask] uses.
func (c *Client) PollInterval() time.Duration { return c.pollInterval }

// request is one API call, before retries.
type request struct {
	method string
	path   string // relative to /api/v1, e.g. "/compute/instances"
	query  url.Values
	body   any

	// rawBody streams a body instead of JSON-encoding one (object upload). When
	// set, body is ignored and contentType must be set.
	rawBody       io.Reader
	contentLength int64
	contentType   string

	// idempotencyKey, when set, is sent as the Idempotency-Key header and is the
	// same on every attempt of this call. The key makes a write safe to retry:
	// the platform answers a replay with the original task instead of queueing a
	// second one. Only [Client.submit] sets it.
	idempotencyKey string
	// replayed, when non-nil, receives whether the platform answered the call as
	// a replay of an earlier one with the same key (Idempotent-Replayed: true).
	replayed *bool
}

// do performs a request and decodes the JSON response into out (which may be
// nil to discard the body).
func (c *Client) do(ctx context.Context, r request, out any) error {
	// A streamed body cannot be replayed, so a retry would send an empty
	// request. Such calls get exactly one attempt.
	attempts := c.retry.MaxAttempts
	if attempts < 1 || r.rawBody != nil {
		attempts = 1
	}

	var encoded []byte
	if r.rawBody == nil && r.body != nil {
		var err error
		if encoded, err = json.Marshal(r.body); err != nil {
			return fmt.Errorf("nemau: encoding request body: %w", err)
		}
	}

	// A read can always be repeated. A write can only be repeated when it
	// carries a key, so that the platform recognises the repetition: a refused
	// or lost POST may or may not have queued its task.
	replayable := safeMethod(r.method) || r.idempotencyKey != ""

	var lastErr error
	var wait time.Duration
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleepCtx(ctx, wait); err != nil {
				return err
			}
		}
		wait = c.retry.backoff(attempt)

		req, err := c.newHTTPRequest(ctx, r, encoded)
		if err != nil {
			return err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// A transport failure is retryable when repeating the call cannot
			// duplicate its effect: an idempotent verb, or a write carrying a key.
			lastErr = fmt.Errorf("nemau: %s %s: %w", r.method, r.path, err)
			if idempotent(r.method) || r.idempotencyKey != "" {
				continue
			}
			return lastErr
		}

		apiErr := decodeError(resp)
		if apiErr == nil {
			defer resp.Body.Close()
			if r.replayed != nil {
				*r.replayed = resp.Header.Get(ReplayedHeader) == "true"
			}
			return decodeInto(resp, out)
		}
		resp.Body.Close()
		lastErr = apiErr
		if !apiErr.Retryable() || !replayable {
			return apiErr
		}
		if apiErr.RetryAfter > 0 {
			// Wait as long as the server asks, up to the policy's limit. A longer
			// delay returns the error at once.
			if apiErr.RetryAfter > c.retry.maxRetryAfter() {
				return apiErr
			}
			if apiErr.RetryAfter > wait {
				wait = apiErr.RetryAfter
			}
		}
	}
	return lastErr
}

func (c *Client) newHTTPRequest(ctx context.Context, r request, encoded []byte) (*http.Request, error) {
	u := *c.baseURL
	// r.path is already escaped: each method applies url.PathEscape to its data
	// segments. Assigning it to u.Path would escape it a second time ("%" becomes
	// "%25"), and the server would look up a different name than the caller gave.
	// Path carries the decoded form and RawPath the escaped one; String() then
	// emits RawPath unchanged.
	escaped := strings.TrimRight(c.baseURL.EscapedPath(), "/") + apiPrefix + r.path
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, fmt.Errorf("nemau: invalid request path %q: %w", r.path, err)
	}
	u.Path, u.RawPath = decoded, escaped
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}

	var body io.Reader
	switch {
	case r.rawBody != nil:
		body = r.rawBody
	case encoded != nil:
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("nemau: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	switch {
	case r.rawBody != nil:
		req.Header.Set("Content-Type", r.contentType)
		// The API rejects an object upload without a Content-Length, and Go only
		// sets one for a body whose size it can determine on its own.
		req.ContentLength = r.contentLength
	case encoded != nil:
		req.Header.Set("Content-Type", "application/json")
	}
	if c.projectID != "" {
		req.Header.Set(ProjectScopeHeader, c.projectID)
	}
	if r.idempotencyKey != "" {
		req.Header.Set(IdempotencyKeyHeader, r.idempotencyKey)
	}
	if c.credential != nil {
		c.credential.apply(req)
	}
	return req, nil
}

func decodeInto(resp *http.Response, out any) error {
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("nemau: decoding response: %w", err)
	}
	return nil
}

// idempotent reports whether a verb may be replayed after a transport failure.
//
// PUT is included because every PUT on this API replaces the whole state.
// PATCH is included too. HTTP does not guarantee that in general, since a PATCH
// may describe a delta, but on this API a PATCH body is a set of fields and
// their new values: applying it twice gives the same state as applying it once.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// RetryPolicy controls retries of retryable failures (429, 5xx, and transport
// errors).
//
// Reads are always retried. A write is retried only when it carries an
// Idempotency-Key, which every asynchronous write does (see
// [WithIdempotencyKey]); without one a repeat could queue the work twice.
// When the server names a delay with Retry-After (429 and 503 do) the client
// waits at least that long.
type RetryPolicy struct {
	MaxAttempts int           // total attempts, including the first
	BaseDelay   time.Duration // delay before the second attempt
	MaxDelay    time.Duration // ceiling for the exponential backoff
	// MaxRetryAfter is the longest Retry-After the client will wait out. A
	// longer one is not retried: the refusal is returned at once. Zero means
	// 60 seconds, the delay the write rate limiter asks for.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy is 4 attempts with jittered exponential backoff.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:   4,
		BaseDelay:     300 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		MaxRetryAfter: defaultMaxRetryAfter,
	}
}

const defaultMaxRetryAfter = 60 * time.Second

func (p RetryPolicy) maxRetryAfter() time.Duration {
	if p.MaxRetryAfter <= 0 {
		return defaultMaxRetryAfter
	}
	return p.MaxRetryAfter
}

// safeMethod reports whether a verb only reads.
func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// parseRetryAfter reads a Retry-After header: delay-seconds or an HTTP date.
// Zero means absent, malformed, or already due.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// backoff returns the delay before retry n (n starts at 1). It uses full
// jitter, so that many clients failing at the same moment do not all retry at
// the same moment.
func (p RetryPolicy) backoff(n int) time.Duration {
	base := p.BaseDelay
	if base <= 0 {
		base = 300 * time.Millisecond
	}
	maxDelay := p.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}
	d := time.Duration(float64(base) * math.Pow(2, float64(n-1)))
	if d > maxDelay {
		d = maxDelay
	}
	// #nosec G404 -- retry jitter only; not security-sensitive.
	return time.Duration(rand.Int63n(int64(d) + 1))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
