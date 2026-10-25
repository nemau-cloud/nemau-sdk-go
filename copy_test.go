package nemau

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The source container is in the path; the key, which may contain slashes, is
// in the body, and empty fields are left out.
func TestCopyObjectRequestShape(t *testing.T) {
	var method, path string
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"container":"archive","key":"2026/a.jpg","size":42,"etag":"e",
			"version_id":"v2","last_modified":"2026-09-23T10:00:00Z",
			"retention":{"mode":"COMPLIANCE","retain_until":"2026-10-23T10:00:01Z"}}`))
	}))

	copied, err := c.Storage.CopyObject(context.Background(), CopyObjectRequest{
		Container: "photos", Key: "2026/a.jpg", DestinationContainer: "archive",
	})
	if err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if method != http.MethodPost || path != "/api/v1/storage/containers/photos/copy" {
		t.Errorf("request = %s %s", method, path)
	}
	want := map[string]any{"key": "2026/a.jpg", "destination_container": "archive"}
	if len(body) != len(want) {
		t.Errorf("body = %v, want exactly %v", body, want)
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("body[%s] = %v, want %v", key, body[key], value)
		}
	}
	if copied.Container != "archive" || copied.Key != "2026/a.jpg" || copied.Size != 42 || copied.VersionID != "v2" {
		t.Errorf("copied = %+v", copied)
	}
	if copied.Retention == nil || copied.Retention.Mode != RetentionCompliance ||
		!copied.Retention.RetainUntil.Equal(time.Date(2026, 10, 23, 10, 0, 1, 0, time.UTC)) {
		t.Errorf("retention = %+v", copied.Retention)
	}
}

func TestCopyObjectSendsReplacedMetadata(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"container":"photos","key":"a.jpg","size":1}`))
	}))

	copied, err := c.Storage.CopyObject(context.Background(), CopyObjectRequest{
		Container: "photos", Key: "a.jpg", ReplaceMetadata: true,
		ContentType: "image/jpeg", Metadata: map[string]string{"owner": "ops"},
	})
	if err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if body["replace_metadata"] != true || body["content_type"] != "image/jpeg" {
		t.Errorf("body = %v", body)
	}
	if meta, _ := body["metadata"].(map[string]any); meta["owner"] != "ops" {
		t.Errorf("metadata = %v", body["metadata"])
	}
	if copied.Retention != nil {
		t.Errorf("retention on an unlocked destination: %+v", copied.Retention)
	}
}

// Caught client-side, like an overlong presign expiry: the API would refuse it
// too, but only after a round trip.
func TestCopyObjectRejectsMetadataWithoutReplaceBeforeCallingTheAPI(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	for _, req := range []CopyObjectRequest{
		{Container: "photos", Key: "a.jpg", DestinationKey: "b.jpg", Metadata: map[string]string{"k": "v"}},
		{Container: "photos", Key: "a.jpg", DestinationKey: "b.jpg", ContentType: "text/plain"},
	} {
		if _, err := c.Storage.CopyObject(context.Background(), req); err == nil || !strings.Contains(err.Error(), "ReplaceMetadata") {
			t.Errorf("err = %v", err)
		}
	}
	if cap.method != "" {
		t.Error("no request should have been sent")
	}
}

// Over the server-side copy limit the API answers 413; that is final, so it is
// not retried.
func TestCopyObjectTooLargeIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"object exceeds the 5 GiB a server-side copy accepts in one request"}`))
	}))

	_, err := c.Storage.CopyObject(context.Background(), CopyObjectRequest{Container: "photos", Key: "big.iso", DestinationContainer: "archive"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("err = %v, want a 413 APIError", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}
