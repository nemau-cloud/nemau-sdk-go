package nemau

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestLoadBalancerListFlavors(t *testing.T) {
	cap := &capture{resp: `{"flavors":[
		{"name":"lb-small","appliances":1,"vcpus":1,"ram_mb":1024,"high_availability":false,"hourly_price":0.0074,"monthly_price":5.4,"currency":"eur"},
		{"name":"lb-ha","appliances":2,"vcpus":1,"ram_mb":1024,"high_availability":true,"hourly_price":0.0141,"monthly_price":10.3,"currency":"eur"}
	],"default":"lb-small"}`}
	c, _ := newTestClient(t, cap.handler())
	flavors, err := c.LoadBalancer.ListFlavors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/v1/network/load-balancer-flavors" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if len(flavors) != 2 || flavors[1].Name != "lb-ha" || !flavors[1].HighAvailability || flavors[1].Appliances != 2 || flavors[1].MonthlyPrice != 10.3 {
		t.Fatalf("flavors = %+v", flavors)
	}
}

func TestCreateLoadBalancerSendsTheFlavorOnlyWhenSet(t *testing.T) {
	for _, tc := range []struct {
		flavor string
		want   string
	}{{"lb-medium", `"flavor":"lb-medium"`}, {"", ""}} {
		cap := &capture{}
		c, _ := newTestClient(t, cap.handler())
		if _, err := c.LoadBalancer.Create(context.Background(), CreateLoadBalancerRequest{
			Name: "edge", VipSubnetID: "sub-1", Flavor: tc.flavor,
		}); err != nil {
			t.Fatal(err)
		}
		if tc.want != "" && !strings.Contains(cap.body, tc.want) {
			t.Fatalf("body %s does not carry %s", cap.body, tc.want)
		}
		if tc.want == "" && strings.Contains(cap.body, "flavor") {
			t.Fatalf("an unset flavor was sent: %s", cap.body)
		}
	}
}

// The create body carries no engine: the API applies automatic mode.
func TestCreateLoadBalancerSendsNoEngine(t *testing.T) {
	cap := &capture{}
	c, _ := newTestClient(t, cap.handler())
	if _, err := c.LoadBalancer.Create(context.Background(), CreateLoadBalancerRequest{
		Name: "edge", VipSubnetID: "sub-1",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cap.body, "provider") {
		t.Fatalf("the create body carries an engine: %s", cap.body)
	}
}

func TestLoadBalancerDecodesTheHAState(t *testing.T) {
	cap := &capture{resp: `{"load_balancer":{"id":"lb-1","name":"edge","flavor":"lb-ha","provider":"auto",
		"provisioning_status":"ACTIVE","admin_state_up":true,"listeners":[],"pools":[],
		"high_availability":{"status":"DEGRADED","reason":"SINGLE_HOST","appliances":[
			{"index":1,"state":"ACTIVE","address":"10.0.0.11"},{"index":2,"state":"STANDBY","address":"10.0.0.12"}]}}}`}
	c, _ := newTestClient(t, cap.handler())
	lb, err := c.LoadBalancer.Get(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	ha := lb.HighAvailability
	if lb.Provider != "auto" || lb.Flavor != "lb-ha" || ha == nil || ha.Status != "DEGRADED" || ha.Reason != "SINGLE_HOST" ||
		len(ha.Appliances) != 2 || ha.Appliances[0].State != "ACTIVE" || ha.Appliances[1].Address != "10.0.0.12" {
		t.Fatalf("load balancer = %+v, ha = %+v", lb, ha)
	}
}
