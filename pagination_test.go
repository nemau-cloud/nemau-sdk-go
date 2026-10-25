package nemau

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestListObjectsLimitAndMarker(t *testing.T) {
	cap := &capture{resp: `{"objects":[],"next_marker":""}`}
	c, _ := newTestClient(t, cap.handler())
	ctx := context.Background()

	if _, err := c.Storage.ListObjects(ctx, "photos", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if cap.query != "" {
		t.Errorf("default listing sent %q; limit 0 must be omitted, the server refuses it", cap.query)
	}
	if _, err := c.Storage.ListObjects(ctx, "photos", "2026/", "bTI", 1000); err != nil {
		t.Fatal(err)
	}
	if cap.query != "limit=1000&marker=bTI&prefix=2026%2F" {
		t.Errorf("query = %q", cap.query)
	}
	cap.path = ""
	for _, limit := range []int{-1, 1001} {
		if _, err := c.Storage.ListObjects(ctx, "photos", "", "", limit); err == nil {
			t.Errorf("limit %d accepted", limit)
		}
	}
	if cap.path != "" {
		t.Error("an out-of-range limit reached the server")
	}
}

func TestIterateObjectsFollowsTheCursor(t *testing.T) {
	var markers []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		markers = append(markers, q.Get("marker"))
		if q.Get("limit") != "1000" || q.Get("prefix") != "a/" {
			t.Errorf("query = %v", q)
		}
		switch q.Get("marker") {
		case "":
			_, _ = w.Write([]byte(`{"objects":[{"name":"a/1"},{"name":"a/2"}],"next_marker":"cur-1"}`))
		case "cur-1":
			_, _ = w.Write([]byte(`{"objects":[{"name":"a/3"}],"next_marker":"cur-2"}`))
		default:
			_, _ = w.Write([]byte(`{"objects":[{"name":"a/4"}],"next_marker":""}`))
		}
	}))

	var names []string
	for o, err := range c.Storage.IterateObjects(context.Background(), "photos", "a/") {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, o.Name)
	}
	if len(names) != 4 || names[3] != "a/4" {
		t.Errorf("names = %v", names)
	}
	if len(markers) != 3 || markers[1] != "cur-1" || markers[2] != "cur-2" {
		t.Errorf("markers = %v, want the cursor echoed verbatim", markers)
	}
}

func TestIterateObjectsStopsEarlyAndOnAStuckCursor(t *testing.T) {
	var pages int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pages, 1)
		_, _ = w.Write([]byte(`{"objects":[{"name":"x"}],"next_marker":"same"}`))
	}))

	for range c.Storage.IterateObjects(context.Background(), "b", "") {
		break
	}
	if pages != 1 {
		t.Errorf("pages = %d after break, want 1", pages)
	}

	pages = 0
	var lastErr error
	for _, err := range c.Storage.IterateObjects(context.Background(), "b", "") {
		lastErr = err
	}
	if lastErr == nil || pages != 2 {
		t.Errorf("stuck cursor: err = %v after %d pages, want an error after 2", lastErr, pages)
	}
}

func TestInstanceMetricsValidatesThePeriod(t *testing.T) {
	cap := &capture{resp: `{}`}
	c, _ := newTestClient(t, cap.handler())
	for _, p := range []string{"", "1h", "6h", "24h", "7d"} {
		if _, err := c.Compute.InstanceMetrics(context.Background(), "i-1", p); err != nil {
			t.Errorf("period %q: %v", p, err)
		}
	}
	cap.path = ""
	for _, p := range []string{"2h", "30d", "1H", "1h;x"} {
		if _, err := c.Compute.InstanceMetrics(context.Background(), "i-1", p); err == nil {
			t.Errorf("period %q accepted", p)
		}
	}
	if cap.path != "" {
		t.Error("an invalid period reached the server")
	}
}
