# nemau-sdk-go guide

This guide explains how to use nemau-sdk-go, the Go client for the Nemau Cloud
public API. The examples assume the package is imported as `nemau`:

```go
import nemau "github.com/nemau-cloud/nemau-sdk-go"
```

They also assume a `client` built with `nemau.NewClient` and a `ctx` of type
`context.Context`. The API reference generated from the package documentation
is on [pkg.go.dev](https://pkg.go.dev/github.com/nemau-cloud/nemau-sdk-go).

## Using the client

- [Authentication](authentication.md): credentials and the environment
  variables `NewClient` reads.
- [Client configuration](configuration.md): base URL, HTTP client, user agent
  and poll interval.
- [Organizations and projects](projects.md): which project a request acts on,
  and how to change it.
- [Asynchronous writes](asynchronous-writes.md): tasks, waiting, timeouts and
  the `AndWait` methods.
- [Updating resources](updating-resources.md): partial updates with
  `nemau.Update` and `nemau.Set`.
- [Errors and retries](errors-and-retries.md): error types, the retry policy,
  rate limits and idempotency keys.

## Services

- [Compute](compute.md): instances, power actions, images, flavors, keypairs,
  pricing and metrics.
- [Snapshots](snapshots.md): copies of an instance's system disk, and restore.
- [Networking](networking.md): networks, subnets, floating IPs, security groups
  and IPv6.
- [Load balancers](load-balancers.md): tiers, listeners, pools, members, health
  monitors and TLS certificates.
- [Block storage](block-storage.md): volumes.
- [Object storage](object-storage.md): containers, objects, presigned URLs and
  S3 access keys.
- [DNS](dns.md): zones, ownership verification and record sets.
- [Kubernetes](kubernetes.md): clusters, node pools, kubeconfig and upgrades.
- [Quotas](quotas.md): limits, usage and headroom.
- [Metering](metering.md): consumption and cost.
- [Regions](regions.md): regions and their health.

## Example program

[`examples/provision`](../examples/provision) creates a network, a subnet, a
security group and an instance, then deletes them:

```sh
go run ./examples/provision -image ubuntu-24.04
```

Pass `-keep` to leave the resources in place.
