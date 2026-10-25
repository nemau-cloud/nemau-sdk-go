package nemau

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient connects a client to a stub server, with short retry and poll
// delays so that tests run quickly.
func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(
		WithBaseURL(srv.URL),
		WithCredential(AccessKey{ID: "AKIA", Secret: "s3cr3t"}),
		WithRetry(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}),
		WithPollInterval(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func TestRequestShape(t *testing.T) {
	var got *http.Request
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"instances":[]}`))
	}))

	if _, err := c.Compute.ListInstances(context.Background()); err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	if want := "/api/v1/compute/instances"; got.URL.Path != want {
		t.Errorf("path = %q, want %q", got.URL.Path, want)
	}
	if got.Header.Get("X-Access-Key-Id") != "AKIA" || got.Header.Get("X-Access-Key-Secret") != "s3cr3t" {
		t.Errorf("access key headers missing: %v", got.Header)
	}
	// With an access key, no Authorization header may be sent: the API would
	// then pick the wrong credential. The SDK must send exactly one.
	if got.Header.Get("Authorization") != "" {
		t.Errorf("unexpected Authorization header with an access key")
	}
	if !strings.HasPrefix(got.Header.Get("User-Agent"), "nemau-sdk-go/") {
		t.Errorf("User-Agent = %q", got.Header.Get("User-Agent"))
	}
}

func TestBearerCredential(t *testing.T) {
	var authz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"zones":[]}`))
	}))
	defer srv.Close()

	c, err := NewClient(WithBaseURL(srv.URL), WithCredential(BearerToken("jwt-token")))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.DNS.ListZones(context.Background()); err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if authz != "Bearer jwt-token" {
		t.Errorf("Authorization = %q", authz)
	}
}

func TestCredentialFromEnv(t *testing.T) {
	t.Setenv(EnvAccessKeyID, "id")
	t.Setenv(EnvAccessKeySecret, "secret")
	t.Setenv(EnvToken, "token")
	// The access key takes precedence, so that an expired NEMAU_TOKEN left in a
	// shell does not hide it.
	if _, ok := CredentialFromEnv().(AccessKey); !ok {
		t.Fatalf("expected AccessKey, got %T", CredentialFromEnv())
	}

	t.Setenv(EnvAccessKeyID, "")
	t.Setenv(EnvAccessKeySecret, "")
	if _, ok := CredentialFromEnv().(BearerToken); !ok {
		t.Fatalf("expected BearerToken, got %T", CredentialFromEnv())
	}

	t.Setenv(EnvToken, "")
	if cred := CredentialFromEnv(); cred != nil {
		t.Fatalf("expected nil credential, got %v", cred)
	}
}

func TestAPIErrorDecoding(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"not authorized for compute:Write","reason":"permission_denied"}`))
	}))

	_, err := c.Compute.CreateInstance(context.Background(), CreateInstanceRequest{Name: "x", ImageID: "img"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsForbidden(err) {
		t.Fatalf("IsForbidden = false for %v", err)
	}
	if !strings.Contains(err.Error(), "not authorized for compute:Write") ||
		!strings.Contains(err.Error(), "permission_denied") {
		t.Errorf("error text lost detail: %v", err)
	}
}

func TestSubnetOverlapIsRecognizable(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"cidr 10.0.1.0/24 overlaps subnet \"app\" (10.0.0.0/16) on network \"backend\"; the subnets of a project are routed together, so their ranges must not overlap","reason":"subnet_overlap"}`))
	}))

	_, err := c.Network.CreateSubnet(context.Background(), "net-1", CreateSubnetRequest{Name: "web", CIDR: "10.0.1.0/24"})
	if !IsSubnetOverlap(err) {
		t.Fatalf("IsSubnetOverlap = false for %v", err)
	}
	if !strings.Contains(err.Error(), `overlaps subnet "app" (10.0.0.0/16) on network "backend"`) {
		t.Errorf("error text lost the conflicting subnet: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: a refusal must not be retried", calls)
	}

	for _, other := range []error{
		&APIError{StatusCode: http.StatusConflict, Message: "listener already has a pool"},
		&APIError{StatusCode: http.StatusForbidden, Reason: "permission_denied"},
		&TaskFailedError{},
		nil,
	} {
		if IsSubnetOverlap(other) {
			t.Errorf("IsSubnetOverlap(%v) = true", other)
		}
	}
}

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"networks":[{"id":"net-1","name":"prod"}]}`))
	}))

	nets, err := c.Network.ListNetworks(context.Background())
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	if len(nets) != 1 || nets[0].ID != "net-1" {
		t.Fatalf("unexpected networks: %+v", nets)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"flavor_id or complete custom sizing is required"}`))
	}))

	if _, err := c.Compute.CreateInstance(context.Background(), CreateInstanceRequest{Name: "x", ImageID: "i"}); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: a 400 must not be retried", calls)
	}
}

func TestSubmitWithoutTaskIDIsAnError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}))
	// Returning an empty reference here would turn into a WaitForTask that
	// polls a task id of "" forever.
	if _, err := c.Network.CreateNetwork(context.Background(), "n", ""); err == nil ||
		!strings.Contains(err.Error(), "no task id") {
		t.Fatalf("err = %v, want a missing-task-id error", err)
	}
}
