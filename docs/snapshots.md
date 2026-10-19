# Snapshots

A snapshot is a crash-consistent copy of an instance's system disk, stored as a
compressed archive in the platform's object store. It is taken without stopping
the instance. Block volumes attached to the instance are never included.

Snapshots are methods of `client.Compute`.

## Creating a snapshot

```go
ref, err := client.Compute.CreateSnapshot(ctx, instanceID, "before-upgrade")
if err != nil {
	return err
}
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
fmt.Println(task.ResultString("snapshot_id"))
```

The task runs until the disk has reached the object store, which takes minutes,
or hours for a large disk. In the meantime the snapshot is listed with the
status `creating`. The task result carries `snapshot_id` and `size_bytes`.

## Listing snapshots

`ListSnapshots` returns the snapshots of the project. A non-empty instance id
narrows the result to one instance:

```go
snapshots, err := client.Compute.ListSnapshots(ctx, instanceID)
if err != nil {
	return err
}
for _, s := range snapshots {
	fmt.Println(s.ID, s.Name, s.Status, s.Restorable)
}
```

`Snapshot.Status` is `creating`, `active` or `restoring`.

## Billing

A snapshot is billed per GB and month on the size of its compressed archive.
Once the snapshot is active:

- `SizeBytes` is the size of the archive, which is what is billed;
- `SourceDiskGB` is the capacity of the disk that was captured;
- `MonthlyCost` is the price, excluding VAT, of keeping the archive for a
  730-hour month, in `Currency`.

These fields are nil while the snapshot is being created.

## Restoring a snapshot

`RestoreSnapshot` puts a snapshot back on the instance it was taken from. The
data is first restored beside the running instance, then the system disk is
swapped: the instance stops only for the swap, and starts again if it was
running.

```go
ref, err := client.Compute.RestoreSnapshot(ctx, snapshotID)
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
```

Everything written since the snapshot is lost. Addresses, security groups and
attached volumes are kept, and the disk keeps its current size. Only snapshots
with `Restorable` set can be restored.

## Deleting a snapshot

`DeleteSnapshot` queues the deletion. A snapshot that is being created or
restored cannot be deleted: the API answers `409`. Deleting an instance deletes
its snapshots.

## Older snapshots

Older snapshots report `Storage == "hypervisor"` instead of `object_store`. They
can be listed and deleted, are not billed, and cannot be restored.
