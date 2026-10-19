package nemau

import (
	"context"
	"net/http"
	"net/url"
)

// DNSService covers /dns: authoritative zones and their record sets.
type DNSService struct{ client *Client }

// Zone statuses.
//
// A zone is created pending: the platform serves nothing for a domain until
// its owner has published the TXT record named in [DNSZone.Verification] and
// asked for it to be checked with [DNSService.VerifyZone].
const (
	DNSZoneStatusPendingVerification = "PENDING_VERIFICATION"
	DNSZoneStatusActive              = "ACTIVE"
)

// The [APIError.Reason] codes of the DNS refusals a client can act on.
const (
	// ReasonDNSZoneReserved (400): the name is under a domain the platform
	// keeps for itself (nemau-cloud.fr, nemau-cloud.com).
	ReasonDNSZoneReserved = "dns_zone_reserved"
	// ReasonDNSZoneExists (409): the zone is already active on the platform,
	// or already claimed in this project.
	ReasonDNSZoneExists = "dns_zone_exists"
	// ReasonDNSZoneAlreadyVerified (409): VerifyZone on a zone that is
	// already active.
	ReasonDNSZoneAlreadyVerified = "dns_zone_already_verified"
	// ReasonDNSZonePendingVerification (409): a record-set write on a zone
	// whose ownership has not been proven yet.
	ReasonDNSZonePendingVerification = "dns_zone_pending_verification"
)

// ListZones returns the project's zones.
func (s *DNSService) ListZones(ctx context.Context) ([]DNSZone, error) {
	var out struct {
		Zones []DNSZone `json:"zones"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/dns/zones"}, &out)
	return out.Zones, err
}

// GetZone returns one zone, by listing and filtering: the API has no per-zone
// read.
func (s *DNSService) GetZone(ctx context.Context, id string) (*DNSZone, error) {
	items, err := s.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("dns zone", id, "/dns/zones")
}

// CreateZoneRequest describes a new zone. Email is the SOA contact and is
// required. A zero TTL becomes 300 server-side.
type CreateZoneRequest struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	Description string `json:"description,omitempty"`
	TTL         int    `json:"ttl,omitempty"`
}

// CreateZone queues a zone creation.
//
// The zone comes back pending, with the TXT record that proves ownership of the
// domain in its Verification field; see [DNSService.VerifyZone]. A name under a
// reserved domain is refused with [ReasonDNSZoneReserved], one that already
// exists with [ReasonDNSZoneExists].
func (s *DNSService) CreateZone(ctx context.Context, req CreateZoneRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/dns/zones", nil, req)
}

// CreateZoneAndWait creates a zone and returns it once created. It is still
// pending: publish its Verification record, then call
// [DNSService.VerifyZoneAndWait].
func (s *DNSService) CreateZoneAndWait(ctx context.Context, req CreateZoneRequest) (*DNSZone, error) {
	ref, err := s.CreateZone(ctx, req)
	if err != nil {
		return nil, err
	}
	id, _, err := s.client.waitForResourceID(ctx, ref, "zone_id")
	if err != nil {
		return nil, err
	}
	return s.GetZone(ctx, id)
}

// VerifyZone queues the ownership check of a pending zone: the platform looks
// up the TXT record named in [DNSZone.Verification] and activates the zone if
// the record holds the expected value.
//
// The task fails, and the zone stays pending, while the record is absent or
// wrong. That includes the time the record takes to reach the domain's current
// nameservers, so a caller that has just published it may need to try again. A
// zone that is already active is refused with [ReasonDNSZoneAlreadyVerified].
func (s *DNSService) VerifyZone(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/dns/zones/"+url.PathEscape(id)+"/verify", nil, nil)
}

// VerifyZoneAndWait runs [DNSService.VerifyZone] to completion and returns the
// zone as it stands afterwards. A record not found yet surfaces as a
// [TaskFailedError].
func (s *DNSService) VerifyZoneAndWait(ctx context.Context, id string) (*DNSZone, error) {
	ref, err := s.VerifyZone(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.WaitFor(ctx, ref); err != nil {
		return nil, err
	}
	return s.GetZone(ctx, id)
}

// DeleteZone queues a zone deletion.
func (s *DNSService) DeleteZone(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/dns/zones/"+url.PathEscape(id), nil, nil)
}

// ListRecordSets returns a zone's record sets. A zone pending verification
// has none yet: the list is empty.
func (s *DNSService) ListRecordSets(ctx context.Context, zoneID string) ([]DNSRecordSet, error) {
	var out struct {
		RecordSets []DNSRecordSet `json:"recordsets"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/dns/zones/" + url.PathEscape(zoneID) + "/recordsets",
	}, &out)
	return out.RecordSets, err
}

// GetRecordSet returns one record set, by listing and filtering.
func (s *DNSService) GetRecordSet(ctx context.Context, zoneID, id string) (*DNSRecordSet, error) {
	items, err := s.ListRecordSets(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("dns record set", id, "/dns/zones/"+zoneID+"/recordsets")
}

// CreateRecordSetRequest describes a record set. A zero TTL becomes 300
// server-side.
type CreateRecordSetRequest struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Records     []string `json:"records"`
	TTL         int      `json:"ttl,omitempty"`
	Description string   `json:"description,omitempty"`
}

// CreateRecordSet queues a record-set creation. The zone must be active: on a
// zone still pending verification the API refuses it with
// [ReasonDNSZonePendingVerification].
func (s *DNSService) CreateRecordSet(ctx context.Context, zoneID string, req CreateRecordSetRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/dns/zones/"+url.PathEscape(zoneID)+"/recordsets", nil, req)
}

// UpdateRecordSetRequest replaces a record set's contents.
type UpdateRecordSetRequest struct {
	Records     []string `json:"records,omitempty"`
	TTL         int      `json:"ttl,omitempty"`
	Description string   `json:"description,omitempty"`
}

// UpdateRecordSet queues a record-set update.
//
// The record-set id is "canonical.name./TYPE". It is sent as ?id= because it
// contains a slash, which cannot be part of a single path segment.
func (s *DNSService) UpdateRecordSet(ctx context.Context, zoneID, recordSetID string, req UpdateRecordSetRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPut,
		"/dns/zones/"+url.PathEscape(zoneID)+"/recordsets",
		url.Values{"id": {recordSetID}}, req)
}

// DeleteRecordSet queues a record-set deletion.
func (s *DNSService) DeleteRecordSet(ctx context.Context, zoneID, recordSetID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete,
		"/dns/zones/"+url.PathEscape(zoneID)+"/recordsets",
		url.Values{"id": {recordSetID}}, nil)
}
