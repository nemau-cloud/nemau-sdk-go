package nemau

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

const fakeSecret = "EXAMPLE-SECRET-NOT-REAL-0123456789"

func TestCreateCredentialSendsAccessAndOmitsUnsetExpiry(t *testing.T) {
	c := &capture{resp: `{"access_key_id":"AKEXAMPLE","secret_access_key":"` + fakeSecret + `","access":"read_only",
		"container":"photos","s3_bucket":"nemau-dev-00000000000000000000000000000000","region":"fr-occitanie-1","endpoint":"","created_at":"2026-10-04T16:06:25Z","expires_at":null}`}
	client, _ := newTestClient(t, c.handler())

	got, err := client.Storage.CreateCredential(context.Background(), "photos", CreateCredentialRequest{Access: CredentialReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	if c.method != http.MethodPost || c.path != "/api/v1/storage/containers/photos/credentials" {
		t.Errorf("request = %s %s", c.method, c.path)
	}
	if strings.Contains(c.body, "expires_at") || !strings.Contains(c.body, `"access":"read_only"`) {
		t.Errorf("body = %s, want access only and no expires_at", c.body)
	}
	if got.SecretAccessKey != fakeSecret || got.Region != "fr-occitanie-1" || got.Endpoint != "" || got.ExpiresAt != nil || got.Access != CredentialReadOnly {
		t.Errorf("decoded = %+v", got)
	}
	// The bucket to address is the store's name, not the container's.
	if got.Container != "photos" || got.S3Bucket != "nemau-dev-00000000000000000000000000000000" {
		t.Errorf("container %q / s3 bucket %q", got.Container, got.S3Bucket)
	}
}

func TestCreateCredentialSendsExpiryInUTC(t *testing.T) {
	c := &capture{resp: `{"access_key_id":"AKEXAMPLE","access":"read_write","container":"b","created_at":"2026-10-04T16:06:25Z","expires_at":"2026-10-05T12:00:00Z"}`}
	client, _ := newTestClient(t, c.handler())
	paris := time.FixedZone("CEST", 2*3600)
	got, err := client.Storage.CreateCredential(context.Background(), "b", CreateCredentialRequest{
		Access: CredentialReadWrite, ExpiresAt: time.Date(2026, 10, 5, 14, 0, 0, 0, paris),
	})
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(c.body), &sent); err != nil || sent["expires_at"] != "2026-10-05T12:00:00Z" {
		t.Errorf("body = %s (%v)", c.body, err)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("ExpiresAt = %v", got.ExpiresAt)
	}
}

func TestCreateCredentialValidatesLocallyBeforeAnyCall(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	if _, err := client.Storage.CreateCredential(context.Background(), "", CreateCredentialRequest{Access: CredentialReadOnly}); err == nil {
		t.Error("empty container accepted")
	}
	if _, err := client.Storage.CreateCredential(context.Background(), "b", CreateCredentialRequest{Access: "admin"}); err == nil {
		t.Error("unknown access accepted")
	}
	if calls != 0 {
		t.Errorf("%d request(s) were sent for invalid input", calls)
	}
}

func TestListAndRevokeCredential(t *testing.T) {
	c := &capture{resp: `{"credentials":[{"access_key_id":"AK1","access":"read_write","container":"b","created_at":"2026-10-04T16:06:25Z","expires_at":null},
		{"access_key_id":"AK2","access":"read_only","container":"b","created_at":"2026-10-04T16:07:25Z","expires_at":"2026-10-05T12:00:00Z"}]}`}
	client, _ := newTestClient(t, c.handler())
	list, err := client.Storage.ListCredentials(context.Background(), "b")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if c.method != http.MethodGet || c.path != "/api/v1/storage/containers/b/credentials" {
		t.Errorf("request = %s %s", c.method, c.path)
	}
	if list[0].ExpiresAt != nil || list[1].ExpiresAt == nil {
		t.Errorf("expiry decoding: %v / %v", list[0].ExpiresAt, list[1].ExpiresAt)
	}
	if strings.Contains(fmt.Sprintf("%+v", list), "secret") {
		t.Error("a listed key exposes a secret field")
	}

	c.resp = ""
	if err := client.Storage.RevokeCredential(context.Background(), "b", "AKEXAMPLE1"); err != nil {
		t.Fatal(err)
	}
	if c.method != http.MethodDelete || c.path != "/api/v1/storage/containers/b/credentials/AKEXAMPLE1" {
		t.Errorf("request = %s %s", c.method, c.path)
	}
}

func TestCredentialErrorsAreClassifiedByCode(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		limit  bool
		ro     bool
	}{
		{429, `{"error":"this container already has the maximum number of access keys","code":"credential_limit"}`, true, false},
		{403, `{"error":"the organization is read-only","code":"organization_read_only"}`, false, true},
		{429, `{"error":"rate limit exceeded"}`, false, false},
		{403, `{"error":"forbidden","reason":"permission_denied"}`, false, false},
	} {
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		client.retry = RetryPolicy{}
		_, err := client.Storage.CreateCredential(context.Background(), "b", CreateCredentialRequest{Access: CredentialReadWrite})
		if err == nil {
			t.Fatalf("%s: no error", tc.body)
		}
		wrapped := fmt.Errorf("create key: %w", err)
		if IsCredentialLimit(wrapped) != tc.limit || IsOrganizationReadOnly(wrapped) != tc.ro {
			t.Errorf("%s: limit=%v readOnly=%v", tc.body, IsCredentialLimit(wrapped), IsOrganizationReadOnly(wrapped))
		}
	}
	if IsCredentialLimit(nil) || IsOrganizationReadOnly(fmt.Errorf("plain")) {
		t.Error("a non-API error is classified")
	}
}

// The secret must not appear when the value is printed or logged.
func TestIssuedCredentialNeverPrintsTheSecret(t *testing.T) {
	cred := IssuedCredential{
		StorageCredential: StorageCredential{AccessKeyID: "AKEXAMPLE", Container: "b", Access: CredentialReadWrite},
		SecretAccessKey:   fakeSecret,
		S3Bucket:          "nemau-dev-00000000000000000000000000000000",
		Region:            "fr-occitanie-1",
	}
	for name, out := range map[string]string{
		"%v":  fmt.Sprintf("%v", cred),
		"%+v": fmt.Sprintf("%+v", cred),
		"%s":  fmt.Sprintf("%s", cred),
		"%#v": fmt.Sprintf("%#v", cred),
		"ptr": fmt.Sprintf("%v", &cred),
	} {
		if strings.Contains(out, fakeSecret) {
			t.Errorf("%s leaks the secret: %s", name, out)
		}
		if !strings.Contains(out, "AKEXAMPLE") || !strings.Contains(out, "nemau-dev-00000000000000000000000000000000") {
			t.Errorf("%s dropped the access key id or the bucket: %s", name, out)
		}
	}
	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("issued", "credential", cred)
	if strings.Contains(buf.String(), fakeSecret) {
		t.Errorf("slog leaks the secret: %s", buf.String())
	}
	// The caller can still read the field explicitly.
	if cred.SecretAccessKey != fakeSecret {
		t.Error("the secret field was altered")
	}
}

func TestCredentialLimitCarriesTheCapWhenTheServerSaysIt(t *testing.T) {
	for _, tc := range []struct {
		body      string
		wantLimit int
		wantScope string
	}{
		{`{"error":"limit reached","code":"credential_limit","limit":10,"scope":"bucket"}`, 10, "bucket"},
		{`{"error":"limit reached","code":"credential_limit","limit":50,"scope":"project"}`, 50, "project"},
		{`{"error":"limit reached","code":"credential_limit"}`, 0, ""},
	} {
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(tc.body))
		}))
		client.retry = RetryPolicy{}
		_, err := client.Storage.CreateCredential(context.Background(), "b", CreateCredentialRequest{Access: CredentialReadWrite})
		if !IsCredentialLimit(err) {
			t.Fatalf("%s: not a credential limit", tc.body)
		}
		limit, scope, ok := CredentialLimit(fmt.Errorf("wrapped: %w", err))
		if !ok || limit != tc.wantLimit || scope != tc.wantScope {
			t.Errorf("%s: CredentialLimit = %d, %q, %v", tc.body, limit, scope, ok)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Limit != tc.wantLimit || apiErr.Scope != tc.wantScope {
			t.Errorf("%s: APIError.Limit/Scope = %+v", tc.body, apiErr)
		}
	}
	if _, _, ok := CredentialLimit(errors.New("plain")); ok {
		t.Error("a plain error was classified as a credential limit")
	}
	if _, _, ok := CredentialLimit(&APIError{StatusCode: 429, Code: "other", Limit: 3}); ok {
		t.Error("another code was classified as a credential limit")
	}
}

// GetContainer carries the bucket name S3 tools need, next to the public name.
func TestGetContainerDecodesTheS3Bucket(t *testing.T) {
	c := &capture{resp: `{"containers":[{"id":"c-1","name":"photos","bytes":0,"count":0,"s3_bucket":"nemau-dev-11111111111111111111111111111111"}],"count":1}`}
	client, _ := newTestClient(t, c.handler())
	got, err := client.Storage.GetContainer(context.Background(), "photos")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "photos" || got.S3Bucket != "nemau-dev-11111111111111111111111111111111" {
		t.Errorf("container = %+v", got)
	}
}
