package nemau

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
		         "kubernetes_version":"v1.35.0","node_pools_count":1,
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
		NetworkID: testNetworkID, SubnetID: testK8sSubnetID,
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

const (
	testNetworkID   = "6f1c0a52-0000-4000-8000-0000000000aa"
	testK8sSubnetID = "6f1c0a52-0000-4000-8000-0000000000bb"
)

// validCluster is a create request that passes every client-side check; the
// tests below change one field at a time.
func validCluster() CreateClusterRequest {
	return CreateClusterRequest{
		Name: "prod", KubernetesVersion: "v1.31.0",
		NetworkID: testNetworkID, SubnetID: testK8sSubnetID,
		ControlPlaneReplicas: 3, ControlPlaneFlavorID: "flavor-cp",
		NodePools: []NodePoolInit{{Name: "workers", Class: "default-worker", Replicas: 3, FlavorID: "flavor-w"}},
	}
}

// refusedWithoutSending asserts that CreateCluster returns an error and sends
// no HTTP request.
func refusedWithoutSending(t *testing.T, req CreateClusterRequest, wantInError string) {
	t.Helper()
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))
	ref, err := c.Kubernetes.CreateCluster(context.Background(), req)
	if err == nil {
		t.Fatalf("CreateCluster accepted the request, ref = %+v", ref)
	}
	if !strings.Contains(err.Error(), wantInError) {
		t.Errorf("error = %q, want it to mention %q", err, wantInError)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("%d HTTP requests sent for a refused request", n)
	}
}

func TestCreateClusterRefusesMissingNetworkWithoutSending(t *testing.T) {
	req := validCluster()
	req.NetworkID = ""
	refusedWithoutSending(t, req, "NetworkID is required")
}

func TestCreateClusterRefusesMissingSubnetWithoutSending(t *testing.T) {
	req := validCluster()
	req.SubnetID = ""
	refusedWithoutSending(t, req, "SubnetID is required")
}

func TestCreateClusterRefusesMalformedNetworkAndSubnetIDs(t *testing.T) {
	for _, id := range []string{"net-1", "6f1c0a52-0000-4000-8000-0000000000aa;drop", "../../x"} {
		req := validCluster()
		req.NetworkID = id
		refusedWithoutSending(t, req, "is not a UUID")

		req = validCluster()
		req.SubnetID = id
		refusedWithoutSending(t, req, "is not a UUID")
	}
}

func TestCreateClusterSendsNetworkAndSubnetAndOmitsEmptyCIDRs(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))
	if _, err := c.Kubernetes.CreateCluster(context.Background(), validCluster()); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	if body["network_id"] != testNetworkID || body["subnet_id"] != testK8sSubnetID {
		t.Errorf("network_id/subnet_id = %v / %v", body["network_id"], body["subnet_id"])
	}
	for _, key := range []string{"pod_cidr", "service_cidr"} {
		if _, present := body[key]; present {
			t.Errorf("%s sent although empty: %v", key, body[key])
		}
	}
}

// CIDRs are checked by the server: the SDK sends a set value as given.
func TestCreateClusterSendsCIDRsWhenSet(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))
	req := validCluster()
	req.PodCIDR = "10.244.0.0/16"
	req.ServiceCIDR = "10.96.0.0/12"
	if _, err := c.Kubernetes.CreateCluster(context.Background(), req); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	if body["pod_cidr"] != "10.244.0.0/16" || body["service_cidr"] != "10.96.0.0/12" {
		t.Errorf("pod_cidr/service_cidr = %v / %v", body["pod_cidr"], body["service_cidr"])
	}
}

// poolWith returns a valid cluster whose only node pool is changed by mutate.
func poolWith(mutate func(*NodePoolInit)) CreateClusterRequest {
	req := validCluster()
	req.NodePools[0].Name = "gpu"
	mutate(&req.NodePools[0])
	return req
}

// labels returns n valid "key=value" labels.
func labels(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "k" + strconv.Itoa(i) + "=v"
	}
	return out
}

// taints returns n valid "key:NoSchedule" taints.
func taints(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "t" + strconv.Itoa(i) + ":NoSchedule"
	}
	return out
}

func TestCreateClusterRefusesInvalidNodePoolLabelsAndTaints(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*NodePoolInit)
		want   string
	}{
		{"label with bad key", func(p *NodePoolInit) { p.Labels = []string{"Bad_Key=x"} }, `label key "Bad_Key" must be`},
		{"label with empty value", func(p *NodePoolInit) { p.Labels = []string{"tier="} }, `label "tier" has an invalid value`},
		{"label without equals sign", func(p *NodePoolInit) { p.Labels = []string{"gpu"} }, `label "gpu" must be key=value`},
		{"label value with a space", func(p *NodePoolInit) { p.Labels = []string{"tier=a b"} }, `label "tier" has an invalid value`},
		{"17 labels", func(p *NodePoolInit) { p.Labels = labels(17) }, "17 labels, at most 16"},
		{"taint without effect", func(p *NodePoolInit) { p.Taints = []string{"dedicated=gpu"} }, `taint "dedicated=gpu" must end in :`},
		{"taint with unknown effect", func(p *NodePoolInit) { p.Taints = []string{"dedicated=gpu:NoRun"} }, `taint "dedicated=gpu:NoRun" must end in :`},
		{"taint with lowercase effect", func(p *NodePoolInit) { p.Taints = []string{"dedicated:noschedule"} }, "must end in :"},
		{"taint with bad key", func(p *NodePoolInit) { p.Taints = []string{"Bad:NoSchedule"} }, `taint key "Bad" must be`},
		{"taint with bad value", func(p *NodePoolInit) { p.Taints = []string{"dedicated=a b:NoExecute"} }, `taint "dedicated" has an invalid value`},
		{"17 taints", func(p *NodePoolInit) { p.Taints = taints(17) }, "17 taints, at most 16"},
		{"class other than default-worker", func(p *NodePoolInit) { p.Class = "gpu-worker" }, `class "gpu-worker" is not supported`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refusedWithoutSending(t, poolWith(tc.mutate), tc.want)
		})
	}
}

// A refusal names the pool by index and name, so the caller can find it.
func TestCreateClusterNamesThePoolInItsError(t *testing.T) {
	req := validCluster()
	req.NodePools = append(req.NodePools, NodePoolInit{Name: "gpu", Replicas: 1, Labels: []string{"nope"}})
	refusedWithoutSending(t, req, `node pool 1 ("gpu")`)
}

func TestCreateClusterSendsValidLabelsAndTaints(t *testing.T) {
	var sent int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sent, 1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))
	req := poolWith(func(p *NodePoolInit) {
		p.Class = "default-worker"
		p.Labels = append(labels(15), "tier=gpu.v1_a-b")
		p.Taints = []string{"dedicated=gpu:NoSchedule", "spot:PreferNoSchedule", "maint=now:NoExecute"}
	})
	if _, err := c.Kubernetes.CreateCluster(context.Background(), req); err != nil {
		t.Fatalf("CreateCluster refused valid pool: %v", err)
	}
	if atomic.LoadInt32(&sent) != 1 {
		t.Errorf("%d requests sent, want 1", sent)
	}
}

// The 16-entry limit itself is allowed, and an empty class means the default.
func TestCreateClusterAcceptsTheLimitsAndEmptyClass(t *testing.T) {
	req := poolWith(func(p *NodePoolInit) {
		p.Class = ""
		p.Labels = labels(16)
		p.Taints = taints(16)
	})
	var sent int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sent, 1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))
	if _, err := c.Kubernetes.CreateCluster(context.Background(), req); err != nil {
		t.Fatalf("CreateCluster refused the limits: %v", err)
	}
	if atomic.LoadInt32(&sent) != 1 {
		t.Errorf("%d requests sent, want 1", sent)
	}
}
