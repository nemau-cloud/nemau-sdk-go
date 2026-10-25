# Networking

`client.Network` manages networks, subnets, network interfaces, floating IPs
and security groups. Load balancers have their own page:
[Load balancers](load-balancers.md).

## Networks

```go
network, err := client.Network.CreateNetworkAndWait(ctx, "backend", "application servers")
if err != nil {
	return err
}
```

`CreateNetwork` queues the creation without waiting. `ListNetworks`,
`GetNetwork`, `UpdateNetwork` and `DeleteNetwork` complete the set. A network
cannot be deleted while it still has a subnet: the task fails with
`resource_in_use`.

## Subnets

`CreateSubnet` queues the creation of a subnet inside a network:

```go
ref, err := client.Network.CreateSubnet(ctx, network.ID, nemau.CreateSubnetRequest{
	Name: "web",
	CIDR: "10.0.1.0/24",
})
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
```

`IPVersion` defaults to 4, for which `CIDR` is required. `GatewayIP`,
`DNSServers` and `AllocationPools` are optional.

The subnets of a project are routed together, whichever network they belong
to, so a range must not overlap any other subnet of the project. The API refuses
an overlap with a `409` that `nemau.IsSubnetOverlap` recognises. The message
names the conflicting subnet, its range and its network:

```go
_, err := client.Network.CreateSubnet(ctx, networkID, nemau.CreateSubnetRequest{
	Name: "web",
	CIDR: "10.0.1.0/24",
})
if nemau.IsSubnetOverlap(err) {
	// cidr 10.0.1.0/24 overlaps subnet "app" (10.0.0.0/16) on network "backend"; ...
}
```

If a conflicting subnet is created between the request and the task, the task
fails instead, and the error is a `*nemau.TaskFailedError` with the same
message.

`ListSubnets` returns the subnets of every network in the project. `GetSubnet`,
`UpdateSubnet` and `DeleteSubnet` act on one subnet.

`ListNetworkInterfaces` returns the interfaces that attach instances to
networks, with their addresses.

## Floating IPs

A floating IP is a public address that can point at an instance or a load
balancer.

```go
ref, err := client.Network.AllocateFloatingIP(ctx, "", "web front")
if err != nil {
	return err
}
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
if task.ResourceID == nil {
	return fmt.Errorf("task %s returned no floating IP id", task.ID)
}

ref, err = client.Network.AssociateFloatingIP(ctx, *task.ResourceID, nemau.TargetInstance, instanceID)
```

- `AllocateFloatingIP` allocates an IPv4 address. An empty external network id
  uses the region's default external network.
- `AllocateFloatingIPWith` takes an `AllocateFloatingIPRequest`, to choose the
  IP version.
- `AssociateFloatingIP` takes `nemau.TargetInstance` or
  `nemau.TargetLoadBalancer`. `DisassociateFloatingIP` detaches the address and
  keeps the allocation, and `ReleaseFloatingIP` releases it.
- `ListFloatingIPs`, `GetFloatingIP` and `UpdateFloatingIP` read the addresses
  and change their description.

## IPv6

IPv6 comes as two independent features. Both are off in a region that has no
IPv6 prefix. `GetCapabilities` reports which ones the region offers:

```go
caps, err := client.Network.GetCapabilities(ctx)
if err != nil {
	return err
}
fmt.Println(caps.IPv6FloatingIPs, caps.IPv6Subnets)
```

IPv6 floating IPs are allocated with `AllocateFloatingIPWith` and
`IPVersion: nemau.IPv6`. The address is translated to the target's private IPv4
address on the region's exit node, so the guest needs no IPv6 configuration and
sees IPv6 clients as addresses of the translation range. A target holds at most
one IPv6 floating IP. In a region without IPv6, the request is refused with a
`400`.

```go
ref, err := client.Network.AllocateFloatingIPWith(ctx, nemau.AllocateFloatingIPRequest{
	IPVersion:   nemau.IPv6,
	Description: "web front, IPv6",
})
```

Dual-stack networks get an IPv6 subnet from `CreateSubnet` with
`IPVersion: nemau.IPv6` and no `CIDR`, `GatewayIP` or `AllocationPools`. The
platform assigns a /64 from the region's routed prefix and uses its first
address as the gateway. The network must already have an IPv4 subnet, and
holds at most one IPv6 subnet. Instances created on the network afterwards get
a public IPv6 address, listed after the IPv4 one in `Instance.Addresses`, and
must carry at least one security group.

```go
ref, err := client.Network.CreateSubnet(ctx, networkID, nemau.CreateSubnetRequest{
	Name:      "web-v6",
	IPVersion: nemau.IPv6,
})
```

## Security groups

A security group is a stateful filter applied to instances. Its rules are
separate resources: a rule is created and deleted, never edited.

```go
ref, err := client.Network.CreateSecurityGroup(ctx, "ssh", "SSH from the office")
if err != nil {
	return err
}
task, err := client.WaitFor(ctx, ref)
if err != nil {
	return err
}
if task.ResourceID == nil {
	return fmt.Errorf("task %s returned no security group id", task.ID)
}
groupID := *task.ResourceID

ref, err = client.Network.CreateSecurityGroupRule(ctx, groupID, nemau.SecurityGroupRuleRequest{
	Direction:      "ingress",
	Protocol:       "tcp",
	PortRangeMin:   22,
	PortRangeMax:   22,
	RemoteIPPrefix: "198.51.100.0/24",
})
```

`Direction` is `ingress` or `egress`. Leave `PortRangeMin` and `PortRangeMax`
at zero for a protocol without ports.

`ListSecurityGroups` returns the groups with their rules, and
`GetSecurityGroup` one group. `UpdateSecurityGroup`, `DeleteSecurityGroup` and
`DeleteSecurityGroupRule` complete the set. An instance's groups are set at
creation or replaced with `Compute.SetSecurityGroups` (see
[Compute](compute.md#security-groups)).

### Egress filtering

New security groups filter egress. A group starts with two egress rules that
allow all outbound traffic, one per IP family (`0.0.0.0/0` and `::/0`). Remove
or narrow them to restrict what instances can reach. An instance's egress is
filtered only when all of its groups filter egress: a single older group lets
all outbound traffic through.

Groups created before egress filtering existed report
`EgressFiltering == false`, and their egress rules have no effect.
`EnableSecurityGroupEgressFiltering` converts such a group: it adds the two
default rules, then recomputes the outbound policy of the instances that carry
the group. Traffic is unchanged when the task completes, since the new rules
allow everything.

```go
ref, err := client.Network.EnableSecurityGroupEgressFiltering(ctx, groupID)
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
```

The conversion cannot be undone. Running it on a group that already filters
egress succeeds without adding anything.
