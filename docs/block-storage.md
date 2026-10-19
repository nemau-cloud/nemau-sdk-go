# Block storage

Block volumes are methods of `client.Storage`. A volume is a block device that
can be attached to an instance. Sizes are in GiB.

## Creating a volume

```go
volume, err := client.Storage.CreateVolumeAndWait(ctx, "data", "database files", 100)
if err != nil {
	return err
}
fmt.Println(volume.ID, volume.Size, volume.Status)
```

`CreateVolume` queues the creation without waiting. `ListVolumes` returns the
volumes of the project, and `GetVolume` one volume, with its `Attachments`.

## Attaching and detaching

`AttachVolume` queues the attachment of a volume to an instance. The completed
task's result carries `device_id`, the stable `/dev/disk/by-id` name of the
volume inside the guest:

```go
ref, err := client.Storage.AttachVolume(ctx, volume.ID, instanceID)
if err != nil {
	return err
}
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
fmt.Println(task.ResultString("device_id"))
```

`DetachVolume(ctx, volumeID, instanceID)` queues the detachment. An instance
with an attached volume cannot be deleted: the delete task fails with
`resource_in_use` (see
[Errors and retries](errors-and-retries.md#failed-tasks)).

## Growing a volume

`ExtendVolume` grows a volume to a new size in GiB. Volumes only grow.

```go
ref, err := client.Storage.ExtendVolume(ctx, volume.ID, 200)
```

`UpdateVolume` changes the name and description (see
[Updating resources](updating-resources.md)), and `DeleteVolume` queues the
deletion.
