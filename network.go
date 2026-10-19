package nemau

import (
	"context"
	"net/http"
	"net/url"
)

// NetworkService covers /network: networks, subnets, interfaces, floating IPs
// and security groups. Load balancers use the same path prefix but have their
// own service, [LoadBalancerService].
type NetworkService struct{ client *Client }

// ListNetworks returns the project's networks.
func (s *NetworkService) ListNetworks(ctx context.Context) ([]Network, error) {
	var out struct {
		Networks []Network `json:"networks"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/networks"}, &out)
	return out.Networks, err
}

// GetNetwork returns one network.
//
// The API has no per-network read, so this lists and filters. A missing network
// returns a 404 [APIError], the same as a 404 from the server, so that
// [IsNotFound] tells a deleted network from a failed call.
func (s *NetworkService) GetNetwork(ctx context.Context, id string) (*Network, error) {
	items, err := s.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("network", id, "/network/networks")
}

// CreateNetwork queues a network creation.
func (s *NetworkService) CreateNetwork(ctx context.Context, name, description string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/network/networks", nil,
		map[string]string{"name": name, "description": description})
}

// CreateNetworkAndWait creates a network and returns it once built.
func (s *NetworkService) CreateNetworkAndWait(ctx context.Context, name, description string) (*Network, error) {
	ref, err := s.CreateNetwork(ctx, name, description)
	if err != nil {
		return nil, err
	}
	id, _, err := s.client.waitForResourceID(ctx, ref, "network_id")
	if err != nil {
		return nil, err
	}
	return s.GetNetwork(ctx, id)
}

// DeleteNetwork queues a network deletion.
func (s *NetworkService) DeleteNetwork(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/networks/"+url.PathEscape(id), nil, nil)
}

// CreateSubnetRequest describes a subnet inside a network.
//
// IPVersion defaults to 4, for which CIDR is required. For [IPv6], leave CIDR,
// GatewayIP and AllocationPools empty: the platform assigns a /64 from the
// region's routed prefix and uses its first address as the gateway. The network
// must already have an IPv4 subnet, and carries at most one IPv6 subnet.
type CreateSubnetRequest struct {
	Name            string           `json:"name"`
	CIDR            string           `json:"cidr,omitempty"`
	IPVersion       int              `json:"ip_version,omitempty"`
	GatewayIP       *string          `json:"gateway_ip,omitempty"`
	DNSServers      []string         `json:"dns_servers,omitempty"`
	AllocationPools []AllocationPool `json:"allocation_pools,omitempty"`
}

// ListSubnets returns the project's subnets, across all networks.
func (s *NetworkService) ListSubnets(ctx context.Context) ([]Subnet, error) {
	var out struct {
		Subnets []Subnet `json:"subnets"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/subnets"}, &out)
	return out.Subnets, err
}

// GetSubnet returns one subnet, by listing and filtering.
func (s *NetworkService) GetSubnet(ctx context.Context, id string) (*Subnet, error) {
	items, err := s.ListSubnets(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("subnet", id, "/network/subnets")
}

// CreateSubnet queues a subnet creation inside a network.
//
// The CIDR must not overlap any other subnet of the project, on this network or
// another: the API refuses it with a 409 that [IsSubnetOverlap] recognizes.
func (s *NetworkService) CreateSubnet(ctx context.Context, networkID string, req CreateSubnetRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/networks/"+url.PathEscape(networkID)+"/subnets", nil, req)
}

// DeleteSubnet queues a subnet deletion.
func (s *NetworkService) DeleteSubnet(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/subnets/"+url.PathEscape(id), nil, nil)
}

// ListNetworkInterfaces returns the interfaces attaching instances to networks.
func (s *NetworkService) ListNetworkInterfaces(ctx context.Context) ([]NetworkInterface, error) {
	var out struct {
		Interfaces []NetworkInterface `json:"interfaces"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/interfaces"}, &out)
	return out.Interfaces, err
}

// ListFloatingIPs returns the project's floating IPs.
func (s *NetworkService) ListFloatingIPs(ctx context.Context) ([]FloatingIP, error) {
	var out struct {
		FloatingIPs []FloatingIP `json:"floatingips"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/floating-ips"}, &out)
	return out.FloatingIPs, err
}

// GetFloatingIP returns one floating IP, by listing and filtering.
func (s *NetworkService) GetFloatingIP(ctx context.Context, id string) (*FloatingIP, error) {
	items, err := s.ListFloatingIPs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("floating ip", id, "/network/floating-ips")
}

// AllocateFloatingIP queues an IPv4 address allocation. Both arguments are
// optional; an empty externalNetworkID uses the region's default external
// network. Use [NetworkService.AllocateFloatingIPWith] to pick the IP version.
func (s *NetworkService) AllocateFloatingIP(ctx context.Context, externalNetworkID, description string) (*TaskRef, error) {
	return s.AllocateFloatingIPWith(ctx, AllocateFloatingIPRequest{
		FloatingNetworkID: externalNetworkID, Description: description,
	})
}

// AllocateFloatingIPRequest describes an address allocation. Every field is
// optional: an empty FloatingNetworkID uses the region's default external
// network, and a zero IPVersion means [IPv4].
type AllocateFloatingIPRequest struct {
	FloatingNetworkID string `json:"floating_network_id,omitempty"`
	Description       string `json:"description,omitempty"`
	IPVersion         int    `json:"ip_version,omitempty"`
}

// AllocateFloatingIPWith queues an address allocation of the requested version.
// An IPv6 allocation is refused with a 400 in a region that does not offer
// IPv6; see [NetworkService.GetCapabilities].
func (s *NetworkService) AllocateFloatingIPWith(ctx context.Context, req AllocateFloatingIPRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/network/floating-ips", nil, req)
}

// ReleaseFloatingIP queues an address release.
func (s *NetworkService) ReleaseFloatingIP(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/floating-ips/"+url.PathEscape(id), nil, nil)
}

// Floating IP association targets.
const (
	TargetInstance     = "instance"
	TargetLoadBalancer = "load_balancer"
)

// AssociateFloatingIP points an address at an instance or a load balancer.
// targetType must be [TargetInstance] or [TargetLoadBalancer].
func (s *NetworkService) AssociateFloatingIP(ctx context.Context, id, targetType, targetID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPut,
		"/network/floating-ips/"+url.PathEscape(id)+"/associate", nil,
		map[string]string{"target_type": targetType, "target_id": targetID})
}

// DisassociateFloatingIP detaches an address from its target, keeping the
// allocation.
func (s *NetworkService) DisassociateFloatingIP(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPut,
		"/network/floating-ips/"+url.PathEscape(id)+"/disassociate", nil, nil)
}

// GetCapabilities reports which optional network features (IPv6 floating IPs,
// IPv6 subnets) the region offers.
func (s *NetworkService) GetCapabilities(ctx context.Context) (*NetworkCapabilities, error) {
	var out struct {
		Capabilities NetworkCapabilities `json:"capabilities"`
	}
	if err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/capabilities"}, &out); err != nil {
		return nil, err
	}
	return &out.Capabilities, nil
}

// ListSecurityGroups returns the project's security groups, rules included.
func (s *NetworkService) ListSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	var out struct {
		SecurityGroups []SecurityGroup `json:"security_groups"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/security-groups"}, &out)
	return out.SecurityGroups, err
}

// GetSecurityGroup returns one security group, by listing and filtering.
func (s *NetworkService) GetSecurityGroup(ctx context.Context, id string) (*SecurityGroup, error) {
	items, err := s.ListSecurityGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("security group", id, "/network/security-groups")
}

// CreateSecurityGroup queues a security group creation.
func (s *NetworkService) CreateSecurityGroup(ctx context.Context, name, description string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/network/security-groups", nil,
		map[string]string{"name": name, "description": description})
}

// DeleteSecurityGroup queues a security group deletion.
func (s *NetworkService) DeleteSecurityGroup(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/security-groups/"+url.PathEscape(id), nil, nil)
}

// EnableSecurityGroupEgressFiltering queues the conversion of a security group
// created before egress filtering existed. The group receives two egress rules
// that allow all outbound traffic (0.0.0.0/0 and ::/0), then every instance
// carrying it has its outbound policy recomputed: an instance whose groups all
// filter egress only sends what one of their egress rules allows.
//
// Nothing changes for traffic when the task completes, since the added rules
// allow everything; removing or narrowing them afterwards takes effect. The
// conversion cannot be undone. Running it on a group that already filters
// egress succeeds without adding anything.
func (s *NetworkService) EnableSecurityGroupEgressFiltering(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/security-groups/"+url.PathEscape(id)+"/enable-egress-filtering", nil, nil)
}

// SecurityGroupRuleRequest describes one filter entry.
//
// Leave PortRangeMin and PortRangeMax at zero for a protocol without ports; the
// API only forwards them when at least one is non-zero.
type SecurityGroupRuleRequest struct {
	Direction      string `json:"direction"` // ingress | egress
	Protocol       string `json:"protocol,omitempty"`
	PortRangeMin   int    `json:"port_range_min,omitempty"`
	PortRangeMax   int    `json:"port_range_max,omitempty"`
	RemoteIPPrefix string `json:"remote_ip_prefix,omitempty"`
}

// CreateSecurityGroupRule queues a rule creation.
func (s *NetworkService) CreateSecurityGroupRule(ctx context.Context, groupID string, req SecurityGroupRuleRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/security-groups/"+url.PathEscape(groupID)+"/rules", nil, req)
}

// DeleteSecurityGroupRule queues a rule deletion.
func (s *NetworkService) DeleteSecurityGroupRule(ctx context.Context, groupID, ruleID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete,
		"/network/security-groups/"+url.PathEscape(groupID)+"/rules/"+url.PathEscape(ruleID), nil, nil)
}
