package nemau

import (
	"context"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// keyRecorder answers each request with the next canned status and records the
// Idempotency-Key it carried.
type keyRecorder struct {
	mu       sync.Mutex
	keys     []string
	statuses []int
	headers  http.Header
	body     string
}

func (k *keyRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append(k.keys, r.Header.Get(IdempotencyKeyHeader))
	status := http.StatusAccepted
	if len(k.keys) <= len(k.statuses) {
		status = k.statuses[len(k.keys)-1]
	}
	for name, vals := range k.headers {
		for _, v := range vals {
			w.Header().Add(name, v)
		}
	}
	w.WriteHeader(status)
	body := k.body
	if body == "" {
		body = `{"task_id":"t-1","status":"queued"}`
		if status >= 400 {
			body = `{"error":"nope"}`
		}
	}
	_, _ = w.Write([]byte(body))
}

func TestWriteSendsAFreshUUIDv4Key(t *testing.T) {
	rec := &keyRecorder{}
	c, _ := newTestClient(t, rec)
	for range 2 {
		if _, err := c.Network.CreateNetwork(context.Background(), "n", ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.keys) != 2 {
		t.Fatalf("keys = %v", rec.keys)
	}
	for _, k := range rec.keys {
		if !uuidV4.MatchString(k) {
			t.Errorf("key %q is not a UUID v4", k)
		}
	}
	if rec.keys[0] == rec.keys[1] {
		t.Error("two logical writes shared a key")
	}
}

func TestReadsCarryNoKey(t *testing.T) {
	rec := &keyRecorder{body: `{"networks":[]}`, statuses: []int{200}}
	c, _ := newTestClient(t, rec)
	if _, err := c.Network.ListNetworks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.keys[0] != "" {
		t.Errorf("GET carried key %q", rec.keys[0])
	}
}

func TestRetriesReuseTheKey(t *testing.T) {
	rec := &keyRecorder{statuses: []int{503, 429, 202}}
	c, _ := newTestClient(t, rec)
	ref, err := c.Network.CreateNetwork(context.Background(), "n", "")
	if err != nil {
		t.Fatal(err)
	}
	if ref.TaskID != "t-1" || len(rec.keys) != 3 {
		t.Fatalf("task %q after %d attempts", ref.TaskID, len(rec.keys))
	}
	if rec.keys[0] == "" || rec.keys[0] != rec.keys[1] || rec.keys[1] != rec.keys[2] {
		t.Errorf("keys changed between attempts: %v", rec.keys)
	}
}

func TestCallerKeyOverridesTheGeneratedOne(t *testing.T) {
	rec := &keyRecorder{statuses: []int{503, 202}}
	c, _ := newTestClient(t, rec)
	ctx := WithIdempotencyKey(context.Background(), "deploy-2026-10-10.run:1")
	if _, err := c.Network.CreateNetwork(ctx, "n", ""); err != nil {
		t.Fatal(err)
	}
	for _, k := range rec.keys {
		if k != "deploy-2026-10-10.run:1" {
			t.Errorf("key = %q", k)
		}
	}
}

func TestInvalidCallerKeyFailsBeforeSending(t *testing.T) {
	rec := &keyRecorder{}
	c, _ := newTestClient(t, rec)
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	for _, key := range []string{"", "has space", "slash/key", "naïve", string(long)} {
		ctx := WithIdempotencyKey(context.Background(), key)
		if _, err := c.Network.CreateNetwork(ctx, "n", ""); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
	if len(rec.keys) != 0 {
		t.Errorf("%d requests sent with an invalid key", len(rec.keys))
	}
}

// A write without a key cannot be repeated safely, so a 503 must surface at
// once. The only keyless writes are the synchronous ones.
func TestKeylessWriteIsNotRetriedOnA503(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"service temporarily unavailable","code":"unavailable"}`))
	}))
	_, err := c.LoadBalancer.ImportCertificate(context.Background(), ImportCertificateRequest{Name: "c"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestReplayFlagIsSurfaced(t *testing.T) {
	rec := &keyRecorder{headers: http.Header{ReplayedHeader: {"true"}}}
	c, _ := newTestClient(t, rec)
	ref, err := c.Network.CreateNetwork(context.Background(), "n", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ref.Replayed {
		t.Error("Replayed = false on an Idempotent-Replayed response")
	}

	rec = &keyRecorder{}
	c, _ = newTestClient(t, rec)
	if ref, _ = c.Network.CreateNetwork(context.Background(), "n", ""); ref.Replayed {
		t.Error("Replayed = true without the header")
	}
}

func TestKeyMismatchIsDetectedAndNotRetried(t *testing.T) {
	rec := &keyRecorder{statuses: []int{422}, body: `{"error":"idempotency_key_mismatch"}`}
	c, _ := newTestClient(t, rec)
	_, err := c.Network.CreateNetwork(context.Background(), "n", "")
	if !IsIdempotencyKeyMismatch(err) {
		t.Fatalf("err = %v, want a key mismatch", err)
	}
	if len(rec.keys) != 1 {
		t.Errorf("attempts = %d, want 1", len(rec.keys))
	}
	if IsIdempotencyKeyMismatch(&APIError{StatusCode: 422, Message: "other"}) || IsIdempotencyKeyMismatch(nil) {
		t.Error("false positive")
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	var times []time.Time
	var mu sync.Mutex
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		n := len(times)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded","code":"rate_limited"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t-1"}`))
	})
	c, _ := newTestClient(t, srv)
	if _, err := c.Network.CreateNetwork(context.Background(), "n", ""); err != nil {
		t.Fatal(err)
	}
	if len(times) != 2 {
		t.Fatalf("attempts = %d", len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 900*time.Millisecond {
		t.Errorf("retried after %v, Retry-After asked for 1s", gap)
	}
}

func TestRetryAfterBeyondTheCapIsNotWaited(t *testing.T) {
	rec := &keyRecorder{statuses: []int{429, 429, 429}}
	rec.headers = http.Header{"Retry-After": {"60"}}
	c, _ := newTestClient(t, rec)
	c.retry.MaxRetryAfter = time.Second

	start := time.Now()
	_, err := c.Network.CreateNetwork(context.Background(), "n", "")
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Fatalf("err = %v", err)
	}
	if apiErr.RetryAfter != 60*time.Second {
		t.Errorf("RetryAfter = %v", apiErr.RetryAfter)
	}
	if len(rec.keys) != 1 || time.Since(start) > 500*time.Millisecond {
		t.Errorf("attempts = %d after %v, want one immediate refusal", len(rec.keys), time.Since(start))
	}
}

func TestRetriesAreBounded(t *testing.T) {
	rec := &keyRecorder{statuses: []int{503, 503, 503, 503, 503, 503}}
	c, _ := newTestClient(t, rec)
	if _, err := c.Network.CreateNetwork(context.Background(), "n", ""); err == nil {
		t.Fatal("expected an error")
	}
	if len(rec.keys) != 3 {
		t.Errorf("attempts = %d, want MaxAttempts (3)", len(rec.keys))
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"30":                            30 * time.Second,
		"0":                             0,
		"-5":                            0,
		"soon":                          0,
		"Sat, 10 Oct 2026 12:01:00 GMT": time.Minute,
		"Sat, 10 Oct 2026 11:00:00 GMT": 0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}
