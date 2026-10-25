package nemau

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testDBID     = "3f2c1d7e-8a4b-4c5d-9e6f-0a1b2c3d4e5f"
	testSubnetID = "11111111-2222-4333-8444-555555555555"
	testSGID     = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	// testPassword is a fixture: it is never a real credential.
	testPassword = "FixturePasswordNotReal0123456789ab"
)

// recorder captures what the handler received, and answers with status and body.
type recorder struct {
	mu     sync.Mutex
	calls  int
	method string
	path   string
	query  string
	ikey   string
	body   string
	status int
	resp   string
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls++
		r.method, r.path, r.query = req.Method, req.URL.Path, req.URL.RawQuery
		r.ikey = req.Header.Get(IdempotencyKeyHeader)
		b, _ := io.ReadAll(req.Body)
		r.body = string(b)
		status := r.status
		if status == 0 {
			status = http.StatusAccepted
		}
		body := r.resp
		if body == "" {
			body = `{"task_id":"task-1","status":"pending"}`
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// bodyKeys decodes a JSON object and returns its top-level keys.
func bodyKeys(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v (%q)", err, raw)
	}
	return m
}

func TestCreateDatabaseBodyAndOneTimePassword(t *testing.T) {
	rec := &recorder{resp: `{"task_id":"task-9","database_id":"","username":"appadmin","password":"` + testPassword + `"}`}
	c, _ := newTestClient(t, rec.handler())

	retention := 7
	creds, err := c.Databases.Create(context.Background(), CreateDatabaseRequest{
		Name: "orders-db", Version: "16", Flavor: "db-small", StorageGB: 20,
		SubnetID: testSubnetID, SecurityGroupIDs: []string{testSGID},
		AdminUsername: "appadmin", BackupRetentionDays: &retention,
		MaintenanceWindow: &DatabaseMaintenanceWindow{Weekday: 0, Hour: 3},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/databases" {
		t.Errorf("request = %s %s", rec.method, rec.path)
	}
	if rec.ikey != "" {
		t.Errorf("Idempotency-Key sent on a password-issuing route: %q", rec.ikey)
	}
	keys := bodyKeys(t, rec.body)
	for _, k := range []string{"name", "version", "flavor", "storage_gb", "subnet_id", "security_group_ids", "admin_username", "backup_retention_days", "maintenance_window"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("body misses %q: %s", k, rec.body)
		}
	}
	if _, ok := keys["description"]; ok {
		t.Errorf("empty description must be omitted: %s", rec.body)
	}
	if _, ok := keys["password"]; ok {
		t.Errorf("the client must never send a password: %s", rec.body)
	}
	var window map[string]int
	if err := json.Unmarshal(keys["maintenance_window"], &window); err != nil || window["weekday"] != 0 || window["hour"] != 3 {
		t.Errorf("maintenance_window = %s, want weekday 0 hour 3", keys["maintenance_window"])
	}
	if creds.TaskID != "task-9" || creds.Username != "appadmin" || creds.Password != testPassword {
		t.Errorf("credentials = %+v", creds)
	}
}

func TestSecretIsNeverPrintedByCredentials(t *testing.T) {
	creds := DatabaseCredentials{TaskID: "t", Username: "appadmin", Password: testPassword}
	for name, got := range map[string]string{
		"String":    creds.String(),
		"%v":        fmt.Sprintf("%v", creds),
		"%+v":       fmt.Sprintf("%+v", creds),
		"%#v":       fmt.Sprintf("%#v", creds),
		"%v-ptr":    fmt.Sprintf("%v", &creds),
		"MarshalJS": mustMarshal(t, creds),
	} {
		if strings.Contains(got, testPassword) {
			t.Errorf("%s leaks the password: %s", name, got)
		}
		if !strings.Contains(got, redactedSecret) {
			t.Errorf("%s does not show the redaction: %s", name, got)
		}
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestPasswordNeverAppearsInErrors(t *testing.T) {
	// A 202 that carries no password is an error that must not echo the body.
	rec := &recorder{resp: `{"task_id":"task-9","username":"appadmin","password":""}`}
	c, _ := newTestClient(t, rec.handler())
	_, err := c.Databases.Create(context.Background(), validCreate())
	if err == nil {
		t.Fatal("expected an error for a response without a password")
	}
	if strings.Contains(err.Error(), "task-9") || strings.Contains(err.Error(), "appadmin") {
		t.Errorf("error echoes the response: %v", err)
	}
}

func TestPasswordIssuingWritesAreNotRetried(t *testing.T) {
	rec := &recorder{status: http.StatusServiceUnavailable, resp: `{"error":"busy"}`}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.Databases.Create(context.Background(), validCreate()); err == nil {
		t.Fatal("expected the 503 to surface")
	}
	if rec.calls != 1 {
		t.Errorf("a password-issuing write was sent %d times, want 1", rec.calls)
	}
}

func TestRotatePasswordAndRestoreRoutes(t *testing.T) {
	rec := &recorder{resp: `{"task_id":"task-2","database_id":"` + testDBID + `","username":"appadmin","password":"` + testPassword + `"}`}
	c, _ := newTestClient(t, rec.handler())

	creds, err := c.Databases.RotatePassword(context.Background(), testDBID)
	if err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/databases/"+testDBID+"/rotate-password" {
		t.Errorf("rotate request = %s %s", rec.method, rec.path)
	}
	if creds.DatabaseID != testDBID {
		t.Errorf("DatabaseID = %q", creds.DatabaseID)
	}

	target := time.Date(2026, 10, 20, 9, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	_, err = c.Databases.Restore(context.Background(), testDBID, RestoreDatabaseRequest{
		Name: "orders-restored", TargetTime: target, Flavor: "db-small", StorageGB: 20,
		SubnetID: testSubnetID, AdminUsername: "appadmin",
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rec.path != "/api/v1/databases/"+testDBID+"/restore" {
		t.Errorf("restore path = %s", rec.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rec.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["target_time"] != "2026-10-20T07:30:00Z" {
		t.Errorf("target_time = %v, want UTC RFC 3339", body["target_time"])
	}
	if rec.ikey != "" {
		t.Errorf("restore sent an Idempotency-Key")
	}
}

func TestRestoreRefusesFutureTarget(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	_, err := c.Databases.Restore(context.Background(), testDBID, RestoreDatabaseRequest{
		Name: "orders-restored", TargetTime: time.Now().Add(time.Hour), Flavor: "db-small", StorageGB: 20,
		SubnetID: testSubnetID, AdminUsername: "appadmin",
	})
	if err == nil || rec.calls != 0 {
		t.Fatalf("future target: err=%v calls=%d, want refusal before any request", err, rec.calls)
	}
}

func TestCreateRefusesBadInputBeforeSending(t *testing.T) {
	cases := map[string]func(*CreateDatabaseRequest){
		"name uppercase":         func(r *CreateDatabaseRequest) { r.Name = "Orders" },
		"storage too small":      func(r *CreateDatabaseRequest) { r.StorageGB = 9 },
		"storage too large":      func(r *CreateDatabaseRequest) { r.StorageGB = 2001 },
		"reserved admin":         func(r *CreateDatabaseRequest) { r.AdminUsername = "postgres" },
		"pg_ admin":              func(r *CreateDatabaseRequest) { r.AdminUsername = "pg_admin" },
		"nemau admin":            func(r *CreateDatabaseRequest) { r.AdminUsername = "nemau_x" },
		"short admin":            func(r *CreateDatabaseRequest) { r.AdminUsername = "ab" },
		"subnet not uuid":        func(r *CreateDatabaseRequest) { r.SubnetID = "subnet-1" },
		"too many groups":        func(r *CreateDatabaseRequest) { r.SecurityGroupIDs = make([]string, 6) },
		"retention too high":     func(r *CreateDatabaseRequest) { v := 36; r.BackupRetentionDays = &v },
		"window hour 24":         func(r *CreateDatabaseRequest) { r.MaintenanceWindow = &DatabaseMaintenanceWindow{Weekday: 1, Hour: 24} },
		"control in description": func(r *CreateDatabaseRequest) { r.Description = "a\nb" },
		"no version":             func(r *CreateDatabaseRequest) { r.Version = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			c, _ := newTestClient(t, rec.handler())
			req := validCreate()
			mutate(&req)
			if _, err := c.Databases.Create(context.Background(), req); err == nil {
				t.Fatal("expected a refusal")
			}
			if rec.calls != 0 {
				t.Errorf("refused input reached the server")
			}
		})
	}
}

func TestUpdateRefusesEmptyAndSendsNamedFields(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.Databases.Update(context.Background(), testDBID, UpdateDatabaseRequest{}); err == nil || rec.calls != 0 {
		t.Fatal("an empty update must be refused before sending")
	}
	empty := ""
	if _, err := c.Databases.Update(context.Background(), testDBID, UpdateDatabaseRequest{Description: &empty}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.method != http.MethodPatch || rec.body != `{"description":""}` {
		t.Errorf("update = %s %s", rec.method, rec.body)
	}
}

func TestResizeRules(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.Databases.Resize(context.Background(), testDBID, ResizeDatabaseRequest{}); err == nil {
		t.Fatal("a resize with neither flavor nor storage must be refused")
	}
	if _, err := c.Databases.Resize(context.Background(), testDBID, ResizeDatabaseRequest{ApplyAt: "later", Flavor: "db-large"}); err == nil {
		t.Fatal("an unknown apply_at must be refused")
	}
	if rec.calls != 0 {
		t.Fatalf("refused resize reached the server")
	}
	gb := 50
	if _, err := c.Databases.Resize(context.Background(), testDBID, ResizeDatabaseRequest{StorageGB: &gb, ApplyAt: ApplyNow}); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if rec.path != "/api/v1/databases/"+testDBID+"/resize" || rec.body != `{"storage_gb":50,"apply_at":"now"}` {
		t.Errorf("resize = %s %s", rec.path, rec.body)
	}
}

func TestDeleteQueryCarriesPurgeOnlyWhenAsked(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.Databases.Delete(context.Background(), testDBID, false); err != nil {
		t.Fatal(err)
	}
	if rec.query != "" {
		t.Errorf("default delete sent query %q", rec.query)
	}
	if _, err := c.Databases.Delete(context.Background(), testDBID, true); err != nil {
		t.Fatal(err)
	}
	if rec.query != "purge_backups=true" || rec.method != http.MethodDelete {
		t.Errorf("purge delete = %s ?%s", rec.method, rec.query)
	}
}

func TestGetAndListDecodeDatabases(t *testing.T) {
	rec := &recorder{resp: `{"id":"` + testDBID + `","name":"orders-db","status":"READY","port":5432,"address":"10.0.0.5",
		"security_group_ids":["` + testSGID + `"],"maintenance_window":{"weekday":6,"hour":23},"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:00Z"}`}
	c, _ := newTestClient(t, rec.handler())
	rec.status = http.StatusOK

	db, err := c.Databases.Get(context.Background(), testDBID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/databases/"+testDBID {
		t.Errorf("get = %s %s", rec.method, rec.path)
	}
	if db.Port != 5432 || db.MaintenanceWindow == nil || db.MaintenanceWindow.Weekday != 6 || len(db.SecurityGroupIDs) != 1 {
		t.Errorf("decoded = %+v", db)
	}

	rec.resp = `{"databases":[{"id":"` + testDBID + `"}],"count":1}`
	list, err := c.Databases.List(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != testDBID {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestCACertificateReadsPEM(t *testing.T) {
	rec := &recorder{status: http.StatusOK, resp: `{"ca_certificate_pem":"-----BEGIN CERTIFICATE-----\nFIXTURE\n-----END CERTIFICATE-----\n"}`}
	c, _ := newTestClient(t, rec.handler())
	pem, err := c.Databases.CACertificate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rec.path != "/api/v1/databases/ca-certificate" || !strings.HasPrefix(pem, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("CA = %q at %s", pem, rec.path)
	}
}

func validCreate() CreateDatabaseRequest {
	return CreateDatabaseRequest{
		Name: "orders-db", Version: "16", Flavor: "db-small", StorageGB: 20,
		SubnetID: testSubnetID, AdminUsername: "appadmin",
	}
}

// decodeDatabaseStrict decodes a database view the way the golden instance
// group test does: an unknown key at any depth is an error, so a JSON tag that
// drifts from the API fails here instead of decoding to a silent zero value.
func decodeDatabaseStrict(t *testing.T, body string) Database {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var db Database
	if err := dec.Decode(&db); err != nil {
		t.Fatalf("database view does not decode strictly: %v", err)
	}
	return db
}

func TestDatabaseViewDecodesResizeAndRestorableWindow(t *testing.T) {
	const pendingBody = `{"id":"` + testDBID + `","name":"orders-db","status":"active",
		"description":"orders","pending_resize":{"flavor":"db-large","storage_gb":100,"apply_at":"2026-10-25T12:00:00Z"},
		"earliest_restorable_time":"2026-10-20T00:00:00Z","latest_restorable_time":"2026-10-25T11:55:00Z",
		"restorable_until":"2026-11-24T00:00:00Z"}`
	const idleBody = `{"id":"` + testDBID + `","name":"orders-db","status":"building","pending_resize":null}`

	// The strict decode checks the fixture against the struct first.
	strict := decodeDatabaseStrict(t, pendingBody)
	if strict.PendingResize == nil || !strict.PendingResize.ApplyAt.Equal(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("strict decode: pending resize = %+v", strict.PendingResize)
	}

	rec := &recorder{status: http.StatusOK, resp: pendingBody}
	c, _ := newTestClient(t, rec.handler())

	db, err := c.Databases.Get(context.Background(), testDBID)
	if err != nil {
		t.Fatal(err)
	}
	if db.Status != "active" || db.Description != "orders" || db.PendingResize == nil {
		t.Fatalf("decoded = %+v", db)
	}
	p := db.PendingResize
	if p.Flavor != "db-large" || p.StorageGB != 100 {
		t.Errorf("pending resize = %+v", p)
	}
	if want := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC); !p.ApplyAt.Equal(want) {
		t.Errorf("apply_at = %v, want %v", p.ApplyAt, want)
	}
	if db.EarliestRestorableTime == nil || db.LatestRestorableTime == nil || db.RestorableUntil == nil {
		t.Errorf("restorable window = %v / %v / %v", db.EarliestRestorableTime, db.LatestRestorableTime, db.RestorableUntil)
	}

	// No resize pending and no backup yet: every optional time is nil.
	strict = decodeDatabaseStrict(t, idleBody)
	if strict.PendingResize != nil || strict.EarliestRestorableTime != nil ||
		strict.LatestRestorableTime != nil || strict.RestorableUntil != nil {
		t.Errorf("idle strict decode = %+v", strict)
	}

	rec.resp = idleBody
	db, err = c.Databases.Get(context.Background(), testDBID)
	if err != nil {
		t.Fatal(err)
	}
	if db.PendingResize != nil || db.EarliestRestorableTime != nil ||
		db.LatestRestorableTime != nil || db.RestorableUntil != nil {
		t.Errorf("no pending resize and no backup must decode to nil, got %+v", db)
	}
}

func TestRestartQueuesATaskOnThePath(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.Databases.Restart(context.Background(), testDBID); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/databases/"+testDBID+"/restart" || rec.body != "" {
		t.Errorf("restart = %s %s %q", rec.method, rec.path, rec.body)
	}
	if _, err := c.Databases.Restart(context.Background(), " "); err == nil || rec.calls != 1 {
		t.Error("an empty id must be refused before sending")
	}
}

func TestFlavorsAndVersionsDecode(t *testing.T) {
	rec := &recorder{status: http.StatusOK, resp: `{"flavors":[{"name":"db-small","vcpus":1,"memory_mb":2048}]}`}
	c, _ := newTestClient(t, rec.handler())
	flavors, err := c.Databases.Flavors(context.Background())
	if err != nil {
		t.Fatalf("Flavors: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/database-flavors" {
		t.Errorf("flavors = %s %s", rec.method, rec.path)
	}
	if len(flavors) != 1 || flavors[0] != (DatabaseFlavor{Name: "db-small", VCPUs: 1, MemoryMB: 2048}) {
		t.Errorf("flavors = %+v", flavors)
	}

	rec.resp = `{"versions":["16","17"]}`
	versions, err := c.Databases.Versions(context.Background())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if rec.path != "/api/v1/database-versions" || len(versions) != 2 || versions[1] != "17" {
		t.Errorf("versions = %v at %s", versions, rec.path)
	}
}

func TestUpdateClearMaintenanceWindowSendsNull(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.Databases.Update(context.Background(), testDBID, UpdateDatabaseRequest{ClearMaintenanceWindow: true}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.body != `{"maintenance_window":null}` {
		t.Errorf("clear body = %s", rec.body)
	}

	win := DatabaseMaintenanceWindow{Weekday: 0, Hour: 3}
	if _, err := c.Databases.Update(context.Background(), testDBID, UpdateDatabaseRequest{MaintenanceWindow: &win}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.body != `{"maintenance_window":{"weekday":0,"hour":3}}` {
		t.Errorf("set body = %s", rec.body)
	}

	calls := rec.calls
	both := UpdateDatabaseRequest{MaintenanceWindow: &win, ClearMaintenanceWindow: true}
	if _, err := c.Databases.Update(context.Background(), testDBID, both); err == nil || rec.calls != calls {
		t.Error("setting and clearing the window together must be refused before sending")
	}
}
