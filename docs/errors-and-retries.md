# Errors and retries

This page describes the errors the SDK returns, how it retries failed requests,
and how idempotency keys make a write safe to repeat.

## API errors

Every non-2xx response is returned as a `*nemau.APIError`:

| Field | Content |
| --- | --- |
| `StatusCode` | The HTTP status. |
| `Message` | The server's error text. An expected refusal carries a specific message, such as `project quota exceeded for instances: 10/10 in use`; any other error carries a generic one. It is safe to print. |
| `Reason` | A machine-readable reason some refusals carry, such as `permission_denied` or `subnet_overlap`. |
| `Code` | A stable cause some refusals carry: `credential_limit`, `organization_read_only` or `invalid_request`. |
| `Limit`, `Scope` | The cap and what it counts, on a `credential_limit` refusal. |
| `Body` | The raw response body, truncated to 4096 bytes. |
| `Method`, `Path` | The call that failed. |
| `RetryAfter` | The delay the server asked for in `Retry-After`, or zero. |

Branch on `StatusCode`, `Code` or `Reason`. The wording of `Message` may
change.

```go
var apiErr *nemau.APIError
if errors.As(err, &apiErr) {
	log.Printf("status %d, code %q, reason %q: %s",
		apiErr.StatusCode, apiErr.Code, apiErr.Reason, apiErr.Message)
}
```

These helpers wrap the `errors.As` check:

| Helper | True for |
| --- | --- |
| `nemau.IsNotFound(err)` | A `404`. |
| `nemau.IsUnauthorized(err)` | A `401`: the credential is missing, invalid or expired. |
| `nemau.IsForbidden(err)` | A `403`: the credential is valid but not allowed, because of an IAM policy, a token scope or a missing organization feature. |
| `nemau.IsSubnetOverlap(err)` | The `409` refusing a subnet whose range overlaps another subnet of the project. See [Networking](networking.md#subnets). |
| `nemau.IsCertificateInUse(err)` | The `409` refusing to delete a certificate a listener still serves. |
| `nemau.IsCredentialLimit(err)` | The `429` refusing a new S3 access key because the limit is reached. `nemau.CredentialLimit(err)` returns the limit and its scope. |
| `nemau.IsOrganizationReadOnly(err)` | The `403` returned while the organization is in its read-only phase. |
| `nemau.IsIdempotencyKeyMismatch(err)` | The `422` refusing an idempotency key already used with different parameters. |
| `nemau.HasReason(err, reason)` | Any `APIError` whose `Reason` is `reason`, for example `nemau.ReasonDNSZoneReserved`. |

`APIError` also has the methods `NotFound()`, `Unauthorized()`, `Forbidden()`
and `Retryable()`.

Some `Get` methods, such as `GetNetwork` and `GetVolume`, list the resources
and filter them, because the API has no read for a single resource of that
kind. A missing id still returns a `404` `*nemau.APIError`, so `IsNotFound`
works the same way.

The SDK checks some arguments before sending anything: an empty `Update`, an
invalid idempotency key, an out-of-range listing limit, an unknown metrics
period. These errors are plain errors whose text starts with `nemau:`. They are
not `APIError` values.

## Failed tasks

`WaitFor` returns a `*nemau.TaskFailedError` when a task ends with the status
`failed` or `cancelled`. Its `Task` field holds the task. For most failures the
platform reports a fixed message, so the error text includes the task id: give
it to support when you ask for help.

```go
_, err := client.WaitFor(ctx, ref)
var failed *nemau.TaskFailedError
if errors.As(err, &failed) {
	log.Printf("task %s failed", failed.Task.ID)
}
```

When the platform refuses a delete because something still depends on the
resource, such as a volume attached to the instance or a subnet on the network,
the task fails with `ErrorCode` set to `resource_in_use`
(`nemau.ErrorCodeResourceInUse`) and a message that names what to remove first.
`nemau.IsResourceInUse(err)` recognises it. Repeating the call cannot succeed
until the dependent resource is gone.

```go
ref, err := client.Network.DeleteNetwork(ctx, networkID)
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
if nemau.IsResourceInUse(err) {
	// The message names the dependent resource. Remove it, then delete again.
}
```

## Retries

The default policy, `nemau.DefaultRetryPolicy()`, makes up to 4 attempts. It
retries on `429`, on `5xx` and on transport errors, with an exponential backoff
that starts at 300 ms, is capped at 5 s, and uses full jitter.

- Reads are always retried.
- Asynchronous writes are retried, because each one carries an idempotency key.
- Synchronous writes, such as `ImportCertificate`, `CreateKeypair` or
  `DeleteObject`, carry no key and are not retried after a `429` or a `5xx`.
  After a transport error, a synchronous `PUT`, `PATCH` or `DELETE` is retried
  and a `POST` is not.
- `UploadObject` is never retried, because its body is a stream that cannot be
  read twice. `DownloadObject` and `FetchPresigned` make a single attempt.

When every attempt fails, the error of the last one is returned.

`nemau.WithRetry` replaces the policy:

```go
policy := nemau.DefaultRetryPolicy()
policy.MaxAttempts = 6
client, err := nemau.NewClient(nemau.WithRetry(policy))
```

`nemau.WithRetry(nemau.RetryPolicy{MaxAttempts: 1})` turns retries off. In a
`RetryPolicy`, a zero `BaseDelay`, `MaxDelay` or `MaxRetryAfter` uses the
default value.

## Rate limits

When the API limits a client, it answers `429` with a `Retry-After` header. A
`503` can carry one too. The client waits at least that long before the next
attempt, and `APIError.RetryAfter` holds the delay. A delay
longer than `RetryPolicy.MaxRetryAfter` (60 s unless set) is not waited out:
the error is returned at once, and the caller decides when to try again.

```go
var apiErr *nemau.APIError
if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
	log.Printf("rate limited, retry in %s", apiErr.RetryAfter)
}
```

## Idempotency keys

Every asynchronous write sends an `Idempotency-Key` header: a new UUID v4 for
each call, reused on each retry of that call. The platform keeps the key for 24
hours and answers a repeat with the original task instead of queueing the work
a second time. `ref.Replayed` is true when that happened.

Pass your own key when the retry happens outside the SDK, for example a job
that may run twice or a pipeline step run again after a timeout:

```go
keyCtx := nemau.WithIdempotencyKey(ctx, "deploy-4821-create-network")
ref, err := client.Network.CreateNetwork(keyCtx, "prod", "")
```

A key is 1 to 128 characters of letters, digits, `.`, `_`, `:` and `-`. Any
other value fails before the request is sent. Use one key for one write only,
and do not share a context carrying a key between different writes. Reusing a
key with different parameters is refused with a `422`, which
`nemau.IsIdempotencyKeyMismatch(err)` recognises.
