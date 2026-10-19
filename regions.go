package nemau

import (
	"context"
	"net/http"
	"net/url"
)

// RegionsService covers /regions.
//
// These are the only API endpoints that need no authentication, so a client
// with no credential can list regions. A CLI can use them to check that the API
// is reachable before asking for credentials.
type RegionsService struct{ client *Client }

// List returns the enabled regions.
func (s *RegionsService) List(ctx context.Context) ([]Region, error) {
	var out []Region
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/regions"}, &out)
	return out, err
}

// Status reports a region's health.
//
// An unhealthy region answers 503, which is returned as an [APIError] instead
// of a RegionStatus. Check for that before treating the error as a client
// problem.
func (s *RegionsService) Status(ctx context.Context, slug string) (*RegionStatus, error) {
	var out RegionStatus
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/regions/" + url.PathEscape(slug) + "/status",
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
