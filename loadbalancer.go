package nemau

import (
	"context"
	"net/http"
	"net/url"
)

// LoadBalancerService covers the load-balancer half of /network: balancers,
// listeners, pools, members and health monitors.
type LoadBalancerService struct{ client *Client }

// List returns the project's load balancers.
func (s *LoadBalancerService) List(ctx context.Context) ([]LoadBalancer, error) {
	var out struct {
		LoadBalancers []LoadBalancer `json:"load_balancers"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/load-balancers"}, &out)
	return out.LoadBalancers, err
}

// Get returns one load balancer with its listeners and pools.
func (s *LoadBalancerService) Get(ctx context.Context, id string) (*LoadBalancer, error) {
	var out struct {
		LoadBalancer *LoadBalancer `json:"load_balancer"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/network/load-balancers/" + url.PathEscape(id),
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.LoadBalancer, nil
}

// ListFlavors returns the load-balancer tiers with their sizing and price.
func (s *LoadBalancerService) ListFlavors(ctx context.Context) ([]LoadBalancerFlavor, error) {
	var out struct {
		Flavors []LoadBalancerFlavor `json:"flavors"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/network/load-balancer-flavors"}, &out)
	return out.Flavors, err
}

// CreateLoadBalancerRequest describes a new balancer. VipSubnetID is required:
// the VIP is allocated inside that subnet.
//
// Flavor picks the tier; empty means lb-small. The high-availability tiers
// (lb-ha, lb-medium, lb-large) run two appliances on two hosts behind the same
// VIP. The tier cannot be changed after creation.
//
// A balancer runs in automatic mode, which serves HTTP, HTTPS, TCP and UDP
// listeners side by side.
type CreateLoadBalancerRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	VipSubnetID string `json:"vip_subnet_id"`
	Flavor      string `json:"flavor,omitempty"`
}

// Create queues a load balancer creation.
func (s *LoadBalancerService) Create(ctx context.Context, req CreateLoadBalancerRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/network/load-balancers", nil, req)
}

// CreateAndWait creates a load balancer and returns it once provisioned.
func (s *LoadBalancerService) CreateAndWait(ctx context.Context, req CreateLoadBalancerRequest) (*LoadBalancer, error) {
	ref, err := s.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	id, _, err := s.client.waitForResourceID(ctx, ref, "load_balancer_id")
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete queues a load balancer deletion.
func (s *LoadBalancerService) Delete(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/load-balancers/"+url.PathEscape(id), nil, nil)
}

// CreateListenerRequest describes a front-end port.
//
// AdminStateUp is a pointer so "not set" and "explicitly false" stay distinct;
// the API defaults an absent value to true.
//
// TLSCertificateRef is required for TERMINATED_HTTPS and refused otherwise: it
// is the ID of a certificate imported with
// [LoadBalancerService.ImportCertificate].
type CreateListenerRequest struct {
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	Protocol          string `json:"protocol"`
	ProtocolPort      int    `json:"protocol_port"`
	DefaultPoolID     string `json:"default_pool_id,omitempty"`
	ConnectionLimit   int    `json:"connection_limit,omitempty"`
	TLSCertificateRef string `json:"tls_certificate_ref,omitempty"`
	AdminStateUp      *bool  `json:"admin_state_up,omitempty"`
}

// ListListeners returns one balancer's listeners. lbID is required.
func (s *LoadBalancerService) ListListeners(ctx context.Context, lbID string) ([]Listener, error) {
	var out struct {
		Listeners []Listener `json:"listeners"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/network/listeners", query: url.Values{"lb_id": {lbID}},
	}, &out)
	return out.Listeners, err
}

// GetListener returns one listener of a balancer, by listing and filtering.
func (s *LoadBalancerService) GetListener(ctx context.Context, lbID, id string) (*Listener, error) {
	items, err := s.ListListeners(ctx, lbID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("listener", id, "/network/listeners")
}

// CreateListener queues a listener creation on a balancer.
func (s *LoadBalancerService) CreateListener(ctx context.Context, lbID string, req CreateListenerRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/load-balancers/"+url.PathEscape(lbID)+"/listeners", nil, req)
}

// DeleteListener queues a listener deletion.
func (s *LoadBalancerService) DeleteListener(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/listeners/"+url.PathEscape(id), nil, nil)
}

// CreatePoolRequest describes a back-end group.
type CreatePoolRequest struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	Protocol           string            `json:"protocol"`
	LBAlgorithm        string            `json:"lb_algorithm"`
	ListenerID         string            `json:"listener_id,omitempty"`
	SessionPersistence map[string]string `json:"session_persistence,omitempty"`
	AdminStateUp       *bool             `json:"admin_state_up,omitempty"`
}

// ListPools returns one balancer's pools. lbID is required.
func (s *LoadBalancerService) ListPools(ctx context.Context, lbID string) ([]Pool, error) {
	var out struct {
		Pools []Pool `json:"pools"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/network/pools", query: url.Values{"lb_id": {lbID}},
	}, &out)
	return out.Pools, err
}

// GetPool returns one pool of a balancer, by listing and filtering.
func (s *LoadBalancerService) GetPool(ctx context.Context, lbID, id string) (*Pool, error) {
	items, err := s.ListPools(ctx, lbID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("pool", id, "/network/pools")
}

// CreatePool queues a pool creation on a balancer.
func (s *LoadBalancerService) CreatePool(ctx context.Context, lbID string, req CreatePoolRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/load-balancers/"+url.PathEscape(lbID)+"/pools", nil, req)
}

// DeletePool queues a pool deletion.
func (s *LoadBalancerService) DeletePool(ctx context.Context, poolID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/network/pools/"+url.PathEscape(poolID), nil, nil)
}

// AddPoolMemberRequest describes a backend. Weight defaults to 1 server-side.
type AddPoolMemberRequest struct {
	Name         string `json:"name"`
	Address      string `json:"address"`
	ProtocolPort int    `json:"protocol_port"`
	Weight       int    `json:"weight,omitempty"`
	SubnetID     string `json:"subnet_id,omitempty"`
	AdminStateUp *bool  `json:"admin_state_up,omitempty"`
}

// ListPoolMembers returns a pool's backends.
func (s *LoadBalancerService) ListPoolMembers(ctx context.Context, poolID string) ([]PoolMember, error) {
	var out struct {
		Members []PoolMember `json:"members"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/network/pools/" + url.PathEscape(poolID) + "/members",
	}, &out)
	return out.Members, err
}

// GetPoolMember returns one backend, by listing and filtering.
func (s *LoadBalancerService) GetPoolMember(ctx context.Context, poolID, id string) (*PoolMember, error) {
	items, err := s.ListPoolMembers(ctx, poolID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("pool member", id, "/network/pools/"+poolID+"/members")
}

// AddPoolMember queues a backend addition.
func (s *LoadBalancerService) AddPoolMember(ctx context.Context, poolID string, req AddPoolMemberRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/pools/"+url.PathEscape(poolID)+"/members", nil, req)
}

// RemovePoolMember queues a backend removal.
func (s *LoadBalancerService) RemovePoolMember(ctx context.Context, poolID, memberID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete,
		"/network/pools/"+url.PathEscape(poolID)+"/members/"+url.PathEscape(memberID), nil, nil)
}

// CreateHealthMonitorRequest describes a probe. Type is HTTP, HTTPS, TCP or
// UDP_CONNECT; the HTTP fields apply to the HTTP types only.
type CreateHealthMonitorRequest struct {
	Type          string `json:"type"`
	Delay         int    `json:"delay"`
	Timeout       int    `json:"timeout"`
	MaxRetries    int    `json:"max_retries"`
	HTTPMethod    string `json:"http_method,omitempty"`
	URLPath       string `json:"url_path,omitempty"`
	ExpectedCodes string `json:"expected_codes,omitempty"`
	AdminStateUp  *bool  `json:"admin_state_up,omitempty"`
}

// ListPoolHealthMonitors returns a pool's probes.
func (s *LoadBalancerService) ListPoolHealthMonitors(ctx context.Context, poolID string) ([]HealthMonitor, error) {
	var out struct {
		HealthMonitors []HealthMonitor `json:"health_monitors"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/network/pools/" + url.PathEscape(poolID) + "/health-monitors",
	}, &out)
	return out.HealthMonitors, err
}

// GetHealthMonitor returns one probe, by listing and filtering.
func (s *LoadBalancerService) GetHealthMonitor(ctx context.Context, poolID, id string) (*HealthMonitor, error) {
	items, err := s.ListPoolHealthMonitors(ctx, poolID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("health monitor", id, "/network/pools/"+poolID+"/health-monitors")
}

// CreateHealthMonitor queues a probe creation on a pool.
func (s *LoadBalancerService) CreateHealthMonitor(ctx context.Context, poolID string, req CreateHealthMonitorRequest) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/network/pools/"+url.PathEscape(poolID)+"/health-monitors", nil, req)
}

// DeleteHealthMonitor queues a probe deletion.
func (s *LoadBalancerService) DeleteHealthMonitor(ctx context.Context, poolID, monitorID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete,
		"/network/pools/"+url.PathEscape(poolID)+"/health-monitors/"+url.PathEscape(monitorID), nil, nil)
}
