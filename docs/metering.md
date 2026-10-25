# Metering

`client.Metering` reads the organization's consumption and accrued cost. The
organization always comes from the credential.

| Method | Returns |
| --- | --- |
| `Usage(ctx, q)` | Hourly usage rows, paginated. |
| `UsageSummary(ctx, q)` | Aggregated usage totals. |
| `Costs(ctx, q)` | Accrued cost per resource. |
| `Events(ctx, q)` | Metering events, paginated. |

The rows have no stable public schema, so each method returns the decoded JSON
as a `map[string]any`. Only the query parameters are stable. Every field of
`nemau.UsageQuery` is optional:

| Field | Filter |
| --- | --- |
| `ResourceType` | One type of resource. |
| `PeriodStart`, `PeriodEnd` | The period, sent in UTC. |
| `Page`, `PageSize` | Pagination. |

```go
now := time.Now()
costs, err := client.Metering.Costs(ctx, nemau.UsageQuery{
	PeriodStart: now.AddDate(0, -1, 0),
	PeriodEnd:   now,
})
if err != nil {
	return err
}
fmt.Println(costs)
```
