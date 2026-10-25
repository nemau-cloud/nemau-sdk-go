package nemau

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// goldenKubernetesClusterPath is the cluster read view the API returns. The
// file is a copy of the golden fixture that defines the contract; re-copy it
// when the view changes. No CI step enforces the copy.
const goldenKubernetesClusterPath = "testdata/kubernetes_cluster.golden.json"

// TestKubernetesClusterGoldenDecodesStrictly decodes the golden view with
// unknown keys refused, at any depth, so a drifting JSON tag fails here.
func TestKubernetesClusterGoldenDecodesStrictly(t *testing.T) {
	raw, err := os.ReadFile(goldenKubernetesClusterPath)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c KubernetesCluster
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("golden view does not decode strictly: %v", err)
	}

	spec := c.Spec
	if spec.NetworkID != "11111111-1111-4111-8111-111111111111" || spec.SubnetID != "22222222-2222-4222-8222-222222222222" {
		t.Errorf("network = %q subnet = %q", spec.NetworkID, spec.SubnetID)
	}
	if spec.PodCIDR != "10.245.0.0/16" || spec.ServiceCIDR != "10.96.0.0/12" {
		t.Errorf("cidrs = %q %q", spec.PodCIDR, spec.ServiceCIDR)
	}
	if spec.Topology.ControlPlaneReplicas != 3 || len(spec.Topology.NodePools) != 2 {
		t.Fatalf("topology = %+v", spec.Topology)
	}

	general := spec.Topology.NodePools[0]
	if len(general.Labels) != 1 || general.Labels[0] != "workload=general" ||
		len(general.Taints) != 1 || general.Taints[0] != "dedicated=general:NoSchedule" {
		t.Errorf("general pool labels = %v taints = %v", general.Labels, general.Taints)
	}
	// An empty array decodes to a non-nil, zero-length slice.
	batch := spec.Topology.NodePools[1]
	if batch.Labels == nil || batch.Taints == nil || len(batch.Labels) != 0 || len(batch.Taints) != 0 {
		t.Errorf("batch pool labels = %v (nil %t) taints = %v (nil %t)",
			batch.Labels, batch.Labels == nil, batch.Taints, batch.Taints == nil)
	}
}

// TestAddNodePoolSendsLabelsAndTaints checks the body and the refusals of a pool
// added to a live cluster.
func TestAddNodePoolSendsLabelsAndTaints(t *testing.T) {
	cap := &capture{resp: `{"task_id":"task-1","status":"queued"}`}
	c, _ := newTestClient(t, cap.handler())
	_, err := c.Kubernetes.AddNodePool(context.Background(), "prod", AddNodePoolRequest{
		PoolName: "gpu", Class: "default-worker", VCPUs: 4, RAMMB: 8192, DiskGB: 40, Replicas: 1,
		Labels: []string{"accelerator=gpu"},
		Taints: []string{"dedicated=gpu:NoSchedule"},
	})
	if err != nil {
		t.Fatalf("AddNodePool: %v", err)
	}
	if cap.path != "/api/v1/kubernetes/clusters/prod/node-pools" {
		t.Errorf("path = %s", cap.path)
	}
	for _, want := range []string{`"labels":["accelerator=gpu"]`, `"taints":["dedicated=gpu:NoSchedule"]`} {
		if !strings.Contains(cap.body, want) {
			t.Errorf("body lacks %s: %s", want, cap.body)
		}
	}
}

func TestAddNodePoolRefusesBadLabelsAndTaintsWithoutSending(t *testing.T) {
	cap := &capture{resp: `{"task_id":"task-1","status":"queued"}`}
	c, _ := newTestClient(t, cap.handler())
	cases := map[string]AddNodePoolRequest{
		"label without value sign": {PoolName: "gpu", Class: "default-worker", Labels: []string{"gpu"}},
		"taint without effect":     {PoolName: "gpu", Class: "default-worker", Taints: []string{"dedicated=gpu"}},
		"unknown class":            {PoolName: "gpu", Class: "gpu-worker", Labels: []string{"a=b"}},
	}
	for name, req := range cases {
		if _, err := c.Kubernetes.AddNodePool(context.Background(), "prod", req); err == nil {
			t.Errorf("%s: refused input was accepted", name)
		}
	}
	if cap.method != "" {
		t.Errorf("refused input reached the server: %s %s", cap.method, cap.path)
	}
	_ = http.MethodPost
}
