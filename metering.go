package nemau

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MeteringService covers /metering: consumption and accrued cost.
//
// The rows have no stable public schema, so these methods return the decoded
// JSON as is; only the query parameters below are stable. The tenant always
// comes from the credential.
type MeteringService struct{ client *Client }

// UsageQuery filters a usage or events listing. All fields are optional.
type UsageQuery struct {
	ResourceType string
	PeriodStart  time.Time
	PeriodEnd    time.Time
	Page         int
	PageSize     int
}

func (q UsageQuery) values() url.Values {
	v := url.Values{}
	if q.ResourceType != "" {
		v.Set("resource_type", q.ResourceType)
	}
	if !q.PeriodStart.IsZero() {
		v.Set("period_start", q.PeriodStart.UTC().Format(time.RFC3339))
	}
	if !q.PeriodEnd.IsZero() {
		v.Set("period_end", q.PeriodEnd.UTC().Format(time.RFC3339))
	}
	if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.PageSize > 0 {
		v.Set("page_size", strconv.Itoa(q.PageSize))
	}
	return v
}

// Usage returns paginated hourly usage rows.
func (s *MeteringService) Usage(ctx context.Context, q UsageQuery) (map[string]any, error) {
	return s.get(ctx, "/metering/usage", q.values())
}

// UsageSummary returns aggregated usage totals.
func (s *MeteringService) UsageSummary(ctx context.Context, q UsageQuery) (map[string]any, error) {
	return s.get(ctx, "/metering/usage/summary", q.values())
}

// Costs returns accrued cost per resource.
func (s *MeteringService) Costs(ctx context.Context, q UsageQuery) (map[string]any, error) {
	return s.get(ctx, "/metering/costs", q.values())
}

// Events returns paginated metering events.
func (s *MeteringService) Events(ctx context.Context, q UsageQuery) (map[string]any, error) {
	return s.get(ctx, "/metering/events", q.values())
}

func (s *MeteringService) get(ctx context.Context, path string, q url.Values) (map[string]any, error) {
	var out map[string]any
	err := s.client.do(ctx, request{method: http.MethodGet, path: path, query: q}, &out)
	return out, err
}
