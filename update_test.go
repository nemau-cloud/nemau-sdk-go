package nemau

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// captured records the one request an update makes.
type captured struct {
	method string
	path   string
	query  string
	body   map[string]any
}

func captureUpdate(t *testing.T) (*Client, *captured) {
	t.Helper()
	seen := &captured{}
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen.method, seen.path, seen.query = r.Method, r.URL.Path, r.URL.RawQuery
		_ = json.Unmarshal(raw, &seen.body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t-1","status":"pending"}`))
	}))
	return client, seen
}

func TestUpdateSendsOnlyTheFieldsThatWereSet(t *testing.T) {
	client, seen := captureUpdate(t)

	if _, err := client.Network.UpdateNetwork(context.Background(), "net-1",
		Update{Description: Set("prod segment")}); err != nil {
		t.Fatalf("UpdateNetwork: %v", err)
	}

	if seen.method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", seen.method)
	}
	if seen.path != "/api/v1/network/networks/net-1" {
		t.Errorf("path = %s", seen.path)
	}
	// An unset field must stay out of the body, so that a caller does not clear
	// fields it did not change.
	if _, present := seen.body["name"]; present {
		t.Errorf("an unset field was sent: %v", seen.body)
	}
	if seen.body["description"] != "prod segment" {
		t.Errorf("description = %v", seen.body["description"])
	}
}

func TestUpdateCanSetAFieldToEmpty(t *testing.T) {
	client, seen := captureUpdate(t)

	// Clearing a description is a valid change; this is why the fields are
	// pointers and not plain strings with omitempty.
	if _, err := client.Network.UpdateNetwork(context.Background(), "net-1",
		Update{Description: Set("")}); err != nil {
		t.Fatalf("UpdateNetwork: %v", err)
	}
	value, present := seen.body["description"]
	if !present || value != "" {
		t.Errorf("body = %v, want an explicit empty description", seen.body)
	}
}

func TestEmptyUpdateNeverReachesTheAPI(t *testing.T) {
	client, seen := captureUpdate(t)

	_, err := client.Storage.UpdateVolume(context.Background(), "vol-1", Update{})
	if !errors.Is(err, errNothingToUpdate) {
		t.Fatalf("err = %v, want errNothingToUpdate", err)
	}
	if seen.method != "" {
		t.Error("an empty update still made a request")
	}
}

func TestVolumeUpdateCarriesTheIDInTheQuery(t *testing.T) {
	client, seen := captureUpdate(t)

	// A volume id can contain a slash; in the path it would add a segment, and
	// the request would not match the route.
	const volid = "vol:104/disk-0"
	if _, err := client.Storage.UpdateVolume(context.Background(), volid,
		Update{Name: Set("data")}); err != nil {
		t.Fatalf("UpdateVolume: %v", err)
	}
	if seen.path != "/api/v1/storage/volumes" {
		t.Errorf("path = %s", seen.path)
	}
	if seen.query == "" {
		t.Errorf("the volume id did not travel as a query parameter")
	}
}

func TestUpdatePathsPerResource(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(*Client) (*TaskRef, error)
		path string
	}{
		{"instance", func(c *Client) (*TaskRef, error) {
			return c.Compute.UpdateInstance(ctx, "i-1", Update{Name: Set("web-2")})
		}, "/api/v1/compute/instances/i-1"},
		{"security group", func(c *Client) (*TaskRef, error) {
			return c.Network.UpdateSecurityGroup(ctx, "sg-1", Update{Name: Set("web")})
		}, "/api/v1/network/security-groups/sg-1"},
		{"pool member", func(c *Client) (*TaskRef, error) {
			return c.LoadBalancer.UpdatePoolMember(ctx, "p-1", "m-1", Update{Weight: Set(3)})
		}, "/api/v1/network/pools/p-1/members/m-1"},
		{"health monitor", func(c *Client) (*TaskRef, error) {
			return c.LoadBalancer.UpdateHealthMonitor(ctx, "p-1", "hm-1", Update{Delay: Set(10)})
		}, "/api/v1/network/pools/p-1/health-monitors/hm-1"},
		{"container", func(c *Client) (*TaskRef, error) {
			return c.Storage.UpdateContainer(ctx, "backups", Update{Versioning: Set(true)})
		}, "/api/v1/storage/containers/backups"},
		{"dns zone", func(c *Client) (*TaskRef, error) {
			return c.DNS.UpdateZone(ctx, "example.com.", Update{TTL: Set(600)})
		}, "/api/v1/dns/zones/example.com."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, seen := captureUpdate(t)
			if _, err := tc.call(client); err != nil {
				t.Fatalf("update: %v", err)
			}
			if seen.path != tc.path {
				t.Errorf("path = %s, want %s", seen.path, tc.path)
			}
		})
	}
}

func TestPatchIsReplayedAfterATransportFailure(t *testing.T) {
	// A PATCH body on this API is a set of fields and their values, not a
	// delta, so replaying it gives the same state.
	if !idempotent(http.MethodPatch) {
		t.Error("PATCH is not replayable, so a lost response becomes a lost update")
	}
}
