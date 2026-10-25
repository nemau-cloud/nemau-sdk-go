package nemau

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	testGroupID = "9b1e2f3a-4c5d-4e6f-8a7b-1c2d3e4f5a6b"
	testNetID   = "22222222-3333-4444-8555-666666666666"
	testPoolID  = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
	testInstID  = "c0ffee00-1111-4222-8333-444455556666"
)

// fixtureUserData is a cloud-config fixture, never a real secret.
const fixtureUserData = "#cloud-config\nruncmd:\n  - echo FIXTUREONLY\n"

func validGroupCreate() CreateInstanceGroupRequest {
	return CreateInstanceGroupRequest{
		Name: "web-pool", NetworkID: testNetID, SubnetID: testSubnetID,
		MinSize: 1, MaxSize: 4,
		LaunchTemplate: &LaunchTemplate{ImageID: "debian-12", FlavorID: "small", UserData: fixtureUserData},
	}
}

func TestCreateInstanceGroupBodyIsExact(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())

	desired := 2
	req := validGroupCreate()
	req.DesiredCapacity = &desired
	req.LoadBalancerPools = []GroupPool{{PoolID: testPoolID, ProtocolPort: 80}}
	req.ScalingPolicies = []ScalingPolicy{{Metric: MetricCPUPercent, Target: 60, WindowSeconds: 300}}
	req.HealthCheck = HealthCheckInstance
	if _, err := c.InstanceGroups.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/compute/instance-groups" {
		t.Errorf("request = %s %s", rec.method, rec.path)
	}
	if rec.ikey == "" {
		t.Error("an asynchronous write must carry an Idempotency-Key")
	}

	var body struct {
		Name            string `json:"name"`
		MinSize         *int   `json:"min_size"`
		MaxSize         *int   `json:"max_size"`
		DesiredCapacity *int   `json:"desired_capacity"`
		LaunchTemplate  struct {
			ImageID  string `json:"image_id"`
			FlavorID string `json:"flavor_id"`
			UserData string `json:"user_data"`
		} `json:"launch_template"`
		LoadBalancerPools []map[string]any `json:"load_balancer_pools"`
		ScalingPolicies   []map[string]any `json:"scaling_policies"`
		HealthCheck       string           `json:"health_check"`
	}
	if err := json.Unmarshal([]byte(rec.body), &body); err != nil {
		t.Fatal(err)
	}
	if body.MinSize == nil || *body.MinSize != 1 || body.MaxSize == nil || *body.MaxSize != 4 {
		t.Errorf("sizes = %s", rec.body)
	}
	if body.DesiredCapacity == nil || *body.DesiredCapacity != 2 {
		t.Errorf("desired_capacity = %s", rec.body)
	}
	if body.LaunchTemplate.UserData != fixtureUserData || body.LaunchTemplate.FlavorID != "small" {
		t.Errorf("launch_template = %s", rec.body)
	}
	if len(body.LoadBalancerPools) != 1 || body.LoadBalancerPools[0]["protocol_port"] != float64(80) {
		t.Errorf("pools = %s", rec.body)
	}
	if len(body.ScalingPolicies) != 1 || body.ScalingPolicies[0]["metric"] != "cpu_percent" {
		t.Errorf("policies = %s", rec.body)
	}
	if _, ok := body.ScalingPolicies[0]["scale_in_enabled"]; ok {
		t.Errorf("unset scale_in_enabled must be omitted: %s", rec.body)
	}
}

func TestCreateInstanceGroupMinZeroIsSent(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	req := validGroupCreate()
	req.MinSize = 0
	req.MaxSize = 0
	req.LaunchTemplate.UserData = ""
	if _, err := c.InstanceGroups.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	keys := bodyKeys(t, rec.body)
	if string(keys["min_size"]) != "0" || string(keys["max_size"]) != "0" {
		t.Errorf("zero bounds must be sent, got %s", rec.body)
	}
}

func TestCreateInstanceGroupRefusesBeforeSending(t *testing.T) {
	cases := map[string]func(*CreateInstanceGroupRequest){
		"name with capitals":   func(r *CreateInstanceGroupRequest) { r.Name = "Web" },
		"min above max":        func(r *CreateInstanceGroupRequest) { r.MinSize = 5 },
		"desired out of bound": func(r *CreateInstanceGroupRequest) { d := 9; r.DesiredCapacity = &d },
		"size above 50":        func(r *CreateInstanceGroupRequest) { r.MaxSize = 51 },
		"no launch template":   func(r *CreateInstanceGroupRequest) { r.LaunchTemplate = nil },
		"no image":             func(r *CreateInstanceGroupRequest) { r.LaunchTemplate.ImageID = "" },
		"partial sizing": func(r *CreateInstanceGroupRequest) {
			r.LaunchTemplate.FlavorID = ""
			r.LaunchTemplate.VCPUs = 2
		},
		"ram not multiple of 256": func(r *CreateInstanceGroupRequest) {
			r.LaunchTemplate.FlavorID = ""
			r.LaunchTemplate.VCPUs, r.LaunchTemplate.RAMMB, r.LaunchTemplate.DiskGB = 2, 1500, 20
		},
		"user data without header": func(r *CreateInstanceGroupRequest) { r.LaunchTemplate.UserData = "FIXTURESECRET" },
		"memory policy": func(r *CreateInstanceGroupRequest) {
			r.ScalingPolicies = []ScalingPolicy{{Metric: "memory_percent", Target: 60}}
		},
		"policy target out of range": func(r *CreateInstanceGroupRequest) {
			r.ScalingPolicies = []ScalingPolicy{{Metric: MetricCPUPercent, Target: 95}}
		},
		"pool port zero": func(r *CreateInstanceGroupRequest) {
			r.LoadBalancerPools = []GroupPool{{PoolID: testPoolID}}
		},
		"duplicate pool": func(r *CreateInstanceGroupRequest) {
			r.LoadBalancerPools = []GroupPool{{PoolID: testPoolID, ProtocolPort: 80}, {PoolID: testPoolID, ProtocolPort: 443}}
		},
		"load_balancer health check": func(r *CreateInstanceGroupRequest) { r.HealthCheck = "load_balancer" },
		"grace too short":            func(r *CreateInstanceGroupRequest) { g := 30; r.HealthCheckGraceSeconds = &g },
		"service level invented":     func(r *CreateInstanceGroupRequest) { r.LaunchTemplate.ServiceLevel = "gold" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			c, _ := newTestClient(t, rec.handler())
			req := validGroupCreate()
			mutate(&req)
			if _, err := c.InstanceGroups.Create(context.Background(), req); err == nil {
				t.Fatal("expected a refusal")
			}
			if rec.calls != 0 {
				t.Errorf("refused input reached the server")
			}
		})
	}
}

func TestUserDataIsNeverEchoedInErrors(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	req := validGroupCreate()
	req.LaunchTemplate.UserData = "FIXTURESECRETVALUE"
	_, err := c.InstanceGroups.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "FIXTURESECRETVALUE") {
		t.Errorf("error echoes the document: %v", err)
	}
}

func TestUpdateSendsEmptyListsToClear(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())

	empty := []string{}
	if _, err := c.InstanceGroups.Update(context.Background(), testGroupID, UpdateInstanceGroupRequest{SecurityGroups: &empty}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.method != http.MethodPatch || rec.body != `{"security_groups":[]}` {
		t.Errorf("clearing security groups sent %s %s", rec.method, rec.body)
	}

	noSchedules := []Schedule{}
	if _, err := c.InstanceGroups.Update(context.Background(), testGroupID, UpdateInstanceGroupRequest{Schedules: &noSchedules}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.body != `{"schedules":[]}` {
		t.Errorf("clearing schedules sent %s", rec.body)
	}
}

func TestUpdateRefusesEmptyAndBadSchedules(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.InstanceGroups.Update(context.Background(), testGroupID, UpdateInstanceGroupRequest{}); err == nil {
		t.Fatal("an empty update must be refused")
	}
	min, max := 5, 2
	bad := []Schedule{{Cron: "0 8 * * 1-5", MinSize: &min, MaxSize: &max}}
	if _, err := c.InstanceGroups.Update(context.Background(), testGroupID, UpdateInstanceGroupRequest{Schedules: &bad}); err == nil {
		t.Fatal("a schedule with min above max must be refused")
	}
	sixFields := []Schedule{{Cron: "0 0 8 * * 1", MinSize: &min}}
	if _, err := c.InstanceGroups.Update(context.Background(), testGroupID, UpdateInstanceGroupRequest{Schedules: &sixFields}); err == nil {
		t.Fatal("a 6-field cron must be refused")
	}
	if rec.calls != 0 {
		t.Errorf("refused update reached the server")
	}
}

func TestSetCapacityBody(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.InstanceGroups.SetCapacity(context.Background(), testGroupID, 3); err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodPut || rec.path != "/api/v1/compute/instance-groups/"+testGroupID+"/capacity" || rec.body != `{"desired_capacity":3}` {
		t.Errorf("capacity = %s %s %s", rec.method, rec.path, rec.body)
	}
	if _, err := c.InstanceGroups.SetCapacity(context.Background(), testGroupID, 51); err == nil {
		t.Error("capacity 51 must be refused")
	}
}

func TestSetMemberProtectionSendsFalseExplicitly(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.InstanceGroups.SetMemberProtection(context.Background(), testGroupID, testInstID, false); err != nil {
		t.Fatal(err)
	}
	wantPath := "/api/v1/compute/instance-groups/" + testGroupID + "/members/" + testInstID + "/protection"
	if rec.method != http.MethodPut || rec.path != wantPath || rec.body != `{"protected":false}` {
		t.Errorf("protection = %s %s %s", rec.method, rec.path, rec.body)
	}
}

func TestDeleteInstancesQuery(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.InstanceGroups.Delete(context.Background(), testGroupID, false); err != nil {
		t.Fatal(err)
	}
	if rec.query != "" {
		t.Errorf("default delete sent %q", rec.query)
	}
	if _, err := c.InstanceGroups.Delete(context.Background(), testGroupID, true); err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodDelete || rec.query != "delete_instances=true" {
		t.Errorf("delete = %s ?%s", rec.method, rec.query)
	}
}

func TestGetAndActivitiesDecode(t *testing.T) {
	rec := &recorder{status: http.StatusOK, resp: `{"instance_group":{"id":"` + testGroupID + `","name":"web-pool","status":"READY",
		"desired_capacity":2,"launch_template":{"image_id":"debian-12","vcpus":2,"ram_mb":2048,"disk_gb":20,"user_data_present":true},
		"schedules":[{"id":"s1","cron":"0 8 * * 1-5","min_size":2}],
		"members":[{"instance_id":"` + testInstID + `","status":"active","template_version":1,"joined_at":"2026-10-20T08:00:00Z","protected_from_scale_in":true}],
		"activities":[{"id":7,"type":"member_created","reason":"cpu","instance_id":"` + testInstID + `","created_at":"2026-10-20T08:00:00Z"}]}}`}
	c, _ := newTestClient(t, rec.handler())
	g, err := c.InstanceGroups.Get(context.Background(), testGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if !g.LaunchTemplate.UserDataPresent || len(g.Members) != 1 || !g.Members[0].ProtectedFromScaleIn || len(g.Activities) != 1 {
		t.Errorf("decoded = %+v", g)
	}
	if len(g.Schedules) != 1 || g.Schedules[0].MinSize == nil || *g.Schedules[0].MinSize != 2 {
		t.Errorf("schedules = %+v", g.Schedules)
	}

	rec.resp = `{"activities":[{"id":7,"type":"member_created","created_at":"2026-10-20T08:00:00Z"}],"count":1}`
	acts, err := c.InstanceGroups.Activities(context.Background(), testGroupID)
	if err != nil || len(acts) != 1 || rec.path != "/api/v1/compute/instance-groups/"+testGroupID+"/activities" {
		t.Errorf("activities = %+v at %s, %v", acts, rec.path, err)
	}
}

func TestRefreshBodyAndRanges(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	surge, unavail := 2, 25
	if _, err := c.InstanceGroups.Refresh(context.Background(), testGroupID, RefreshInstanceGroupRequest{MaxSurge: &surge, MaxUnavailablePct: &unavail}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/compute/instance-groups/"+testGroupID+"/refresh" ||
		rec.body != `{"max_unavailable_pct":25,"max_surge":2}` {
		t.Errorf("refresh = %s %s %s", rec.method, rec.path, rec.body)
	}

	if _, err := c.InstanceGroups.Refresh(context.Background(), testGroupID, RefreshInstanceGroupRequest{}); err != nil {
		t.Fatalf("Refresh with stored shares: %v", err)
	}
	if rec.body != `{}` {
		t.Errorf("empty refresh body = %s, want {} so the stored shares apply", rec.body)
	}

	calls := rec.calls
	bad := []RefreshInstanceGroupRequest{
		{MaxUnavailablePct: intPtr(51)},
		{MaxUnavailablePct: intPtr(-1)},
		{MaxSurge: intPtr(0)},
		{MaxSurge: intPtr(11)},
	}
	for _, req := range bad {
		if _, err := c.InstanceGroups.Refresh(context.Background(), testGroupID, req); err == nil {
			t.Errorf("refresh %+v must be refused", req)
		}
	}
	if rec.calls != calls {
		t.Error("refused refresh reached the server")
	}
}

func TestCancelRefreshPath(t *testing.T) {
	rec := &recorder{}
	c, _ := newTestClient(t, rec.handler())
	if _, err := c.InstanceGroups.CancelRefresh(context.Background(), testGroupID); err != nil {
		t.Fatalf("CancelRefresh: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/compute/instance-groups/"+testGroupID+"/refresh/cancel" {
		t.Errorf("cancel = %s %s", rec.method, rec.path)
	}
}

func intPtr(v int) *int { return &v }
