package nemau

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

// The listing is the platform's record of the cluster: a lowercase status and
// the figures in specs. It decodes into the summary type, whose "status" is a
// string, unlike the detail type's.
func TestListClustersDecodesTheSummaryRow(t *testing.T) {
	cap := &capture{resp: `{"clusters":[{
		"id":"6f1c0a52-0000-4000-8000-000000000001","resourceType":"kubernetes_cluster",
		"name":"prod","status":"active","tenantId":"t","projectId":"p","userId":"u",
		"specs":{"phase":"Provisioned","infrastructure_ready":true,"control_plane_ready":true,
		         "kubernetes_version":"v1.35.0","namespace":"nemau-project-p","node_pools_count":1,
		         "control_plane_replicas":3,"worker_replicas":3,"worker_vcpus":12,"billing_future_key":"x"},
		"createdAt":"2026-09-01T10:00:00Z","updatedAt":"2026-09-01T10:05:00Z"},
		{"id":"c2","name":"staging","status":"building","specs":{"kubernetes_version":"v1.36.0"},
		 "createdAt":"2026-09-02T10:00:00Z","updatedAt":"2026-09-02T10:00:00Z"}],"count":2}`}
	c, _ := newTestClient(t, cap.handler())

	clusters, err := c.Kubernetes.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/v1/kubernetes/clusters" {
		t.Errorf("%s %s", cap.method, cap.path)
	}
	if len(clusters) != 2 {
		t.Fatalf("got %d clusters", len(clusters))
	}
	prod := clusters[0]
	if prod.Status != "active" || prod.Specs.Phase != "Provisioned" || prod.Specs.KubernetesVersion != "v1.35.0" {
		t.Errorf("prod = %+v", prod)
	}
	if !prod.Specs.ControlPlaneReady || prod.Specs.ControlPlaneReplicas != 3 || prod.Specs.WorkerReplicas != 3 {
		t.Errorf("prod specs = %+v", prod.Specs)
	}
	if prod.CreatedAt == nil || prod.CreatedAt.Year() != 2026 {
		t.Errorf("createdAt = %v", prod.CreatedAt)
	}
	// A cluster not reconciled yet carries only part of its specs.
	if clusters[1].Status != "building" || clusters[1].Specs.Phase != "" {
		t.Errorf("staging = %+v", clusters[1])
	}
}

// The platform runs preflight, maintenance window and upgrade inside one task:
// UpgradeCluster is a single call whose body names the version.
func TestUpgradeClusterIsOneCallWithVersion(t *testing.T) {
	var calls []string
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-up","status":"queued"}`))
	}))

	ref, err := c.Kubernetes.UpgradeCluster(context.Background(), "prod", "v1.36.0")
	if err != nil {
		t.Fatalf("UpgradeCluster: %v", err)
	}
	if ref.TaskID != "task-up" {
		t.Errorf("task = %q", ref.TaskID)
	}
	if len(calls) != 1 || calls[0] != "POST /api/v1/kubernetes/clusters/prod/upgrade" {
		t.Errorf("calls = %v", calls)
	}
	// Exactly {"version": ...}: target_version is not sent, because the server
	// refuses unknown fields.
	if len(body) != 1 || body["version"] != "v1.36.0" {
		t.Errorf("body = %v", body)
	}
}

func TestUpgradeClusterRejectsABadVersionBeforeSending(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	for _, v := range []string{"", "latest", "1.31", "1.31.0-rc1", "v1.31.0; drop"} {
		if _, err := c.Kubernetes.UpgradeCluster(context.Background(), "prod", v); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}
	if calls != 0 {
		t.Errorf("%d requests sent for invalid versions", calls)
	}
}

// The API refuses a cluster without control-plane replicas or node pools; the
// request must carry both, and a pool of zero nodes must keep its zero.
func TestCreateClusterSendsReplicasAndNodePools(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/kubernetes/clusters" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-1","status":"queued"}`))
	}))

	_, err := c.Kubernetes.CreateCluster(context.Background(), CreateClusterRequest{
		Name: "prod", KubernetesVersion: "v1.31.0",
		ControlPlaneVCPUs: 2, ControlPlaneRAMMB: 4096, ControlPlaneDiskGB: 20,
		ControlPlaneReplicas: 3,
		NodePools: []NodePoolInit{
			{Name: "workers", Class: "default-worker", VCPUs: 2, RAMMB: 4096, DiskGB: 20, Replicas: 3},
			{Name: "standby", Class: "default-worker", FlavorID: "flavor-1", Replicas: 0},
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	if body["control_plane_replicas"] != float64(3) {
		t.Errorf("control_plane_replicas = %v", body["control_plane_replicas"])
	}
	pools, _ := body["node_pools"].([]any)
	if len(pools) != 2 {
		t.Fatalf("node_pools = %v", body["node_pools"])
	}
	standby := pools[1].(map[string]any)
	if replicas, present := standby["replicas"]; !present || replicas != float64(0) {
		t.Errorf("zero-node pool lost its replicas: %v", standby)
	}
	if standby["flavor_id"] != "flavor-1" || standby["vcpus"] != nil {
		t.Errorf("flavor-sized pool = %v", standby)
	}
}
