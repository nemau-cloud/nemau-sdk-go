package nemau

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Update carries the fields a PATCH changes.
//
// A nil pointer means "leave it alone"; a pointer to the zero value means "set
// it to empty". A caller can therefore change one field without clearing the
// others, including fields another client has just set.
//
// Use [Set] to build the pointers.
type Update struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`

	// Load balancer family.
	LBAlgorithm     *string `json:"lb_algorithm,omitempty"`
	ConnectionLimit *int    `json:"connection_limit,omitempty"`
	Weight          *int    `json:"weight,omitempty"`
	Delay           *int    `json:"delay,omitempty"`
	Timeout         *int    `json:"timeout,omitempty"`
	MaxRetries      *int    `json:"max_retries,omitempty"`
	ExpectedCodes   *string `json:"expected_codes,omitempty"`
	// TLSCertificateRef points a TERMINATED_HTTPS listener at another
	// imported certificate: the way to renew one without downtime.
	TLSCertificateRef *string `json:"tls_certificate_ref,omitempty"`

	// Object containers.
	Versioning    *bool `json:"versioning,omitempty"`
	RetentionDays *int  `json:"retention_days,omitempty"`

	// DNS zones.
	Email *string `json:"email,omitempty"`
	TTL   *int    `json:"ttl,omitempty"`
}

// Set returns a pointer to v, for building an [Update].
//
//	client.Network.UpdateNetwork(ctx, id, nemau.Update{
//	    Description: nemau.Set("prod segment"),
//	})
func Set[T any](v T) *T { return &v }

// errNothingToUpdate is returned before any request when an Update is empty.
// The API answers 400 in the same case; checking here saves a round trip.
var errNothingToUpdate = errors.New("nemau: update carries no field to change")

func (u Update) isEmpty() bool {
	return u == Update{}
}

// patch is the one call behind every Update* method.
func (c *Client) patch(ctx context.Context, path string, query url.Values, update Update) (*TaskRef, error) {
	if update.isEmpty() {
		return nil, errNothingToUpdate
	}
	return c.submit(ctx, http.MethodPatch, path, query, update)
}

// UpdateInstance changes an instance's name or description.
//
// The instance keeps running: nothing addresses an instance by its name.
func (s *ComputeService) UpdateInstance(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/compute/instances/"+url.PathEscape(id), nil, update)
}

// UpdateNetwork changes a network's name or description.
//
// The network is not rebuilt: its identifier and VXLAN segment stay as they
// are, so the instances attached to it are untouched.
func (s *NetworkService) UpdateNetwork(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/networks/"+url.PathEscape(id), nil, update)
}

// UpdateSubnet changes a subnet's name.
func (s *NetworkService) UpdateSubnet(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/subnets/"+url.PathEscape(id), nil, update)
}

// UpdateSecurityGroup changes a security group's name or description.
//
// Its rules are separate resources and remain immutable: a rule is created and
// deleted, never edited.
func (s *NetworkService) UpdateSecurityGroup(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/security-groups/"+url.PathEscape(id), nil, update)
}

// UpdateFloatingIP changes a floating IP's description. The address itself is
// the allocation and cannot be changed.
func (s *NetworkService) UpdateFloatingIP(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/floating-ips/"+url.PathEscape(id), nil, update)
}

// UpdateLoadBalancer changes a load balancer's name or description.
func (s *LoadBalancerService) UpdateLoadBalancer(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/load-balancers/"+url.PathEscape(id), nil, update)
}

// UpdateListener changes a listener's name, description, connection limit or,
// on a TERMINATED_HTTPS listener, its certificate. The appliance reloads its
// configuration; established connections are kept.
func (s *LoadBalancerService) UpdateListener(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/listeners/"+url.PathEscape(id), nil, update)
}

// UpdatePool changes a pool's name, description or balancing algorithm.
//
// Changing the algorithm rewrites the appliance configuration in place;
// existing connections are not dropped.
func (s *LoadBalancerService) UpdatePool(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/network/pools/"+url.PathEscape(id), nil, update)
}

// UpdatePoolMember changes a member's name or weight.
func (s *LoadBalancerService) UpdatePoolMember(ctx context.Context, poolID, memberID string, update Update) (*TaskRef, error) {
	path := "/network/pools/" + url.PathEscape(poolID) + "/members/" + url.PathEscape(memberID)
	return s.client.patch(ctx, path, nil, update)
}

// UpdateHealthMonitor changes a monitor's probe timings or expected codes.
func (s *LoadBalancerService) UpdateHealthMonitor(ctx context.Context, poolID, monitorID string, update Update) (*TaskRef, error) {
	path := "/network/pools/" + url.PathEscape(poolID) + "/health-monitors/" + url.PathEscape(monitorID)
	return s.client.patch(ctx, path, nil, update)
}

// UpdateVolume changes a volume's name or description. Its size changes with
// [StorageService.ExtendVolume], which only ever grows.
func (s *StorageService) UpdateVolume(ctx context.Context, volumeID string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/storage/volumes", url.Values{"id": {volumeID}}, update)
}

// UpdateContainer changes a container's versioning or retention.
//
// The name cannot be changed. Retention can only be extended: shortening it
// would weaken a WORM guarantee.
func (s *StorageService) UpdateContainer(ctx context.Context, name string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/storage/containers/"+url.PathEscape(name), nil, update)
}

// UpdateZone changes a zone's description, SOA contact or default TTL. Its name
// is the zone's identity and cannot change.
func (s *DNSService) UpdateZone(ctx context.Context, id string, update Update) (*TaskRef, error) {
	return s.client.patch(ctx, "/dns/zones/"+url.PathEscape(id), nil, update)
}
