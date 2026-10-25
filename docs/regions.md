# Regions

`client.Regions` lists the regions and reports their health. These are the only
endpoints of the API that need no authentication, so a client without a
credential can call them, for example to check that the API is reachable before
asking for credentials.

```go
client, err := nemau.NewClient()
if err != nil {
	return err
}
regions, err := client.Regions.List(ctx)
if err != nil {
	return err
}
for _, r := range regions {
	fmt.Println(r.Slug, r.DisplayName, r.Country)
}
```

`List` returns the enabled regions. `Status(ctx, slug)` returns a region's
health as a `*nemau.RegionStatus`, with `Healthy` and `CheckedAt`.

An unhealthy region answers `503`, which `Status` returns as an
`*nemau.APIError` once the retries of the client's policy are spent. Check for
it before treating the error as a problem on the caller's side:

```go
status, err := client.Regions.Status(ctx, slug)
var apiErr *nemau.APIError
switch {
case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusServiceUnavailable:
	fmt.Println(slug, "is unhealthy")
case err != nil:
	return err
default:
	fmt.Println(slug, "checked at", status.CheckedAt)
}
```
