# Kubernetes

`client.Kubernetes` manages Kubernetes clusters built with Cluster API. Every
cluster method identifies the cluster by its name.

## Versions and cluster classes

```go
versions, err := client.Kubernetes.ListVersions(ctx)
if err != nil {
	return err
}
fmt.Println(versions)
```

`ListVersions` returns the supported Kubernetes versions, and
`ListClusterClasses` the cluster templates that can be deployed.

## Creating a cluster

The API requires the number of control-plane nodes and at least one node pool.
The console's defaults are 3 control-plane nodes and one pool named `workers`,
of class `default-worker`, with 3 nodes of 2 vCPUs, 4096 MB of RAM and 20 GB of
disk:

```go
cluster, err := client.Kubernetes.CreateClusterAndWait(ctx, nemau.CreateClusterRequest{
	Name:                 "prod",
	KubernetesVersion:    "v1.31.0",
	ControlPlaneVCPUs:    2,
	ControlPlaneRAMMB:    4096,
	ControlPlaneDiskGB:   20,
	ControlPlaneReplicas: 3,
	NodePools: []nemau.NodePoolInit{{
		Name:     "workers",
		Class:    "default-worker",
		Replicas: 3,
		VCPUs:    2,
		RAMMB:    4096,
		DiskGB:   20,
	}},
})
```

- `ControlPlaneReplicas` is 1 to 9. Use an odd number: etcd needs a quorum.
- Size the control plane with `ControlPlaneFlavorID`, or with all three of
  `ControlPlaneVCPUs`, `ControlPlaneRAMMB` and `ControlPlaneDiskGB`. A partial
  size is refused. Flavor fields require the compute-flavors organization
  feature.
- A node pool's `Name` is a DNS label (lowercase letters, digits and hyphens,
  63 characters at most), unique within the cluster.
- A node pool's `Replicas` is 0 to 100. A pool of zero nodes is valid.
- Size the nodes with `FlavorID`, with all three of `VCPUs`, `RAMMB` and
  `DiskGB`, or leave them empty when `CreateClusterRequest.WorkerFlavorID` is
  set.
- With an autoscaler, set `AutoscalerMin` and `AutoscalerMax`, and start
  `Replicas` at `AutoscalerMin`.

`CreateClusterAndWait` returns once the control plane is up. Provisioning takes
minutes, so give the context a generous deadline. `CreateCluster` queues the
creation without waiting.

## Reading clusters

`ListClusters` returns a summary of each cluster. Its `Status` is the
platform's lifecycle status (`active`, `building`, `upgrading`, `deleting` or
`error`), and the figures in `Specs` date from the last refresh, so a cluster
created a moment ago may not have them all yet.

`GetCluster` returns the live state: topology, conditions and API endpoint.

```go
cluster, err := client.Kubernetes.GetCluster(ctx, "prod")
if err != nil {
	return err
}
fmt.Println(cluster.Spec.Topology.Version, cluster.Status.Phase, cluster.Status.ControlPlaneReady)
```

## Kubeconfig

`GetKubeconfig` returns a cluster's kubeconfig as YAML. It contains
cluster-admin credentials: treat it as a secret. Each read is recorded as an
audit event.

```go
kubeconfig, err := client.Kubernetes.GetKubeconfig(ctx, "prod")
if err != nil {
	return err
}
if err := os.WriteFile("prod.kubeconfig", []byte(kubeconfig), 0o600); err != nil {
	return err
}
```

`RotateKubeconfig` invalidates the cluster's kubeconfig credentials and issues
new ones. It requires the kubernetes-kubeconfig-rotation organization feature.

## Scaling

| Method | Effect |
| --- | --- |
| `ScaleControlPlane(ctx, cluster, replicas)` | Changes the number of control-plane nodes. Use an odd number. |
| `AddNodePool(ctx, cluster, req)` | Adds a node pool to a running cluster. |
| `ScaleNodePool(ctx, cluster, pool, replicas)` | Changes the number of nodes in a pool. |
| `SetAutoscaler(ctx, cluster, pool, min, max)` | Sets the autoscaler bounds of a pool: `0 < min <= max`, or `0, 0` to turn the autoscaler off. |
| `RemoveNodePool(ctx, cluster, pool)` | Removes a node pool. |

Each method queues a task. `AddNodePoolRequest` requires `PoolName` and
`Class`, and the node size as `FlavorID` or as all three of `VCPUs`, `RAMMB`
and `DiskGB`:

```go
ref, err := client.Kubernetes.AddNodePool(ctx, "prod", nemau.AddNodePoolRequest{
	PoolName: "batch",
	Class:    "default-worker",
	VCPUs:    4,
	RAMMB:    8192,
	DiskGB:   40,
	Replicas: 2,
})
```

`DeleteCluster` queues the deletion of a cluster.

## Upgrades

`UpgradeCluster` upgrades a cluster to a version written `1.31.2` or
`v1.31.2`. Any other format is refused before the request is sent.

```go
ref, err := client.Kubernetes.UpgradeCluster(ctx, "prod", "1.31.2")
if err != nil {
	return err
}
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
fmt.Println(task.ResultString("maintenance_window_id"))
```

One task runs the preflight checks, opens a maintenance window and starts the
rolling upgrade. A preflight with blocking checks fails the task, which
`WaitFor` returns as a `*nemau.TaskFailedError`. The task ends when the upgrade
starts, before it finishes: follow its progress with `GetCluster`. Upgrades
require the kubernetes-guided-upgrades organization feature.

`Preflight` runs the health checks on their own, to see a blocking issue before
upgrading. With an empty version it checks the cluster's health only; with a
version it also checks that the version is allowed and not already active. It
requires the kubernetes-health-preflight organization feature.

```go
preflight, err := client.Kubernetes.Preflight(ctx, "prod", "1.31.2")
if err != nil {
	return err
}
if !preflight.Report.Ready {
	fmt.Println(preflight.Report.BlockingIssues)
}
```

An upgrade opens its own maintenance window. `CreateMaintenanceWindow`
schedules one ahead of time. Its name must match
`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`, and the window must end after it starts
and last at most 30 days.

`Upgrade` and `StartUpgrade` are deprecated names for `UpgradeCluster`.
