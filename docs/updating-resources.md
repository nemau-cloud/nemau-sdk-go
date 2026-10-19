# Updating resources

Names, descriptions and some settings can be changed in place with the
`Update...` methods. Each one takes a `nemau.Update`, sends it as a `PATCH`, and
returns a `*nemau.TaskRef` like any other asynchronous write.

The fields of `Update` are pointers. A nil field is left out of the request and
keeps its current value, so changing one field does not clear the others,
including a field another client has set in the meantime. `nemau.Set` returns a
pointer to its argument:

```go
ref, err := client.Network.UpdateNetwork(ctx, networkID, nemau.Update{
	Description: nemau.Set("front segment"),
})
if err != nil {
	return err
}
if _, err := client.WaitFor(ctx, ref); err != nil {
	return err
}
```

`nemau.Set("")` clears a text field. An `Update` with no field set is refused
before any request is sent.

## Fields by method

| Method | Fields it changes |
| --- | --- |
| `Compute.UpdateInstance` | `Name`, `Description`. The instance keeps running. |
| `Network.UpdateNetwork` | `Name`, `Description`. Attached instances are not affected. |
| `Network.UpdateSubnet` | `Name` |
| `Network.UpdateSecurityGroup` | `Name`, `Description` |
| `Network.UpdateFloatingIP` | `Description`. The address cannot change. |
| `LoadBalancer.UpdateLoadBalancer` | `Name`, `Description` |
| `LoadBalancer.UpdateListener` | `Name`, `Description`, `ConnectionLimit`, `TLSCertificateRef` |
| `LoadBalancer.UpdatePool` | `Name`, `Description`, `LBAlgorithm` |
| `LoadBalancer.UpdatePoolMember` | `Name`, `Weight` |
| `LoadBalancer.UpdateHealthMonitor` | `Delay`, `Timeout`, `MaxRetries`, `ExpectedCodes` |
| `Storage.UpdateVolume` | `Name`, `Description` |
| `Storage.UpdateContainer` | `Versioning`, `RetentionDays`. Retention can only be extended. |
| `DNS.UpdateZone` | `Description`, `Email` (the SOA contact), `TTL` |

Some values never change after creation: a container's name, a zone's name, a
load balancer's tier. Security group rules cannot be edited: delete a rule and
create a new one.

Changing a pool's algorithm or a listener's settings rewrites the load
balancer's configuration in place. Established connections are kept.

## Other changes

Some changes have their own methods:

| Change | Method |
| --- | --- |
| Instance sizing | `Compute.Resize`, see [Compute](compute.md#resizing) |
| Instance security groups | `Compute.SetSecurityGroups`, see [Compute](compute.md#security-groups) |
| Volume size | `Storage.ExtendVolume`, see [Block storage](block-storage.md) |
| DNS records | `DNS.UpdateRecordSet`, see [DNS](dns.md#record-sets) |
| Flavor definition | `Compute.UpdateFlavor`, see [Compute](compute.md#flavors) |
| Kubernetes topology and version | `ScaleControlPlane`, `ScaleNodePool`, `SetAutoscaler`, `UpgradeCluster`, see [Kubernetes](kubernetes.md) |
| Egress filtering on an older security group | `Network.EnableSecurityGroupEgressFiltering`, see [Networking](networking.md#egress-filtering) |
