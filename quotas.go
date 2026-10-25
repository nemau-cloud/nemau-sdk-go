package nemau

import (
	"context"
	"net/http"
)

// Quota dimension names. Every write is checked against these limits before it
// is accepted; reading them lets a caller size a request before sending it.
const (
	QuotaInstances      = "instances"
	QuotaCores          = "cores"
	QuotaRAM            = "ram"
	QuotaVolumes        = "volumes"
	QuotaVolumeSize     = "volumeSize"
	QuotaSnapshots      = "snapshots"
	QuotaNetworks       = "networks"
	QuotaSubnets        = "subnets"
	QuotaFloatingIPs    = "floatingIps"
	QuotaSecurityGroups = "securityGroups"
	QuotaLoadBalancers  = "loadBalancers"
	QuotaObjectStorage  = "objectStorage"
)

// Quota scopes. A write needs room in both.
const (
	QuotaScopeTenant  = "tenant"
	QuotaScopeProject = "project"
)

// QuotaDimension is one limit and what currently stands against it.
type QuotaDimension struct {
	Name string `json:"name"`
	// Unit is "count", "MB", "GB" or "bytes". RAM is counted in MB and volume
	// size in GB.
	Unit string `json:"unit"`
	Used int64  `json:"used"`
	// Reserved is capacity held by accepted tasks that have not finished yet.
	// The API compares used+reserved+requested to the limit, so include it when
	// computing headroom.
	Reserved int64 `json:"reserved"`
	Limit    int64 `json:"limit"`
}

// Available reports the headroom left in this dimension, never negative.
func (d *QuotaDimension) Available() int64 {
	if free := d.Limit - d.Used - d.Reserved; free > 0 {
		return free
	}
	return 0
}

// QuotaScope is one scope's allowance.
type QuotaScope struct {
	Scope      string           `json:"scope"`
	ProjectID  string           `json:"project_id,omitempty"`
	Dimensions []QuotaDimension `json:"dimensions"`
}

// Dimension returns one dimension of this scope by name.
func (s *QuotaScope) Dimension(name string) *QuotaDimension {
	for i := range s.Dimensions {
		if s.Dimensions[i].Name == name {
			return &s.Dimensions[i]
		}
	}
	return nil
}

// QuotaList is the answer to a quota read: the project the credential resolves
// to, and the organization that owns it.
type QuotaList struct {
	Quotas []QuotaScope `json:"quotas"`
}

// Scope returns one scope by name, QuotaScopeProject or QuotaScopeTenant.
func (l *QuotaList) Scope(name string) *QuotaScope {
	for i := range l.Quotas {
		if l.Quotas[i].Scope == name {
			return &l.Quotas[i]
		}
	}
	return nil
}

// Binding returns the named dimension with the least headroom across all
// scopes, which is the limit a write will reach first.
//
// A write is accepted only when both the project and its organization have
// room, so checking the project alone is not enough. Binding returns nil when
// no scope reports the dimension.
func (l *QuotaList) Binding(name string) *QuotaDimension {
	var binding *QuotaDimension
	for i := range l.Quotas {
		dimension := l.Quotas[i].Dimension(name)
		if dimension == nil {
			continue
		}
		if binding == nil || dimension.Available() < binding.Available() {
			binding = dimension
		}
	}
	return binding
}

// QuotasService reads the capacity limits applied to a credential's scope.
type QuotasService struct{ client *Client }

// Get returns the limits, usage and pending reservations for the project the
// credential resolves to and for the organization that owns it.
func (s *QuotasService) Get(ctx context.Context) (*QuotaList, error) {
	var out QuotaList
	if err := s.client.do(ctx, request{method: http.MethodGet, path: "/quotas"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
