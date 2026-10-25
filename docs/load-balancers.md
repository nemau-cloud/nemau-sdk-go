# Load balancers

`client.LoadBalancer` manages load balancers, listeners, pools, pool members,
health monitors and the TLS certificates that listeners serve.

A load balancer receives traffic on a virtual IP address (VIP). A listener is a
front-end port on that address. It sends connections to a pool, whose members
are the backends, and health monitors probe the members.

## Tiers

`CreateLoadBalancerRequest.Flavor` picks the tier. An empty value means
`lb-small`, which runs one appliance. `lb-ha`, `lb-medium` and `lb-large` run
two appliances on two hosts, active and standby behind the same VIP.
`ListFlavors` returns the tiers with their sizing and price:

```go
tiers, err := client.LoadBalancer.ListFlavors(ctx)
if err != nil {
	return err
}
for _, t := range tiers {
	fmt.Println(t.Name, t.Appliances, t.VCPUs, t.RAMMB, t.MonthlyPrice, t.Currency)
}
```

`VCPUs` and `RAMMB` size each appliance. A high-availability tier is billed as
one load balancer. The tier is fixed at creation: to change it, create a new
load balancer.

## Creating a load balancer

`VipSubnetID` is required: the VIP is allocated inside that subnet.

```go
lb, err := client.LoadBalancer.CreateAndWait(ctx, nemau.CreateLoadBalancerRequest{
	Name:        "web",
	VipSubnetID: subnetID,
	Flavor:      "lb-ha",
})
if err != nil {
	return err
}
fmt.Println(lb.ID, lb.VipAddress)
```

`Create` queues the creation without waiting. `List`, `Get`,
`UpdateLoadBalancer` and `Delete` complete the set. `Get` returns the load
balancer with its listeners and pools.

Every new load balancer runs in automatic mode and serves HTTP, HTTPS, TCP and
UDP listeners side by side. `LoadBalancer.Provider` reports the engine: new
load balancers report `auto`, and an older one may report `haproxy` (no UDP
listeners) or `nginx` (UDP listeners only).

## High availability

On a high-availability tier, `LoadBalancer.HighAvailability` reports the state
of the pair. It is nil on a single-appliance tier.

- `Status` is `REDUNDANT` (one active and one standby appliance, on two hosts),
  `DEGRADED` or `UNKNOWN`.
- `Reason` explains a `DEGRADED` pair: `SPLIT_BRAIN`, `NO_ACTIVE_APPLIANCE`,
  `APPLIANCE_UNAVAILABLE` or `SINGLE_HOST` (both appliances share one host).
- `Appliances` lists each appliance with its `State` (`ACTIVE`, `STANDBY`,
  `DOWN` or `UNKNOWN`) and its `Address`.

Health checks and proxied connections leave from each appliance's own address
on the VIP subnet. The security groups of the members must allow both
appliances' addresses.

## Listeners, pools and members

```go
ref, err := client.LoadBalancer.CreatePool(ctx, lb.ID, nemau.CreatePoolRequest{
	Name:        "web",
	Protocol:    "HTTP",
	LBAlgorithm: "ROUND_ROBIN",
})
if err != nil {
	return err
}
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
if task.ResourceID == nil {
	return fmt.Errorf("task %s returned no pool id", task.ID)
}
poolID := *task.ResourceID

ref, err = client.LoadBalancer.AddPoolMember(ctx, poolID, nemau.AddPoolMemberRequest{
	Name:         "web-1",
	Address:      "10.0.1.10",
	ProtocolPort: 8080,
})
if err != nil {
	return err
}
if _, err := client.WaitFor(ctx, ref); err != nil {
	return err
}

ref, err = client.LoadBalancer.CreateListener(ctx, lb.ID, nemau.CreateListenerRequest{
	Name:          "http",
	Protocol:      "HTTP",
	ProtocolPort:  80,
	DefaultPoolID: poolID,
})
```

- Listener protocols are `HTTP`, `HTTPS` (passed through to the members),
  `TERMINATED_HTTPS` (decrypted on the load balancer, see
  [TLS certificates](#tls-certificates)), `TCP` and `UDP`.
- `LBAlgorithm` is `ROUND_ROBIN`, `LEAST_CONNECTIONS` or `SOURCE_IP`.
- A member's `Weight` defaults to 1.
- `AdminStateUp` is a pointer, so that an absent value and `false` stay
  distinct. The API treats an absent value as `true`.

`ListListeners`, `ListPools`, `GetListener` and `GetPool` take the load
balancer id. `ListPoolMembers`, `GetPoolMember`, `UpdatePoolMember` and
`RemovePoolMember` take the pool id. `UpdateListener`, `UpdatePool`,
`DeleteListener` and `DeletePool` act on one listener or pool (see
[Updating resources](updating-resources.md)).

## Health monitors

```go
ref, err := client.LoadBalancer.CreateHealthMonitor(ctx, poolID, nemau.CreateHealthMonitorRequest{
	Type:          "HTTP",
	Delay:         5,
	Timeout:       3,
	MaxRetries:    3,
	HTTPMethod:    "GET",
	URLPath:       "/healthz",
	ExpectedCodes: "200",
})
```

`Type` is `HTTP`, `HTTPS`, `TCP` or `UDP_CONNECT`, and the HTTP fields apply to
the HTTP types only. UDP pools report `UDP_CONNECT`: failures on them are
detected passively, without an active probe.

`ListPoolHealthMonitors`, `GetHealthMonitor`, `UpdateHealthMonitor` and
`DeleteHealthMonitor` take the pool id.

## TLS certificates

A `TERMINATED_HTTPS` listener decrypts traffic on the load balancer with a
certificate imported into the project. The import is synchronous, and the
private key cannot be read back:

```go
cert, err := client.LoadBalancer.ImportCertificate(ctx, nemau.ImportCertificateRequest{
	Name:        "www",
	Certificate: string(fullchainPEM), // leaf first, chain optional
	PrivateKey:  string(keyPEM),
})
if err != nil {
	return err
}

ref, err := client.LoadBalancer.CreateListener(ctx, lbID, nemau.CreateListenerRequest{
	Name:              "https",
	Protocol:          "TERMINATED_HTTPS",
	ProtocolPort:      443,
	DefaultPoolID:     poolID,
	TLSCertificateRef: cert.ID,
})
```

`Certificate` holds the PEM leaf, optionally followed by its chain, and `Chain`
adds intermediates. The private key must not be protected by a passphrase.
Accepted keys are RSA from 2048 to 4096 bits, and ECDSA P-256 and P-384.
`TLSCertificateRef` is required for `TERMINATED_HTTPS` and refused for other
protocols.

A refused import is an `*nemau.APIError` whose `Reason` names the cause. The
`nemau.ReasonCertificate...` constants list the possible values, such as
`ReasonCertificateKeyMismatch` and `ReasonCertificateExpired`. Test for one
with `nemau.HasReason`.

`ListCertificates` returns the certificates with the listeners that serve them
(`UsedBy`), and `GetCertificate` one certificate, with its domains, issuer,
validity dates and SHA-256 fingerprint.

### Renewing a certificate

A certificate cannot be modified. To renew one, import the new certificate,
point the listener at it, then delete the old one:

```go
renewed, err := client.LoadBalancer.ImportCertificate(ctx, newRequest)
if err != nil {
	return err
}
ref, err := client.LoadBalancer.UpdateListener(ctx, listenerID, nemau.Update{
	TLSCertificateRef: nemau.Set(renewed.ID),
})
if err != nil {
	return err
}
if _, err := client.WaitFor(ctx, ref); err != nil {
	return err
}
err = client.LoadBalancer.DeleteCertificate(ctx, oldCertID)
```

The load balancer reloads its configuration, and established connections are
kept.
Deleting a certificate that a listener still serves is refused with a `409`,
which `nemau.IsCertificateInUse` recognises.
