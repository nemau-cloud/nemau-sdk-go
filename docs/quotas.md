# Quotas

Every write is checked against capacity limits before the API accepts it.
`client.Quotas.Get` returns the limits, the usage and the pending reservations,
so that a caller can size a request before sending it.

Limits apply at two scopes: the project the credential resolves to
(`nemau.QuotaScopeProject`) and the organization that owns it
(`nemau.QuotaScopeTenant`). A write is accepted only when both have room.

```go
quotas, err := client.Quotas.Get(ctx)
if err != nil {
	return err
}
if cores := quotas.Binding(nemau.QuotaCores); cores != nil && cores.Available() < 4 {
	return fmt.Errorf("only %d vCPUs left", cores.Available())
}
```

`Binding(name)` returns the dimension with the least headroom across both
scopes, which is the limit a write reaches first. It returns nil when no scope
reports the dimension. `Scope(name)` returns one scope, and
`QuotaScope.Dimension(name)` one dimension of that scope.

## Dimensions

Each `QuotaDimension` has a `Name`, a `Unit` (`count`, `MB`, `GB` or `bytes`),
`Used`, `Reserved` and `Limit`. `Reserved` is capacity held by accepted tasks
that have not finished yet. The API compares used, reserved and requested
capacity with the limit, and `Available()` returns the headroom that remains,
never negative.

| Constant | Name |
| --- | --- |
| `nemau.QuotaInstances` | `instances` |
| `nemau.QuotaCores` | `cores` |
| `nemau.QuotaRAM` | `ram` (in MB) |
| `nemau.QuotaVolumes` | `volumes` |
| `nemau.QuotaVolumeSize` | `volumeSize` (in GB) |
| `nemau.QuotaSnapshots` | `snapshots` |
| `nemau.QuotaNetworks` | `networks` |
| `nemau.QuotaSubnets` | `subnets` |
| `nemau.QuotaFloatingIPs` | `floatingIps` |
| `nemau.QuotaSecurityGroups` | `securityGroups` |
| `nemau.QuotaLoadBalancers` | `loadBalancers` |
| `nemau.QuotaObjectStorage` | `objectStorage` |

A write refused for lack of quota returns an `*nemau.APIError` whose `Message`
names the dimension, for example
`project quota exceeded for instances: 10/10 in use`.
