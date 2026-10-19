package nemau

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// KubernetesService covers /kubernetes: managed clusters driven by Cluster API.
//
// Clusters are addressed by name, not by id: every endpoint here takes the
// cluster's Kubernetes object name.
type KubernetesService struct{ client *Client }

// ListVersions returns the supported Kubernetes versions.
func (s *KubernetesService) ListVersions(ctx context.Context) ([]string, error) {
	var out struct {
		Versions []string `json:"versions"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/kubernetes/versions"}, &out)
	return out.Versions, err
}

// ListClusterClasses returns the deployable cluster templates.
func (s *KubernetesService) ListClusterClasses(ctx context.Context) ([]ClusterClass, error) {
	var out struct {
		ClusterClasses []ClusterClass `json:"cluster_classes"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/kubernetes/cluster-classes"}, &out)
	return out.ClusterClasses, err
}

// ListClusters returns the project's clusters as the platform lists them.
//
// A listing is a summary. Its Status is the platform's lowercase lifecycle
// status ("active", "building", ...), and the version, phase and readiness flags
// in Specs date from the last reconciliation. Use GetCluster for the live state:
// topology, conditions and API endpoint.
func (s *KubernetesService) ListClusters(ctx context.Context) ([]KubernetesClusterSummary, error) {
	var out struct {
		Clusters []KubernetesClusterSummary `json:"clusters"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/kubernetes/clusters"}, &out)
	return out.Clusters, err
}

// GetCluster returns one cluster's live state.
func (s *KubernetesService) GetCluster(ctx context.Context, name string) (*KubernetesCluster, error) {
	var out KubernetesCluster
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/kubernetes/clusters/" + url.PathEscape(name),
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetKubeconfig returns a cluster's kubeconfig as YAML.
//
// Reading one is an audited event (GET_KUBECONFIG): it hands out cluster-admin
// credentials, so treat the result as a secret.
func (s *KubernetesService) GetKubeconfig(ctx context.Context, name string) (string, error) {
	var out struct {
		ClusterName string `json:"cluster_name"`
		Kubeconfig  string `json:"kubeconfig"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/kubernetes/clusters/" + url.PathEscape(name) + "/kubeconfig",
	}, &out)
	return out.Kubeconfig, err
}

// CreateClusterRequest describes a new cluster.
//
// Control-plane sizing is either ControlPlaneFlavorID or all three of
// ControlPlaneVCPUs/RAMMB/DiskGB; a partial custom size is rejected. Any flavor
// field also requires the compute-flavors organization feature.
//
// ControlPlaneReplicas (1 to 9; use an odd number, etcd needs a quorum) and at
// least one NodePools entry are required: the API refuses a cluster without
// them. The console's defaults are 3 control-plane nodes and one pool named
// "workers", class "default-worker", 3 nodes of 2 vCPU, 4096 MB, 20 GB.
type CreateClusterRequest struct {
	Name                 string         `json:"name"`
	KubernetesVersion    string         `json:"kubernetes_version"`
	ControlPlaneVCPUs    int            `json:"control_plane_vcpus,omitempty"`
	ControlPlaneRAMMB    int            `json:"control_plane_ram_mb,omitempty"`
	ControlPlaneDiskGB   int            `json:"control_plane_disk_gb,omitempty"`
	ControlPlaneFlavorID string         `json:"control_plane_flavor_id,omitempty"`
	WorkerFlavorID       string         `json:"worker_flavor_id,omitempty"`
	ClusterClass         string         `json:"cluster_class,omitempty"`
	ControlPlaneReplicas int            `json:"control_plane_replicas,omitempty"`
	NodePools            []NodePoolInit `json:"node_pools,omitempty"`
}

// NodePoolInit is a worker group declared at cluster creation.
//
// Name is a DNS label (lowercase letters, digits and hyphens, 63 characters at
// most), unique within the cluster. Replicas is 0 to 100 and always sent: a
// pool of zero nodes is valid. The node size is FlavorID, or all three of
// VCPUs/RAMMB/DiskGB, or nothing when CreateClusterRequest.WorkerFlavorID is
// set. With an autoscaler, start Replicas at AutoscalerMin.
type NodePoolInit struct {
	Name          string   `json:"name"`
	Class         string   `json:"class,omitempty"`
	FlavorID      string   `json:"flavor_id,omitempty"`
	VCPUs         int      `json:"vcpus,omitempty"`
	RAMMB         int      `json:"ram_mb,omitempty"`
	DiskGB        int      `json:"disk_gb,omitempty"`
	Replicas      int      `json:"replicas"`
	AutoscalerMin int      `json:"autoscaler_min,omitempty"`
	AutoscalerMax int      `json:"autoscaler_max,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	Taints        []string `json:"taints,omitempty"`
}

// CreateCluster queues a cluster creation.
func (s *KubernetesService) CreateCluster(ctx context.Context, req CreateClusterRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/kubernetes/clusters", nil, req)
}

// CreateClusterAndWait creates a cluster and returns it once the control plane
// is up. Provisioning a cluster takes minutes; give ctx a generous deadline.
func (s *KubernetesService) CreateClusterAndWait(ctx context.Context, req CreateClusterRequest) (*KubernetesCluster, error) {
	ref, err := s.CreateCluster(ctx, req)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.WaitFor(ctx, ref); err != nil {
		return nil, err
	}
	// Read the cluster by name, which is what every cluster endpoint takes.
	return s.GetCluster(ctx, req.Name)
}

// DeleteCluster queues a cluster deletion.
func (s *KubernetesService) DeleteCluster(ctx context.Context, name string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/kubernetes/clusters/"+url.PathEscape(name), nil, nil)
}

// ScaleControlPlane queues a control-plane replica change. Use an odd count:
// etcd needs a quorum.
func (s *KubernetesService) ScaleControlPlane(ctx context.Context, name string, replicas int) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/kubernetes/clusters/"+url.PathEscape(name)+"/scale-control-plane", nil,
		map[string]int{"replicas": replicas})
}

// AddNodePoolRequest describes a worker group added to a live cluster.
//
// Sizing is either FlavorID (an organization flavor, which then sets the node
// size) or all three of VCPUs/RAMMB/DiskGB. Class is required by the API.
type AddNodePoolRequest struct {
	PoolName      string `json:"pool_name"`
	Class         string `json:"class"`
	FlavorID      string `json:"flavor_id,omitempty"`
	VCPUs         int    `json:"vcpus,omitempty"`
	RAMMB         int    `json:"ram_mb,omitempty"`
	DiskGB        int    `json:"disk_gb,omitempty"`
	Replicas      int    `json:"replicas,omitempty"`
	AutoscalerMin int    `json:"autoscaler_min,omitempty"`
	AutoscalerMax int    `json:"autoscaler_max,omitempty"`
}

// AddNodePool queues adding a worker group to a cluster.
func (s *KubernetesService) AddNodePool(ctx context.Context, cluster string, req AddNodePoolRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/kubernetes/clusters/"+url.PathEscape(cluster)+"/node-pools", nil, req)
}

// RemoveNodePool queues removing a worker group.
func (s *KubernetesService) RemoveNodePool(ctx context.Context, cluster, pool string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete,
		"/kubernetes/clusters/"+url.PathEscape(cluster)+"/node-pools/"+url.PathEscape(pool), nil, nil)
}

// ScaleNodePool queues a worker-group replica change.
func (s *KubernetesService) ScaleNodePool(ctx context.Context, cluster, pool string, replicas int) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/kubernetes/clusters/"+url.PathEscape(cluster)+"/node-pools/"+url.PathEscape(pool)+"/scale", nil,
		map[string]int{"replicas": replicas})
}

// SetAutoscaler queues autoscaler bounds on a worker group. Both bounds are
// required: either 0 < min <= max, or min = max = 0 to turn the autoscaler off.
func (s *KubernetesService) SetAutoscaler(ctx context.Context, cluster, pool string, min, max int) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/kubernetes/clusters/"+url.PathEscape(cluster)+"/node-pools/"+url.PathEscape(pool)+"/autoscaler", nil,
		map[string]int{"min": min, "max": max})
}

// kubernetesVersionPattern is the version shape the API accepts: x.y.z, with an
// optional leading "v". Whether the version is one the platform offers is the
// server's call (see ListVersions).
var kubernetesVersionPattern = regexp.MustCompile(`^v?[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)

// UpgradeCluster queues a guided version upgrade of a cluster to version
// ("1.31.2" or "v1.31.2").
//
// One task does the whole sequence on the platform side: preflight checks, a
// maintenance window, then the upgrade. There is nothing to open or check
// beforehand. A preflight with blocking checks fails the task rather than the
// request, so the refusal arrives through [Client.WaitFor] as a
// [TaskFailedError]; run [KubernetesService.Preflight] first to see it early.
// The task completes when the rolling upgrade starts, before it finishes;
// follow its progress with GetCluster. Its result names the window
// (maintenance_window_id).
//
// Requires the kubernetes-guided-upgrades organization feature.
func (s *KubernetesService) UpgradeCluster(ctx context.Context, name, version string) (*TaskRef, error) {
	if !kubernetesVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("nemau: invalid Kubernetes version %q: want x.y.z, such as 1.31.2", version)
	}
	return s.client.submit(ctx, http.MethodPost,
		"/kubernetes/clusters/"+url.PathEscape(name)+"/upgrade", nil,
		map[string]string{"version": version})
}

// Upgrade is UpgradeCluster.
//
// Deprecated: use [KubernetesService.UpgradeCluster]. The upgrade task runs the
// preflight and opens the maintenance window itself.
func (s *KubernetesService) Upgrade(ctx context.Context, name, targetVersion string) (*TaskRef, error) {
	return s.UpgradeCluster(ctx, name, targetVersion)
}

// StartUpgrade is UpgradeCluster.
//
// Deprecated: use [KubernetesService.UpgradeCluster], which does not need a
// maintenance window to exist beforehand.
func (s *KubernetesService) StartUpgrade(ctx context.Context, name, targetVersion string) (*TaskRef, error) {
	return s.UpgradeCluster(ctx, name, targetVersion)
}

// Preflight runs the pre-upgrade health checks. An empty targetVersion checks
// the cluster's health only; a version also checks that it is allowed and not
// already active.
//
// Requires the kubernetes-health-preflight organization feature.
func (s *KubernetesService) Preflight(ctx context.Context, name, targetVersion string) (*KubernetesPreflight, error) {
	body := map[string]string{}
	if targetVersion != "" {
		body["target_version"] = targetVersion
	}
	var out KubernetesPreflight
	err := s.client.do(ctx, request{
		method: http.MethodPost, path: "/kubernetes/clusters/" + url.PathEscape(name) + "/preflight", body: body,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// MaintenanceWindowRequest opens a maintenance window on a cluster.
//
// Name must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$; the window must end after
// it starts and last at most 30 days.
type MaintenanceWindowRequest struct {
	Name     string
	StartsAt time.Time
	EndsAt   time.Time
	Reason   string
}

// CreateMaintenanceWindow opens a maintenance window on a cluster. An upgrade
// opens its own, so this is only for scheduling a window ahead of one.
func (s *KubernetesService) CreateMaintenanceWindow(ctx context.Context, name string, req MaintenanceWindowRequest) (*MaintenanceWindow, error) {
	var out MaintenanceWindow
	err := s.client.do(ctx, request{
		method: http.MethodPost, path: "/kubernetes/clusters/" + url.PathEscape(name) + "/maintenance",
		body: map[string]string{
			"name":      req.Name,
			"starts_at": req.StartsAt.UTC().Format(time.RFC3339),
			"ends_at":   req.EndsAt.UTC().Format(time.RFC3339),
			"reason":    req.Reason,
		},
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateKubeconfig invalidates a cluster's kubeconfig credentials and issues
// new ones. Requires the kubernetes-kubeconfig-rotation organization feature.
func (s *KubernetesService) RotateKubeconfig(ctx context.Context, name string) (map[string]any, error) {
	var out map[string]any
	err := s.client.do(ctx, request{
		method: http.MethodPost, path: "/kubernetes/clusters/" + url.PathEscape(name) + "/kubeconfig/rotate",
	}, &out)
	return out, err
}
