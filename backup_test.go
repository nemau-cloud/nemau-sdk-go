package nemau

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestBackupPolicyPatchSendsExactlyTheFieldsThatWereSet(t *testing.T) {
	client, seen := captureUpdate(t)

	// Hour 0 is a valid value and must reach the wire: it is a pointer, so
	// omitempty does not drop it.
	if _, err := client.Compute.UpdateInstance(context.Background(), "inst-1", Update{
		BackupSchedule:      Set(BackupWeekly),
		BackupHour:          Set(0),
		BackupWeekday:       Set(6),
		BackupRetentionDays: Set(35),
	}); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}

	if seen.method != http.MethodPatch || seen.path != "/api/v1/compute/instances/inst-1" {
		t.Errorf("request = %s %s", seen.method, seen.path)
	}
	want := map[string]any{
		"backup_schedule":       "weekly",
		"backup_hour":           float64(0),
		"backup_weekday":        float64(6),
		"backup_retention_days": float64(35),
	}
	if len(seen.body) != len(want) {
		t.Errorf("body = %v, want exactly %v", seen.body, want)
	}
	for k, v := range want {
		if got, ok := seen.body[k]; !ok || got != v {
			t.Errorf("%s = %v (present %v), want %v", k, got, ok, v)
		}
	}
}

func TestBackupPolicyPatchLeavesUnsetBackupFieldsOut(t *testing.T) {
	client, seen := captureUpdate(t)

	if _, err := client.Compute.UpdateInstance(context.Background(), "inst-1",
		Update{BackupSchedule: Set(BackupNone)}); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}
	if len(seen.body) != 1 || seen.body["backup_schedule"] != "none" {
		t.Errorf("body = %v, want only backup_schedule none", seen.body)
	}
}

func TestBackupPolicyRefusesOutOfRangeValuesWithoutARequest(t *testing.T) {
	cases := map[string]Update{
		"unknown schedule":   {BackupSchedule: Set("hourly")},
		"hour too high":      {BackupHour: Set(24)},
		"hour negative":      {BackupHour: Set(-1)},
		"weekday too high":   {BackupWeekday: Set(7)},
		"weekday negative":   {BackupWeekday: Set(-1)},
		"retention zero":     {BackupRetentionDays: Set(0)},
		"retention too high": {BackupRetentionDays: Set(36)},
	}
	for name, update := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := captureUpdate(t)
			_, err := client.Compute.UpdateInstance(context.Background(), "inst-1", update)
			if err == nil {
				t.Fatal("err = nil, want a range refusal")
			}
			if seen.method != "" {
				t.Error("an out-of-range policy still made a request")
			}
		})
	}
}

func TestValidateBackupPolicyAcceptsTheBounds(t *testing.T) {
	ok := []Update{
		{BackupSchedule: Set(BackupNone)},
		{BackupSchedule: Set(BackupDaily), BackupHour: Set(23), BackupRetentionDays: Set(1)},
		{BackupSchedule: Set(BackupWeekly), BackupHour: Set(0), BackupWeekday: Set(0), BackupRetentionDays: Set(35)},
		// An unset field is not checked: the merged state is checked by the platform.
		{BackupHour: Set(5)},
		{},
	}
	for i, u := range ok {
		if err := ValidateBackupPolicy(u); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestInstanceDecodesTheBackupPolicy(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"instance":{"id":"inst-1","name":"web","status":"running",
			"backup_schedule":"weekly","backup_hour":0,"backup_weekday":3,"backup_retention_days":14}}`))
	}))
	inst, err := client.Compute.GetInstance(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.BackupSchedule != BackupWeekly {
		t.Errorf("BackupSchedule = %q", inst.BackupSchedule)
	}
	if inst.BackupHour == nil || *inst.BackupHour != 0 {
		t.Errorf("BackupHour = %v, want a set 0", inst.BackupHour)
	}
	if inst.BackupWeekday == nil || *inst.BackupWeekday != 3 {
		t.Errorf("BackupWeekday = %v", inst.BackupWeekday)
	}
	if inst.BackupRetentionDays == nil || *inst.BackupRetentionDays != 14 {
		t.Errorf("BackupRetentionDays = %v", inst.BackupRetentionDays)
	}
}

func TestInstanceWithoutAPolicyDecodesAsNone(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"instance":{"id":"inst-1","name":"web","backup_schedule":"none"}}`))
	}))
	inst, err := client.Compute.GetInstance(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.BackupSchedule != BackupNone {
		t.Errorf("BackupSchedule = %q, want none", inst.BackupSchedule)
	}
	if inst.BackupHour != nil || inst.BackupWeekday != nil || inst.BackupRetentionDays != nil {
		t.Error("unset backup fields decoded as non-nil")
	}
}

func TestSnapshotDecodesOriginAndExpiry(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"snapshots":[
			{"id":"s-1","origin":"scheduled","expires_at":"2026-11-20T02:00:00Z"},
			{"id":"s-2","origin":"manual"}]}`))
	}))
	snaps, err := client.Compute.ListSnapshots(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots", len(snaps))
	}
	want := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	if snaps[0].Origin != SnapshotOriginScheduled || snaps[0].ExpiresAt == nil || !snaps[0].ExpiresAt.Equal(want) {
		t.Errorf("scheduled snapshot = %+v", snaps[0])
	}
	if snaps[1].Origin != SnapshotOriginManual || snaps[1].ExpiresAt != nil {
		t.Errorf("manual snapshot = %+v", snaps[1])
	}
}
