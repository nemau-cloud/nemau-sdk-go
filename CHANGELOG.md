# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/) (0.x: a minor
release may change the API).

## [Unreleased]

### Added

- `CreateInstanceRequest.UserData`, an optional cloud-init document given at
  creation (16 KiB at most, checked before sending). `Instance.UserDataSHA256`
  reports its SHA-256; the document is never returned.
- Scheduled backups of instances. `Update` gains `BackupSchedule`,
  `BackupHour`, `BackupWeekday` and `BackupRetentionDays`, which
  `UpdateInstance` sends. `Instance` reports `BackupSchedule` (always) and the
  other three when set. `BackupNone`, `BackupDaily` and `BackupWeekly` name the
  schedules.
- `ValidateBackupPolicy` checks those fields against their ranges before a
  request is sent. `UpdateInstance` calls it.
- `Snapshot.Origin` (`SnapshotOriginManual` or `SnapshotOriginScheduled`) and
  `Snapshot.ExpiresAt`, set on scheduled snapshots only.
- `DatabasesService` (`Client.Databases`): managed PostgreSQL. `List`, `Get`,
  `Create`, `Update`, `Resize`, `RotatePassword`, `Restore`, `Delete` and
  `CACertificate`. `Create`, `RotatePassword` and `Restore` return a
  `DatabaseCredentials` that holds the administrator password once. The type
  redacts the password in `String`, `GoString` and `MarshalJSON`.
- `InstanceGroupsService` (`Client.InstanceGroups`): instance groups with
  `List`, `Get`, `Activities`, `Create`, `Update`, `SetCapacity`,
  `SetMemberProtection` and `Delete`. A launch template takes `UserData`,
  checked before sending; the platform never returns it.
- Comfort checks in both services mirror the platform's ranges. A request that
  breaks one is refused before it leaves the process.
- Password-issuing writes send no `Idempotency-Key` and are not retried after a
  transport failure or a 429: the platform refuses the key on those routes, and
  a repeat could create a second database.
- `DatabasesService.Restart` queues a restart of a database (`202`, a task).
- `DatabasesService.Flavors` and `DatabasesService.Versions` list the region's
  database flavors (`DatabaseFlavor`: name, vCPUs, memory in MB) and PostgreSQL
  versions.
- `UpdateDatabaseRequest.ClearMaintenanceWindow` removes a database's
  maintenance window: the request sends an explicit `null`. Setting and clearing
  it in one request is refused before sending.
- `InstanceGroupsService.Refresh` starts a rolling refresh with optional
  `max_unavailable_pct` (0 to 50) and `max_surge` (1 to 10); an absent share
  keeps the group's stored setting. `InstanceGroupsService.CancelRefresh`
  cancels the refresh in progress.
- `InstanceGroup.Refresh` reports the last refresh (`InstanceGroupRefresh`):
  status (`Refresh*` constants), target version, start and completion times,
  and the number of protected members it skipped.
- A guide in `docs/`, with one page per topic and per service. The README now
  covers installation and a first program, and links to the guide.
- `CONTRIBUTING.md` and `SECURITY.md`.
- `KubernetesClusterSpec` decodes `NetworkID`, `SubnetID`, `PodCIDR` and
  `ServiceCIDR` as the cluster read view carries them.
- `AddNodePoolRequest.Labels` and `Taints`, checked as `NodePoolInit` checks
  them before `AddNodePool` sends anything.

### Changed

- **Breaking change:** `CreateClusterRequest` requires `NetworkID` and `SubnetID`
  (the UUIDs of the project network and of the subnet the nodes live in).
  `CreateCluster` and `CreateClusterAndWait` refuse a request without them
  before sending anything, so existing callers must add both fields.
  `PodCIDR` and `ServiceCIDR` are new optional fields, sent only when set.
- `CreateCluster` checks each node pool's `Class`, `Labels` and `Taints` before
  sending, and refuses a malformed value with an error naming the pool. The
  platform enforces the same rules; this only fails earlier.
- `DefaultBaseURL` is now `https://api.nemau-cloud.com`, the production
  API. Set `NEMAU_API_URL` or `WithBaseURL` to target another environment.
- Projects and quotas are read from `/projects` and `/quotas`. The former
  `/orchestration/projects` and `/orchestration/quotas` paths are gone.

## [0.5.0]

### Added

- Idempotency keys. Every asynchronous write sends an `Idempotency-Key` header:
  a fresh UUID v4 per call, reused on each retry of that call. `WithIdempotencyKey(ctx, key)`
  sets your own (1 to 128 characters of letters, digits, `.`, `_`, `:` and `-`),
  `TaskRef.Replayed` reports a replay, and `IsIdempotencyKeyMismatch(err)`
  recognises the `422` the platform answers when a key is reused with
  different parameters.
- `StorageService.IterateObjects` walks a container listing across pages with
  the platform's opaque `next_marker` cursor. `ListObjects` returns a single
  page; a `limit` above 1000 is refused before the request is sent.
- `ValidateMetricsPeriod` and `MetricsPeriods`: `InstanceMetrics` accepts
  `1h`, `6h`, `24h` and `7d` only, and refuses anything else before the request
  is sent.
- `KubernetesService.UpgradeCluster(ctx, name, version)` runs the guided upgrade
  (preflight, maintenance window, rolling upgrade) as one task.

### Changed

- Retries honour `Retry-After` on `429` and `503`, up to
  `RetryPolicy.MaxRetryAfter` (60 s unless set); a longer delay is returned
  at once as an `*APIError` carrying `RetryAfter`.
- Retries are limited to reads and to writes that carry an `Idempotency-Key`.
  The synchronous writes (certificate import, keypair creation, object delete)
  and object uploads are not retried after a `429` or `5xx`.
- `Version` now reports the release (it was still `0.1.0`), so the
  `User-Agent` identifies the SDK correctly.

### Removed

- `PreflightBlockedError`: the SDK no longer runs the preflight and opens the
  maintenance window itself; the upgrade task does both. A blocked preflight
  fails the upgrade task and is returned as a `*TaskFailedError`.

### Notes

- Access keys expire: the console issues them for 90 days by default and 365
  days at most, and a user can hold five active keys. After expiry the API
  answers `401`; create the next key before the current one expires.
