# Asynchronous writes

Most writes are asynchronous. The API accepts a create, an update, a delete or
an action with `202 Accepted` and a task id, and the platform does the work in
the background. The SDK returns that answer as a `*nemau.TaskRef`. When the
call returns, the resource does not exist yet.

```go
ref, err := client.Network.CreateNetwork(ctx, "backend", "")
if err != nil {
	return err
}
fmt.Println("task", ref.TaskID, ref.Status)
```

## Waiting for a task

`client.WaitFor` reads the task until it reaches a terminal status and returns
it:

```go
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
if task.ResourceID != nil {
	fmt.Println("network", *task.ResourceID)
}
```

- A completed task is returned with a nil error.
- A failed or cancelled task is returned with a `*nemau.TaskFailedError` that
  carries it. See [Errors and retries](errors-and-retries.md#failed-tasks).
- A nil `ref` returns `nil, nil`.

`client.WaitForTask(ctx, taskID)` does the same from a task id, for example one
stored by an earlier run.

A task can be unreadable for a short time right after the write. `WaitFor`
keeps polling through that `404` instead of failing. It reads the task every
2 seconds by default; `nemau.WithPollInterval` changes the interval.

## Timeouts and cancellation

The SDK has no separate timeout for waiting. The deadline and the cancellation
come from the context:

```go
waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
defer cancel()

task, err := client.WaitFor(waitCtx, ref)
if errors.Is(err, context.DeadlineExceeded) {
	// The task may still be running. Read it later with
	// client.Tasks.Get(ctx, ref.TaskID).
}
```

Stopping the wait does not stop the task. The API has no way to cancel a task.

## Methods that wait

These methods submit the write, wait for the task, then read the resource back:

| Method | Returns |
| --- | --- |
| `client.Compute.CreateInstanceAndWait` | `*nemau.Instance` |
| `client.Network.CreateNetworkAndWait` | `*nemau.Network` |
| `client.LoadBalancer.CreateAndWait` | `*nemau.LoadBalancer` |
| `client.Storage.CreateVolumeAndWait` | `*nemau.Volume` |
| `client.Kubernetes.CreateClusterAndWait` | `*nemau.KubernetesCluster` |
| `client.DNS.CreateZoneAndWait` | `*nemau.DNSZone` |
| `client.DNS.VerifyZoneAndWait` | `*nemau.DNSZone` |

```go
instance, err := client.Compute.CreateInstanceAndWait(ctx, nemau.CreateInstanceRequest{
	Name:      "web-1",
	ImageID:   "ubuntu-24.04",
	VCPUs:     2,
	RAMMB:     4096,
	DiskGB:    40,
	NetworkID: networkID,
})
if err != nil {
	return err
}
fmt.Println(instance.ID, instance.Status, instance.Addresses)
```

They return the same errors as `WaitFor`, and are bounded by the context in the
same way.

## Reading a task

A `*nemau.Task` has these fields:

| Field | Content |
| --- | --- |
| `ID`, `TaskType` | The task id and the kind of operation. |
| `Status` | `pending`, `running`, `retrying`, `completed`, `failed` or `cancelled` (the `nemau.TaskStatus...` constants). |
| `Progress` | Progress reported by the platform. |
| `ResourceID`, `ResourceType` | The resource the task acted on, once one exists. |
| `Result` | The outputs of the operation, as a map. |
| `Error`, `ErrorCode` | Why the task failed. See [Errors and retries](errors-and-retries.md#failed-tasks). |
| `CreatedAt`, `CompletedAt` | Timestamps. |

`Terminal()` reports whether the task has reached a final status, and
`Succeeded()` whether it completed.

The platform sets `ResourceID` on any task linked to a resource. Some
operations also report outputs in `Result`, whose keys depend on the operation.
`ResultString(keys...)` and `ResultInt(keys...)` return the value of the first
key present. The keys documented in this guide are `snapshot_id` and
`size_bytes` for a snapshot, `device_id` for a volume attachment, and
`maintenance_window_id` for a Kubernetes upgrade.

`client.Tasks.Get(ctx, id)` reads one task, and
`client.Tasks.ListForResource(ctx, resourceID)` returns the operations recorded
against one resource. The API has no listing of all tasks.

## Synchronous calls

A few writes answer at once with their result and return no `TaskRef`:

- Compute: `CreateFlavor`, `UpdateFlavor`, `DeleteFlavor`, `CreateKeypair`.
- Load balancers: `ImportCertificate`, `DeleteCertificate`.
- Object storage: `UploadObject`, `DeleteObject`, `CopyObject`, `PresignGet`,
  `PresignPut`, `CreateCredential`, `RevokeCredential`.
- Kubernetes: `Preflight`, `CreateMaintenanceWindow`, `RotateKubeconfig`.

## Repeated writes

Every asynchronous write sends an idempotency key, so that a retry cannot queue
the same work twice. `TaskRef.Replayed` is true when the platform recognised a
repeat and returned the original task. See
[Errors and retries](errors-and-retries.md#idempotency-keys).
