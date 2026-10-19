package nemau

import (
	"context"
	"net/http"
	"testing"
)

// The two-string AllocateFloatingIP sends no ip_version, so its request is
// valid in a region without IPv6.
func TestAllocateFloatingIPStaysIPv4(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	if _, err := c.Network.AllocateFloatingIP(context.Background(), "", "front"); err != nil {
		t.Fatal(err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/v1/network/floating-ips" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if cap.body != `{"description":"front"}` {
		t.Fatalf("body = %s", cap.body)
	}
}

func TestAllocateFloatingIPWithSendsTheVersion(t *testing.T) {
	for _, tc := range []struct {
		req  AllocateFloatingIPRequest
		want string
	}{
		{AllocateFloatingIPRequest{IPVersion: IPv6}, `{"ip_version":6}`},
		{AllocateFloatingIPRequest{FloatingNetworkID: "ext-1", IPVersion: IPv4}, `{"floating_network_id":"ext-1","ip_version":4}`},
		{AllocateFloatingIPRequest{}, `{}`},
	} {
		cap := &capture{}
		c, _ := newTestClient(t, cap.handler())
		if _, err := c.Network.AllocateFloatingIPWith(context.Background(), tc.req); err != nil {
			t.Fatal(err)
		}
		if cap.body != tc.want {
			t.Errorf("body = %s, want %s", cap.body, tc.want)
		}
	}
}

// An IPv6 subnet names no range: the platform assigns the /64, so an empty
// CIDR must be absent from the body rather than sent as "".
func TestCreateIPv6SubnetOmitsTheCIDR(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	if _, err := c.Network.CreateSubnet(context.Background(), "net-1", CreateSubnetRequest{
		Name: "v6", IPVersion: IPv6,
	}); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/api/v1/network/networks/net-1/subnets" {
		t.Fatalf("path = %s", cap.path)
	}
	if cap.body != `{"name":"v6","ip_version":6}` {
		t.Fatalf("body = %s", cap.body)
	}
}

func TestCreateIPv4SubnetBodyIsUnchanged(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	if _, err := c.Network.CreateSubnet(context.Background(), "net-1", CreateSubnetRequest{
		Name: "v4", CIDR: "10.0.0.0/24",
	}); err != nil {
		t.Fatal(err)
	}
	if cap.body != `{"name":"v4","cidr":"10.0.0.0/24"}` {
		t.Fatalf("body = %s", cap.body)
	}
}

func TestIPVersionDecodes(t *testing.T) {
	cap := &capture{resp: `{"floatingips":[
		{"id":"fip-4","address":"203.0.113.7","status":"available"},
		{"id":"fip-6","address":"2001:db8:f::10","ip_version":6,"private_address":"10.0.0.5","status":"attached"}]}`}
	c, _ := newTestClient(t, cap.handler())
	fips, err := c.Network.ListFloatingIPs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(fips) != 2 || fips[0].IPVersion != 0 || fips[1].IPVersion != IPv6 || fips[1].PrivateAddress != "10.0.0.5" {
		t.Fatalf("floating ips = %+v", fips)
	}

	cap = &capture{resp: `{"subnets":[{"id":"sub-6","cidr":"2001:db8:100:1::/64","gateway_ip":"2001:db8:100:1::1","ip_version":6,"allocation_pools":[]}]}`}
	c, _ = newTestClient(t, cap.handler())
	subnet, err := c.Network.GetSubnet(context.Background(), "sub-6")
	if err != nil {
		t.Fatal(err)
	}
	if subnet.IPVersion != IPv6 || subnet.CIDR != "2001:db8:100:1::/64" || len(subnet.AllocationPools) != 0 {
		t.Fatalf("subnet = %+v", subnet)
	}
}

func TestGetCapabilities(t *testing.T) {
	cap := &capture{resp: `{"capabilities":{"ipv6_floating_ips":true,"ipv6_subnets":false}}`}
	c, _ := newTestClient(t, cap.handler())
	caps, err := c.Network.GetCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/v1/network/capabilities" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if !caps.IPv6FloatingIPs || caps.IPv6Subnets {
		t.Fatalf("capabilities = %+v", caps)
	}
}

func TestEnableSecurityGroupEgressFiltering(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	ref, err := c.Network.EnableSecurityGroupEgressFiltering(context.Background(), "sg-1")
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/v1/network/security-groups/sg-1/enable-egress-filtering" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if cap.body != "" {
		t.Fatalf("the conversion takes no body, got %s", cap.body)
	}
	if ref.TaskID != "t" {
		t.Fatalf("task id = %q", ref.TaskID)
	}
}

// A group created before egress filtering is served with egress_filtering
// false; one created since, or converted, with true.
func TestSecurityGroupEgressFilteringDecodes(t *testing.T) {
	cap := &capture{resp: `{"security_groups":[
		{"id":"sg-old","name":"old","egress_filtering":false,"rules":[]},
		{"id":"sg-new","name":"new","egress_filtering":true,"rules":[
			{"id":"r1","security_group_id":"sg-new","direction":"egress","action":"accept","remote_ip_prefix":"0.0.0.0/0"}
		]}
	],"count":2}`}
	c, _ := newTestClient(t, cap.handler())
	groups, err := c.Network.ListSecurityGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].EgressFiltering || !groups[1].EgressFiltering {
		t.Fatalf("groups = %+v", groups)
	}
}
