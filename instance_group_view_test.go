package nemau

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// goldenInstanceGroupPath is the view the API returns for an instance group.
// The file is a fixed copy of that response. When the response changes, update
// the copy as well, or this test checks an outdated shape.
const goldenInstanceGroupPath = "testdata/instance_group.golden.json"

// TestInstanceGroupGoldenViewDecodes decodes the golden view strictly: an
// unknown key in the file, at any depth, is an error, so a JSON tag that drifts
// from the contract fails here rather than decoding to a silent zero value.
func TestInstanceGroupGoldenViewDecodes(t *testing.T) {
	raw, err := os.ReadFile(goldenInstanceGroupPath)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var g InstanceGroup
	if err := dec.Decode(&g); err != nil {
		t.Fatalf("golden view does not decode strictly: %v", err)
	}

	if g.ID != "6f1c2a4e-8b7d-4c1a-9e2f-3a5b7c9d1e01" || g.Name != "web" || g.Description != "Public web tier" {
		t.Errorf("identity = %q %q %q", g.ID, g.Name, g.Description)
	}
	if g.Status != "ok" || g.HealthCheck != "instance" || g.HealthCheckGraceSeconds != 300 || g.DrainSeconds != 30 {
		t.Errorf("health = %q %q grace %d drain %d", g.Status, g.HealthCheck, g.HealthCheckGraceSeconds, g.DrainSeconds)
	}
	if g.MinSize != 1 || g.MaxSize != 5 || g.DesiredCapacity != 2 || g.InstanceCount != 2 {
		t.Errorf("capacity = %d/%d desired %d count %d", g.MinSize, g.MaxSize, g.DesiredCapacity, g.InstanceCount)
	}
	if g.MaxSurge != 1 || g.MaxUnavailablePct != 0 || g.TemplateVersion != 2 {
		t.Errorf("rollout = surge %d unavailable %d template %d", g.MaxSurge, g.MaxUnavailablePct, g.TemplateVersion)
	}
	if !g.CreatedAt.Equal(time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)) || g.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v / %v", g.CreatedAt, g.UpdatedAt)
	}

	lt := g.LaunchTemplate
	if lt.Version != 2 || lt.ImageID != "ubuntu-24" || lt.VCPUs != 2 || lt.RAMMB != 2048 || lt.DiskGB != 20 ||
		lt.KeyName != "ops-key" || len(lt.SecurityGroupIDs) != 1 || lt.NetworkID == "" || lt.SubnetID == "" {
		t.Errorf("launch template = %+v", lt)
	}
	if !lt.UserDataPresent || lt.UserDataSHA256 != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
		t.Errorf("user data = present %t sha %q", lt.UserDataPresent, lt.UserDataSHA256)
	}

	if len(g.Pools) != 1 || g.Pools[0].ProtocolPort != 80 || g.Pools[0].PoolID == "" {
		t.Errorf("pools = %+v", g.Pools)
	}
	if len(g.ScalingPolicies) != 1 || g.ScalingPolicies[0].Metric != "cpu_percent" || g.ScalingPolicies[0].Target != 60 ||
		g.ScalingPolicies[0].ScaleInEnabled == nil || !*g.ScalingPolicies[0].ScaleInEnabled {
		t.Errorf("scaling policies = %+v", g.ScalingPolicies)
	}
	if len(g.Schedules) != 1 || g.Schedules[0].Cron != "0 6 * * 1-5" || g.Schedules[0].DesiredCapacity == nil ||
		*g.Schedules[0].DesiredCapacity != 3 || g.Schedules[0].LastSlot == nil {
		t.Errorf("schedules = %+v", g.Schedules)
	}

	if len(g.Members) != 2 {
		t.Fatalf("members = %+v", g.Members)
	}
	if m := g.Members[1]; m.InstanceID != "e1f2a3b4-c5d6-4e7f-8091-a2b3c4d5e6f7" || m.Status != "pending" ||
		!m.ProtectedFromScaleIn || m.TemplateVersion != 1 || m.JoinedAt.IsZero() {
		t.Errorf("member = %+v", m)
	}

	if len(g.Activities) != 2 {
		t.Fatalf("activities = %+v", g.Activities)
	}
	if a := g.Activities[0]; a.ID != 42 || a.Type != "member_created" || a.Reason != "scale_out" ||
		a.InstanceID != "e1f2a3b4-c5d6-4e7f-8091-a2b3c4d5e6f7" || a.CreatedAt.IsZero() {
		t.Errorf("activity 0 = %+v", a)
	}
	// A null instance_id decodes to the empty string: the activity is not about one instance.
	if a := g.Activities[1]; a.ID != 41 || a.Type != "capacity_set" || a.InstanceID != "" {
		t.Errorf("activity 1 = %+v", a)
	}

	if g.Refresh == nil {
		t.Fatal("refresh = nil, want the golden refresh")
	}
	r := g.Refresh
	if r.Status != RefreshInProgress || r.TargetVersion != 2 || r.SkippedProtected != 1 ||
		!r.StartedAt.Equal(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)) || r.CompletedAt != nil {
		t.Errorf("refresh = %+v", r)
	}
}

// TestInstanceGroupViewSlicesAreArrays checks the SDK type, not encoding/json:
// when the API sends an empty array for each list field, the decoded value
// holds a non-nil, zero-length slice. It is strict, so a dropped or renamed
// JSON key fails here, and a slice field turned into a pointer fails to compile
// against the len calls below.
func TestInstanceGroupViewSlicesAreArrays(t *testing.T) {
	const body = `{"id":"x","pools":[],"scaling_policies":[],"schedules":[],"members":[],"activities":[]}`
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	var g InstanceGroup
	if err := dec.Decode(&g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	checks := []struct {
		name  string
		n     int
		isNil bool
	}{
		{"pools", len(g.Pools), g.Pools == nil},
		{"scaling_policies", len(g.ScalingPolicies), g.ScalingPolicies == nil},
		{"schedules", len(g.Schedules), g.Schedules == nil},
		{"members", len(g.Members), g.Members == nil},
		{"activities", len(g.Activities), g.Activities == nil},
	}
	for _, c := range checks {
		if c.isNil {
			t.Errorf("%s decoded as nil from []", c.name)
		}
		if c.n != 0 {
			t.Errorf("%s has %d elements, want 0", c.name, c.n)
		}
	}
}
