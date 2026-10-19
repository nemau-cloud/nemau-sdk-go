# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/) (0.x: a minor
release may change the API).

## [Unreleased]

### Added

- A guide in `docs/`, with one page per topic and per service. The README now
  covers installation and a first program, and links to the guide.
- `CONTRIBUTING.md` and `SECURITY.md`.

### Changed

- `DefaultBaseURL` is now `https://api.nemau-cloud.com`, the production
  API. Set `NEMAU_API_URL` or `WithBaseURL` to target another environment.

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
