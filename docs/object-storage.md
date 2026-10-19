# Object storage

Object storage is part of `client.Storage`. A container holds objects, each
identified by a key. The SDK can transfer objects through the API, hand out
presigned URLs for direct transfers, and issue S3 access keys scoped to one
container.

## Containers

```go
ref, err := client.Storage.CreateContainer(ctx, "reports")
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
```

`ListContainers` returns the containers of the project with their size
(`Bytes`) and object count (`Count`), and `GetContainer` one container by
name. `DeleteContainer` queues a deletion.

### Versioning and object lock

`CreateContainerWithOptions` creates a versioned or immutable container.
Protection can only be set at creation, because S3 does not allow adding object
lock to an existing bucket. A container for immutable backups must be created
with `ObjectLock` set:

```go
ref, err := client.Storage.CreateContainerWithOptions(ctx, nemau.CreateContainerRequest{
	Name:          "backups",
	ObjectLock:    true,
	RetentionMode: nemau.RetentionCompliance,
	RetentionDays: 30,
})
```

- `ObjectLock` makes stored versions immutable for `RetentionDays`. It implies
  `Versioning`.
- `RetentionMode` is `nemau.RetentionGovernance` (the default) or
  `nemau.RetentionCompliance`. A governance lock can be lifted by the platform.
  A compliance lock cannot be lifted by anyone until the retention period ends.
  No customer credential can lift either.
- `RetentionDays` is at most `nemau.MaxRetentionDays` (2555 days, seven years).
- A retention mode or period without `ObjectLock` is refused before the request
  is sent.

`Container.Protection` reports what was requested at creation.
`ContainerProtection` reads what the object store enforces, and `Immutable()`
is true when both versioning and object lock are in force:

```go
protection, err := client.Storage.ContainerProtection(ctx, "backups")
if err != nil {
	return err
}
fmt.Println(protection.Versioning, protection.RetentionMode, protection.RetentionDays, protection.Immutable())
```

`UpdateContainer` changes versioning and retention. The name cannot be
changed, and retention can only be extended through the API.

## Objects

Object transfers through the API are synchronous and limited to
`nemau.MaxObjectSize` (100 MiB) per object. For larger objects, use
[presigned URLs](#presigned-urls) or an [S3 access key](#s3-access-keys).

```go
f, err := os.Open("report.csv")
if err != nil {
	return err
}
defer f.Close()
info, err := f.Stat()
if err != nil {
	return err
}

uploaded, err := client.Storage.UploadObject(ctx, "reports", "2026/report.csv", "text/csv", f, info.Size())
```

- `UploadObject` needs the exact size in bytes. An empty content type is sent
  as `application/octet-stream`. The upload is not retried, because its body is
  a stream.
- `DownloadObject` returns an `io.ReadCloser` that the caller must close.
- `DeleteObject` removes one object.

### Listing objects

A listing is paged: 100 objects by default (`nemau.DefaultListObjectsLimit`),
and at most 1000 per call (`nemau.MaxListObjectsLimit`). `IterateObjects`
follows the pages for you:

```go
for obj, err := range client.Storage.IterateObjects(ctx, "photos", "2026/") {
	if err != nil {
		return err
	}
	fmt.Println(obj.Name, obj.Bytes)
}
```

Breaking out of the loop stops the listing: no further page is requested. An
error is yielded once and ends the sequence. Objects written during the walk
may or may not appear.

`ListObjects(ctx, container, prefix, marker, limit)` returns a single
`*nemau.ObjectPage`. Pass its `NextMarker` back as `marker` to read the next
page; it is empty on the last page. The marker is an opaque cursor: pass it back
unchanged. A `limit` above 1000 is refused before the request is sent.

### Copying an object

A copy happens inside the object store, within a container or into another
container of the same project. The data does not pass through the API or the
client, so `MaxObjectSize` does not apply. The limit is `nemau.MaxCopySize`
(5 GiB), the S3 limit for a single copy request; a larger object is refused
with a `413`. For larger objects, use an S3 access key with a tool such as
`rclone copy`.

```go
copied, err := client.Storage.CopyObject(ctx, nemau.CopyObjectRequest{
	Container:            "reports",
	Key:                  "2026.csv",
	DestinationContainer: "archive",
})
```

`DestinationContainer` defaults to the source container and `DestinationKey` to
the source key. The source's content type and metadata are kept unless
`ReplaceMetadata` is set, in which case `ContentType` and `Metadata` replace
them. Copying an object onto itself is accepted only with `ReplaceMetadata`:
it rewrites the metadata in place.

A copy into a container with object lock receives the destination container's
default retention, reported in `CopiedObject.Retention`. The source's retention
does not follow the copy.

## Presigned URLs

A presigned URL transfers one object directly to or from the object store,
without going through the API, so `MaxObjectSize` does not apply.

```go
presigned, err := client.Storage.PresignGet(ctx, "photos", "holiday.jpg", 0)
if err != nil {
	return err
}
body, size, err := client.FetchPresigned(ctx, presigned)
if err != nil {
	return err
}
defer body.Close()
```

- `PresignGet` returns a download URL, `PresignPut` an upload URL. Any HTTP
  client can use them.
- A zero expiry uses the platform default of 15 minutes. The longest accepted
  is `nemau.PresignTTLCap` (one hour).
- `FetchPresigned` does not send the client's credential: the URL is already
  authorized, and sending an access key to the object store would expose it to
  a host other than the API.

Treat a presigned URL as a secret: it is a bearer token for that object until
it expires.

## S3 access keys

For a migration or a backup, an S3 access key scoped to one container is more
practical than one URL per object. It works with `aws s3 sync`, `rclone sync`,
`s3cmd` and other S3 tools.

```go
credential, err := client.Storage.CreateCredential(ctx, "photos", nemau.CreateCredentialRequest{
	Access:    nemau.CredentialReadOnly,       // or nemau.CredentialReadWrite
	ExpiresAt: time.Now().Add(24 * time.Hour), // optional; zero means never
})
if err != nil {
	return err
}
fmt.Println(credential.AccessKeyID, credential.S3Bucket, credential.Endpoint)
```

- `SecretAccessKey` is returned only by this call. The platform does not store
  it: a caller that loses it must create a new key. Printing the value with
  `fmt` or `log/slog` redacts the secret; read the field explicitly when you
  need it.
- S3 tools address the bucket named in `credential.S3Bucket`. The object store
  gives each container an opaque bucket name that differs from the container
  name, and the key's policy covers that bucket only. `Container.S3Bucket`
  carries the same value.
- `nemau.CredentialReadOnly` lists and reads objects.
  `nemau.CredentialReadWrite` also writes and deletes them. A key never changes
  container settings.
- An empty `Endpoint` means the platform has no reachable S3 endpoint to show,
  as in a demonstration environment.

```sh
aws --endpoint-url "$ENDPOINT" s3 sync "s3://$S3_BUCKET" ./backup
```

Creating, listing and revoking keys requires the `storage:ManageCredentials`
permission. `ListCredentials(ctx, container)` returns the keys without their
secrets, and `RevokeCredential(ctx, container, accessKeyID)` deletes one. Each
container holds a limited number of keys. When the limit is reached,
`nemau.IsCredentialLimit(err)` is true, and `nemau.CredentialLimit(err)`
returns the limit and what it counts (`bucket` or `project`):

```go
if limit, scope, ok := nemau.CredentialLimit(err); ok {
	log.Printf("key limit reached: %d per %s", limit, scope)
}
```
