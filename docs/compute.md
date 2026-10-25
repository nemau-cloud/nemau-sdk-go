# Compute

`client.Compute` manages instances, their power state, images, flavors, SSH
keypairs, the rate card and instance metrics. Snapshots have their own page:
[Snapshots](snapshots.md).

## Instances

`ListInstances` returns the instances of the project, and `GetInstance` one
instance by id. An unknown id returns a `404` `*nemau.APIError`.

```go
instance, err := client.Compute.GetInstance(ctx, instanceID)
if err != nil {
	return err
}
fmt.Println(instance.Name, instance.Status, instance.Sizing.VCPUs, instance.Addresses)
```

`Instance.Status` is `running`, `stopped`, `paused` or `pending`. Fields the
platform cannot report are left empty: an empty `Addresses` means the addresses
are unknown, and `Created` is nil when the creation time is unknown.

### Creating an instance

`CreateInstance` queues the creation and returns a `*nemau.TaskRef`;
`CreateInstanceAndWait` also waits and returns the new instance.

```go
instance, err := client.Compute.CreateInstanceAndWait(ctx, nemau.CreateInstanceRequest{
	Name:           "web-1",
	ImageID:        "ubuntu-24.04",
	VCPUs:          2,
	RAMMB:          4096,
	DiskGB:         40,
	NetworkID:      networkID,
	SecurityGroups: []string{groupID},
	KeyName:        "deploy",
})
```

- Size the instance with `FlavorID`, or with all three of `VCPUs`, `RAMMB` and
  `DiskGB`. The API refuses a partial custom size. `FlavorID` requires the
  compute-flavors organization feature.
- `ImageID` comes from `ListImages`.
- An instance is attached to one network at creation. `NetworkIDs` is accepted,
  but only its first entry is used: prefer `NetworkID`.
- `KeyName` is the name of a keypair (see [Keypairs](#keypairs)).
- `UserData` is an optional cloud-init document that runs at the guest's first
  boot. It must start with `#cloud-config`, `#!` or a MIME multipart header, be
  valid UTF-8 without NUL bytes, and be at most `MaxUserDataBytes` (16 KiB). The
  SDK checks these before sending. The API never returns the document, and it
  cannot be changed after creation: `Instance.UserDataSHA256` holds its hex
  SHA-256 so that you can detect a change without storing the document.
  The document may contain secrets; do not log it.

`DeleteInstance` queues the deletion. It fails with `resource_in_use` while a
volume is attached to the instance (see
[Errors and retries](errors-and-retries.md#failed-tasks)). Deleting an instance
also deletes its snapshots.

### Power actions

Each action queues a task:

| Method | Effect |
| --- | --- |
| `Start(ctx, id)` | Powers the instance on. |
| `Stop(ctx, id)` | Powers the instance off. |
| `Reboot(ctx, id, rebootType)` | Restarts the instance. `rebootType` is `SOFT` (the default when empty) or `HARD`. |
| `Pause(ctx, id)`, `Unpause(ctx, id)` | Freezes the instance in memory, and resumes it. |
| `Suspend(ctx, id)`, `Resume(ctx, id)` | Writes the memory to disk and stops the instance, and restores it. |

```go
ref, err := client.Compute.Reboot(ctx, instanceID, "HARD")
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
```

### Resizing

`Resize` changes the sizing. All three values are required, and the API bounds
them: 1 to 64 vCPUs, 1024 to 262144 MB of RAM, 10 to 5000 GB of disk.

```go
ref, err := client.Compute.Resize(ctx, instanceID, nemau.Sizing{
	VCPUs:  4,
	RAMMB:  8192,
	DiskGB: 80,
})
```

### Security groups

`SetSecurityGroups` replaces the instance's security groups with the given
list. It does not add to the current groups. An empty or nil slice removes all
the groups, which stops filtering the instance's traffic.

```go
ref, err := client.Compute.SetSecurityGroups(ctx, instanceID, []string{webGroupID, sshGroupID})
```

The name and description of an instance change with `UpdateInstance` (see
[Updating resources](updating-resources.md)). So does its backup policy, see
[Scheduled backups](snapshots.md#scheduled-backups).

## Images

`ListImages` returns the bootable images, with their `ID`, `Name`, `OSName`,
`Status` and `DiskFormat`.

```go
images, err := client.Compute.ListImages(ctx)
if err != nil {
	return err
}
for _, image := range images {
	fmt.Println(image.ID, image.OSName)
}
```

## Flavors

A flavor is a named instance size defined by the organization. Flavors require
the compute-flavors organization feature; without it, these methods return a
`403`. Unlike most writes, flavor writes are synchronous.

```go
flavor, err := client.Compute.CreateFlavor(ctx, nemau.FlavorRequest{
	Name:   "standard-2",
	VCPUs:  2,
	RAMMB:  4096,
	DiskGB: 40,
})
if err != nil {
	return err
}
```

`ListFlavors` returns the flavors, `UpdateFlavor` replaces a flavor's
definition, and `DeleteFlavor` removes one.

## Keypairs

`CreateKeypair` registers an SSH public key. With an empty public key, the
platform generates an Ed25519 keypair. The call is synchronous because the
generated private key is only available in its response: the platform does not
store it, so save `PrivateKey` at once.

```go
keypair, err := client.Compute.CreateKeypair(ctx, "deploy", "")
if err != nil {
	return err
}
if err := os.WriteFile("deploy.pem", []byte(keypair.PrivateKey), 0o600); err != nil {
	return err
}
```

`ListKeypairs` returns the project's keypairs. `DeleteKeypair` takes the
keypair's name and queues the deletion. A name that contains a slash cannot be
deleted through the API, so avoid slashes in keypair names.

## Pricing

`GetPricing` returns the region's rate card as a `map[string]any`.

## Metrics

`InstanceMetrics` returns an instance's usage metrics as a `map[string]any`.
The period is `1h` (the default when empty), `6h`, `24h` or `7d`
(`nemau.MetricsPeriods`). Any other value is refused before the request is sent;
`nemau.ValidateMetricsPeriod` runs the same check.

```go
metrics, err := client.Compute.InstanceMetrics(ctx, instanceID, "24h")
```
