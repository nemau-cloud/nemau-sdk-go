package nemau

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// capture records the one request a service method makes, so a test can assert
// the exact URL the platform requires.
type capture struct {
	method string
	path   string
	query  string
	body   string
	resp   string
}

func (c *capture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.method, c.path, c.query = r.Method, r.URL.Path, r.URL.RawQuery
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			c.body = string(b)
		}
		body := c.resp
		if body == "" {
			body = `{"task_id":"t","status":"pending"}`
		}
		_, _ = w.Write([]byte(body))
	}
}

// A volume id can contain a slash, so it must go in ?id=. As a path segment it
// would add a segment, and the request would not match the route.
func TestVolumeOperationsCarryIDInQuery(t *testing.T) {
	const volid = "vol:104/disk-0"

	cases := []struct {
		name       string
		call       func(*Client) error
		wantMethod string
		wantPath   string
	}{
		{"delete", func(c *Client) error {
			_, err := c.Storage.DeleteVolume(context.Background(), volid)
			return err
		}, http.MethodDelete, "/api/v1/storage/volumes"},
		{"attach", func(c *Client) error {
			_, err := c.Storage.AttachVolume(context.Background(), volid, "inst-1")
			return err
		}, http.MethodPost, "/api/v1/storage/volumes/attach"},
		{"detach", func(c *Client) error {
			_, err := c.Storage.DetachVolume(context.Background(), volid, "inst-1")
			return err
		}, http.MethodPost, "/api/v1/storage/volumes/detach"},
		{"extend", func(c *Client) error {
			_, err := c.Storage.ExtendVolume(context.Background(), volid, 50)
			return err
		}, http.MethodPut, "/api/v1/storage/volumes/extend"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capture{}
			c, _ := newTestClient(t, cap.handler())
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if cap.method != tc.wantMethod || cap.path != tc.wantPath {
				t.Errorf("%s %s, want %s %s", cap.method, cap.path, tc.wantMethod, tc.wantPath)
			}
			if got := mustQueryID(t, cap.query); got != volid {
				t.Errorf("?id= carried %q, want %q", got, volid)
			}
		})
	}
}

// A record-set id is "canonical.name./TYPE", so it goes in ?id= for the same
// reason.
func TestRecordSetOperationsCarryIDInQuery(t *testing.T) {
	const rsID = "app.example.com./A"

	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	if _, err := c.DNS.DeleteRecordSet(context.Background(), "zone-1", rsID); err != nil {
		t.Fatalf("DeleteRecordSet: %v", err)
	}
	if cap.path != "/api/v1/dns/zones/zone-1/recordsets" {
		t.Errorf("path = %q", cap.path)
	}
	if got := mustQueryID(t, cap.query); got != rsID {
		t.Errorf("?id= carried %q, want %q", got, rsID)
	}
}

func mustQueryID(t *testing.T, raw string) string {
	t.Helper()
	for _, pair := range strings.Split(raw, "&") {
		if k, v, ok := strings.Cut(pair, "="); ok && k == "id" {
			decoded, err := url.QueryUnescape(v)
			if err != nil {
				t.Fatalf("decoding ?id=%q: %v", v, err)
			}
			return decoded
		}
	}
	t.Fatalf("no id in query %q", raw)
	return ""
}

func TestSetSecurityGroupsSendsEmptyArray(t *testing.T) {
	// An empty list removes all the groups, so nil must be sent as [] and not as
	// null or an absent key.
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	if _, err := c.Compute.SetSecurityGroups(context.Background(), "inst-1", nil); err != nil {
		t.Fatalf("SetSecurityGroups: %v", err)
	}
	if cap.method != http.MethodPut || cap.path != "/api/v1/compute/instances/inst-1/security-groups" {
		t.Errorf("%s %s", cap.method, cap.path)
	}
	if cap.body != `{"security_groups":[]}` {
		t.Errorf("body = %s", cap.body)
	}
}

func TestListFloatingIPsReadsPlatformKey(t *testing.T) {
	// The platform spells it "floatingips", with no separator.
	cap := &capture{resp: `{"floatingips":[{"id":"fip-1","address":"203.0.113.7","status":"ACTIVE"}],"count":1}`}
	c, _ := newTestClient(t, cap.handler())
	fips, err := c.Network.ListFloatingIPs(context.Background())
	if err != nil {
		t.Fatalf("ListFloatingIPs: %v", err)
	}
	if len(fips) != 1 || fips[0].Address != "203.0.113.7" {
		t.Fatalf("floating ips = %+v", fips)
	}
}

func TestGetNetworkSynthesises404(t *testing.T) {
	// There is no per-network read, so Get lists and filters. A missing network
	// must be reported as a 404, so that a caller can tell a deleted network
	// from a failed call.
	cap := &capture{resp: `{"networks":[{"id":"net-1"}]}`}
	c, _ := newTestClient(t, cap.handler())
	_, err := c.Network.GetNetwork(context.Background(), "net-absent")
	if !IsNotFound(err) {
		t.Fatalf("err = %v, want a 404", err)
	}
}

func TestUploadObjectSetsContentLength(t *testing.T) {
	// The API answers 411 without a Content-Length, and Go does not set one for
	// an arbitrary io.Reader.
	var gotLen int64
	var gotType, gotQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen, gotType, gotQuery = r.ContentLength, r.Header.Get("Content-Type"), r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"object_name":"a/b.txt","size":5}`))
	}))

	payload := "hello"
	got, err := c.Storage.UploadObject(context.Background(), "bucket", "a/b.txt", "text/plain",
		strings.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("UploadObject: %v", err)
	}
	if gotLen != 5 {
		t.Errorf("Content-Length = %d, want 5", gotLen)
	}
	if gotType != "text/plain" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if gotQuery != "key=a%2Fb.txt" {
		t.Errorf("query = %q, want the key percent-encoded", gotQuery)
	}
	if got.Size != 5 {
		t.Errorf("size = %d", got.Size)
	}
}

func TestKubernetesClusterIsNameAddressed(t *testing.T) {
	cap := &capture{resp: `{"id":"c1","name":"prod","status":{"phase":"Provisioned"}}`}
	c, _ := newTestClient(t, cap.handler())
	cluster, err := c.Kubernetes.GetCluster(context.Background(), "prod")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if cap.path != "/api/v1/kubernetes/clusters/prod" {
		t.Errorf("path = %q", cap.path)
	}
	if cluster.Status.Phase != "Provisioned" {
		t.Errorf("phase = %q", cluster.Status.Phase)
	}
}

func TestPresignRequestShape(t *testing.T) {
	cap := &capture{resp: `{"url":"https://s3.example.test/b/k?X-Amz-Signature=abc","method":"GET","expires_at":"2030-01-01T00:00:00Z"}`}
	c, _ := newTestClient(t, cap.handler())

	presigned, err := c.Storage.PresignGet(context.Background(), "bucket", "a/b.txt", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/v1/storage/containers/bucket/presign" {
		t.Errorf("%s %s", cap.method, cap.path)
	}
	if !strings.Contains(cap.body, `"expires_in":300`) || !strings.Contains(cap.body, `"method":"GET"`) {
		t.Errorf("body = %s", cap.body)
	}
	if presigned.URL == "" {
		t.Error("empty presigned URL")
	}
}

func TestPresignRejectsAnOverlongTTLBeforeCallingTheAPI(t *testing.T) {
	// Checked client-side: the message names the cap and no request is sent.
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	_, err := c.Storage.PresignGet(context.Background(), "bucket", "k", 48*time.Hour)
	if err == nil || !strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("err = %v", err)
	}
	if cap.method != "" {
		t.Error("no request should have been sent")
	}
}

func TestFetchPresignedSendsNoCredential(t *testing.T) {
	// A presigned URL is already authorized. Sending the access key would expose
	// it to the object store's host, which is not the API.
	var sawKey, sawAuth string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey, sawAuth = r.Header.Get("X-Access-Key-Id"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte("content"))
	}))
	defer store.Close()

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	body, _, err := c.FetchPresigned(context.Background(), &PresignedObject{URL: store.URL + "/b/k"})
	if err != nil {
		t.Fatalf("FetchPresigned: %v", err)
	}
	defer body.Close()

	if sawKey != "" || sawAuth != "" {
		t.Errorf("credential sent to the object store: key=%q auth=%q", sawKey, sawAuth)
	}
	raw, _ := io.ReadAll(body)
	if string(raw) != "content" {
		t.Errorf("corps = %q", raw)
	}
}

func TestFetchPresignedErrorHidesTheSignature(t *testing.T) {
	// An error string ends up in logs, and the signature in a presigned URL is
	// a valid credential.
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error>expired</Error>"))
	}))
	defer store.Close()

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	_, _, err := c.FetchPresigned(context.Background(),
		&PresignedObject{URL: store.URL + "/b/k?X-Amz-Signature=deadbeefsecret"})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "deadbeefsecret") {
		t.Errorf("the signature leaks into the error: %v", err)
	}
}

func TestRestoreSnapshotPostsWithoutBody(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	ref, err := c.Compute.RestoreSnapshot(context.Background(), "snap-1")
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if ref.TaskID != "t" {
		t.Errorf("task id = %q", ref.TaskID)
	}
	if cap.method != http.MethodPost || cap.path != "/api/v1/compute/snapshots/snap-1/restore" {
		t.Errorf("%s %s", cap.method, cap.path)
	}
	if cap.body != "" {
		t.Errorf("body = %q, want none", cap.body)
	}
}

func TestListSnapshotsDecodesStorageAndCost(t *testing.T) {
	// Size and cost are absent until an object-store snapshot is active, and
	// always absent on hypervisor snapshots: they decode as nil, not as zero.
	cap := &capture{resp: `{"snapshots":[
		{"id":"s1","name":"a","instance_id":"i1","status":"active","storage":"object_store",
		 "size_bytes":2147483648,"source_disk_gb":20,"monthly_cost":0.1,"currency":"eur","restorable":true},
		{"id":"s2","name":"b","instance_id":"i1","status":"active","storage":"hypervisor","restorable":false}
	],"count":2}`}
	c, _ := newTestClient(t, cap.handler())
	items, err := c.Compute.ListSnapshots(context.Background(), "i1")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if cap.query != "instance_id=i1" {
		t.Errorf("query = %q", cap.query)
	}
	if len(items) != 2 {
		t.Fatalf("got %d snapshots", len(items))
	}
	s1, s2 := items[0], items[1]
	if s1.Storage != "object_store" || !s1.Restorable || s1.SizeBytes == nil || *s1.SizeBytes != 2147483648 ||
		s1.SourceDiskGB == nil || *s1.SourceDiskGB != 20 || s1.MonthlyCost == nil || *s1.MonthlyCost != 0.1 || s1.Currency != "eur" {
		t.Errorf("object-store snapshot decoded as %+v", s1)
	}
	if s2.Storage != "hypervisor" || s2.Restorable || s2.SizeBytes != nil || s2.MonthlyCost != nil {
		t.Errorf("hypervisor snapshot decoded as %+v", s2)
	}
}

// An unknown retention mode is refused before any request, and the message
// names the two valid ones.
func TestCreateContainerRejectsAnUnknownRetentionMode(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL.Path)
	}))
	_, err := c.Storage.CreateContainerWithOptions(context.Background(), CreateContainerRequest{
		Name: "backups", ObjectLock: true, RetentionMode: "FOREVER", RetentionDays: 30,
	})
	if err == nil || !strings.Contains(err.Error(), `retention_mode must be "GOVERNANCE" or "COMPLIANCE"`) {
		t.Fatalf("err = %v", err)
	}
}

// A retention above the server's cap is refused before any request.
func TestCreateContainerRejectsRetentionAboveTheCap(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL.Path)
	}))
	_, err := c.Storage.CreateContainerWithOptions(context.Background(), CreateContainerRequest{
		Name: "backups", ObjectLock: true, RetentionMode: RetentionCompliance, RetentionDays: MaxRetentionDays + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "retention_days must not exceed 2555") {
		t.Fatalf("err = %v", err)
	}
}
