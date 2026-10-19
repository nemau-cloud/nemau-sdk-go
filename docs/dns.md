# DNS

`client.DNS` manages authoritative zones and their record sets.

## Creating a zone

A new zone is pending until its owner proves control of the domain. The
platform serves nothing for the domain until then.

```go
zone, err := client.DNS.CreateZoneAndWait(ctx, nemau.CreateZoneRequest{
	Name:  "example.com.",
	Email: "hostmaster@example.com",
})
if err != nil {
	return err
}
v := zone.Verification
fmt.Printf("publish %s %s %q\n", v.Name, v.Type, v.Value)
```

`Email` is the SOA contact and is required. A zero `TTL` becomes 300 on the
server.

The zone comes back with `Status` set to `PENDING_VERIFICATION`
(`nemau.DNSZoneStatusPendingVerification`). `Verification` names the TXT record
to publish at the domain's current DNS host, such as
`_nemau-challenge.example.com.`, and the exact value it must hold.

A name under a domain the platform reserves is refused with
`nemau.ReasonDNSZoneReserved`, and a zone that already exists with
`nemau.ReasonDNSZoneExists`:

```go
if nemau.HasReason(err, nemau.ReasonDNSZoneExists) {
	// The zone is already active on the platform, or already claimed in this project.
}
```

## Verifying ownership

Once the TXT record is published, `VerifyZone` asks the platform to check it.
If the record holds the expected value, the zone becomes `ACTIVE`
(`nemau.DNSZoneStatusActive`) and `Verification` becomes nil.

```go
active, err := client.DNS.VerifyZoneAndWait(ctx, zone.ID)
if err != nil {
	return err // a *nemau.TaskFailedError while the record is not found
}
fmt.Println(active.Status)
```

The task fails, and the zone stays pending, while the record is absent or
wrong. That includes the time the record takes to reach the domain's current
nameservers, so a caller that has published the record a moment ago may need to
try again. Verifying a zone that is already active is refused with
`nemau.ReasonDNSZoneAlreadyVerified`.

`ListZones`, `GetZone`, `UpdateZone` and `DeleteZone` complete the set. Zone
methods take the zone's `ID`. Its `Name` (`example.com.`) is not an identifier.

## Record sets

A record set groups the records that share a name and a type. A zone must be
active to receive record sets: on a pending zone, the API refuses the write with
`nemau.ReasonDNSZonePendingVerification`.

```go
ref, err := client.DNS.CreateRecordSet(ctx, zone.ID, nemau.CreateRecordSetRequest{
	Name:    "www",
	Type:    "A",
	Records: []string{"203.0.113.7"},
	TTL:     300,
})
if err != nil {
	return err
}
_, err = client.WaitFor(ctx, ref)
```

A zero `TTL` becomes 300 on the server. A record set's id has the form
`canonical.name./TYPE`, for example `www.example.com./A`:

```go
ref, err := client.DNS.UpdateRecordSet(ctx, zone.ID, "www.example.com./A", nemau.UpdateRecordSetRequest{
	Records: []string{"203.0.113.7", "203.0.113.8"},
})
```

`UpdateRecordSet` replaces the records, the TTL or the description.
`ListRecordSets` returns the record sets of a zone (none while it is pending),
and `GetRecordSet` and `DeleteRecordSet` act on one record set.
