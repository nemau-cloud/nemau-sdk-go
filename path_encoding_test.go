package nemau

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// A path segment is escaped once, where the method builds it. The transport must
// not escape it again: "%" would become "%25" and the server would look up a
// different resource than the caller named.
func TestPathSegmentsReachTheServerEncodedExactlyOnce(t *testing.T) {
	for _, name := range []string{"plain", "with space", "100%", "a/b", "a+b", "café", "a?b#c", "%2F"} {
		var escaped, decoded string
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			escaped, decoded = r.URL.EscapedPath(), r.URL.Path
			_, _ = w.Write([]byte(`{"credentials":[]}`))
		}))
		if _, err := client.Storage.ListCredentials(context.Background(), name); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		wantEscaped := "/api/v1/storage/containers/" + url.PathEscape(name) + "/credentials"
		if escaped != wantEscaped {
			t.Errorf("%q: the server saw %q, want %q (encoded once)", name, escaped, wantEscaped)
		}
		wantDecoded, _ := url.PathUnescape(wantEscaped)
		if decoded != wantDecoded {
			t.Errorf("%q: decoded path %q, want %q", name, decoded, wantDecoded)
		}
	}
}

// The same holds under a base URL that has its own path prefix.
func TestPathEncodingHoldsUnderABasePathPrefix(t *testing.T) {
	var escaped string
	client, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"credentials":[]}`))
	}))
	c, err := NewClient(WithBaseURL(srv.URL+"/gateway/"), WithCredential(AccessKey{ID: "AKIA", Secret: "s3cr3t"}))
	if err != nil {
		t.Fatal(err)
	}
	_ = client
	if _, err := c.Storage.ListCredentials(context.Background(), "my bucket"); err != nil {
		t.Fatal(err)
	}
	if want := "/gateway/api/v1/storage/containers/my%20bucket/credentials"; escaped != want {
		t.Errorf("server saw %q, want %q", escaped, want)
	}
}
