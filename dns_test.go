package nemau

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// testZoneID is a zone's public id, a UUID, not its name.
const testZoneID = "3f1c9a2e-7b4d-4e8a-9c61-2d5f0b8e4a17"

// A new zone is pending until its owner proves control of the domain; the
// record to publish comes back on the zone itself.
func TestPendingZoneCarriesItsVerificationRecord(t *testing.T) {
	cap := &capture{resp: `{"zones":[
		{"id":"3f1c9a2e-7b4d-4e8a-9c61-2d5f0b8e4a17","name":"example.com.","status":"PENDING_VERIFICATION",
		 "verification":{"name":"_nemau-challenge.example.com.","type":"TXT","value":"nemau-verification=abc123"}},
		{"id":"8a2b6c4d-1e3f-4a5b-8c7d-9e0f1a2b3c4d","name":"example.org.","status":"ACTIVE"}
	],"count":2}`}
	c, _ := newTestClient(t, cap.handler())

	zone, err := c.DNS.GetZone(context.Background(), testZoneID)
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if zone.Status != DNSZoneStatusPendingVerification {
		t.Errorf("status = %q", zone.Status)
	}
	want := DNSZoneVerification{Name: "_nemau-challenge.example.com.", Type: "TXT", Value: "nemau-verification=abc123"}
	if zone.Verification == nil || *zone.Verification != want {
		t.Fatalf("verification = %+v, want %+v", zone.Verification, want)
	}

	active, err := c.DNS.GetZone(context.Background(), "8a2b6c4d-1e3f-4a5b-8c7d-9e0f1a2b3c4d")
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if active.Status != DNSZoneStatusActive || active.Verification != nil {
		t.Errorf("active zone = %+v, want no verification", active)
	}
}

func TestVerifyZonePostsToTheZone(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())

	ref, err := c.DNS.VerifyZone(context.Background(), testZoneID)
	if err != nil {
		t.Fatalf("VerifyZone: %v", err)
	}
	if ref.TaskID != "t" {
		t.Errorf("task id = %q", ref.TaskID)
	}
	if want := "/api/v1/dns/zones/" + testZoneID + "/verify"; cap.method != http.MethodPost || cap.path != want {
		t.Errorf("%s %s, want POST %s", cap.method, cap.path, want)
	}
	if cap.body != "" {
		t.Errorf("body = %q, want none", cap.body)
	}
}

// verifySequence serves the three calls VerifyZoneAndWait makes: the verify
// write, its task, then the zone list.
type verifySequence struct {
	terminal string
	listed   int32
}

func (s *verifySequence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/verify"):
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-1","status":"pending"}`))
	case strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
		body := `{"id":"task-1","taskType":"dns:verify_zone","status":"` + s.terminal + `","progress":100`
		if s.terminal == TaskStatusFailed {
			body += `,"error":"TXT record _nemau-challenge.example.com. not found"`
		} else {
			body += `,"result":{"zone_id":"` + testZoneID + `","status":"ACTIVE"}`
		}
		_, _ = w.Write([]byte(body + `}`))
	case r.URL.Path == "/api/v1/dns/zones":
		atomic.AddInt32(&s.listed, 1)
		_, _ = w.Write([]byte(`{"zones":[{"id":"` + testZoneID + `","name":"example.com.","status":"ACTIVE"}],"count":1}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestVerifyZoneAndWaitReturnsTheActiveZone(t *testing.T) {
	seq := &verifySequence{terminal: TaskStatusCompleted}
	c, _ := newTestClient(t, seq)

	zone, err := c.DNS.VerifyZoneAndWait(context.Background(), testZoneID)
	if err != nil {
		t.Fatalf("VerifyZoneAndWait: %v", err)
	}
	if zone.Status != DNSZoneStatusActive || zone.Verification != nil {
		t.Errorf("zone = %+v, want active without verification", zone)
	}
}

// A record that is absent or wrong fails the task; the caller gets the task's
// message and no zone read.
func TestVerifyZoneAndWaitReportsAMissingRecord(t *testing.T) {
	seq := &verifySequence{terminal: TaskStatusFailed}
	c, _ := newTestClient(t, seq)

	_, err := c.DNS.VerifyZoneAndWait(context.Background(), testZoneID)
	var failed *TaskFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *TaskFailedError", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error lost the task's message: %v", err)
	}
	if seq.listed != 0 {
		t.Errorf("zone listed %d times after a failed verification", seq.listed)
	}
}

func TestDNSRefusalsAreRecognizableByReason(t *testing.T) {
	cases := []struct {
		status int
		reason string
		call   func(*Client) error
	}{
		{http.StatusBadRequest, ReasonDNSZoneReserved, func(c *Client) error {
			_, err := c.DNS.CreateZone(context.Background(), CreateZoneRequest{Name: "x.nemau-cloud.fr.", Email: "a@b.c"})
			return err
		}},
		{http.StatusConflict, ReasonDNSZoneExists, func(c *Client) error {
			_, err := c.DNS.CreateZone(context.Background(), CreateZoneRequest{Name: "example.com.", Email: "a@b.c"})
			return err
		}},
		{http.StatusConflict, ReasonDNSZoneAlreadyVerified, func(c *Client) error {
			_, err := c.DNS.VerifyZone(context.Background(), testZoneID)
			return err
		}},
		{http.StatusConflict, ReasonDNSZonePendingVerification, func(c *Client) error {
			_, err := c.DNS.CreateRecordSet(context.Background(), testZoneID, CreateRecordSetRequest{Name: "www", Type: "A", Records: []string{"203.0.113.7"}})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"refused","reason":"` + tc.reason + `"}`))
			}))
			err := tc.call(c)
			if !HasReason(err, tc.reason) {
				t.Fatalf("HasReason(%v, %q) = false", err, tc.reason)
			}
			if HasReason(err, "some_other_reason") {
				t.Errorf("HasReason matched an unrelated reason")
			}
		})
	}
	if HasReason(nil, ReasonDNSZoneExists) || HasReason(&TaskFailedError{}, ReasonDNSZoneExists) {
		t.Error("HasReason matched something that is not an APIError")
	}
	if HasReason(&APIError{StatusCode: http.StatusConflict}, "") {
		t.Error("HasReason matched an empty reason")
	}
}

// The creation task names the zone by its UUID, in resourceId or in the result;
// the zone read back is the pending one.
func TestCreateZoneAndWaitReturnsThePendingZoneByID(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"task_id":"task-1","status":"pending"}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
			_, _ = w.Write([]byte(`{"id":"task-1","taskType":"dns:create_zone","status":"completed","progress":100,` +
				`"result":{"zone_id":"` + testZoneID + `","status":"PENDING_VERIFICATION"}}`))
		default:
			_, _ = w.Write([]byte(`{"zones":[{"id":"` + testZoneID + `","name":"example.com.","status":"PENDING_VERIFICATION",` +
				`"verification":{"name":"_nemau-challenge.example.com.","type":"TXT","value":"nemau-verification=abc123"}}],"count":1}`))
		}
	}))

	zone, err := c.DNS.CreateZoneAndWait(context.Background(), CreateZoneRequest{Name: "example.com.", Email: "dns@example.com"})
	if err != nil {
		t.Fatalf("CreateZoneAndWait: %v", err)
	}
	if zone.ID != testZoneID || zone.Status != DNSZoneStatusPendingVerification || zone.Verification == nil {
		t.Errorf("zone = %+v", zone)
	}
}
