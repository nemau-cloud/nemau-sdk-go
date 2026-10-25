package nemau

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// ComputeService covers /compute: instances, their power state, snapshots,
// keypairs, and the image and flavor catalogues.
type ComputeService struct{ client *Client }

// CreateInstanceRequest describes a new instance.
//
// Sizing is either a FlavorID or all three of VCPUs/RAMMB/DiskGB; the API
// rejects a partial custom size. FlavorID also requires the compute-flavors
// organization feature.
//
// The API accepts NetworkIDs but uses only its first entry, because an instance
// is attached to one network at creation. Prefer NetworkID.
//
// UserData is an optional cloud-init document (see [MaxUserDataBytes]) that the
// guest runs at its first boot. It is sealed by the platform, never returned by
// the API, and cannot be changed after creation: the instance only exposes its
// SHA-256 as [Instance.UserDataSHA256]. It may hold secrets. Empty means none.
type CreateInstanceRequest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	VCPUs          int      `json:"vcpus,omitempty"`
	RAMMB          int      `json:"ram_mb,omitempty"`
	DiskGB         int      `json:"disk_gb,omitempty"`
	FlavorID       string   `json:"flavor_id,omitempty"`
	ServiceLevel   string   `json:"service_level,omitempty"`
	ImageID        string   `json:"image_id"`
	NetworkID      string   `json:"network_id,omitempty"`
	NetworkIDs     []string `json:"network_ids,omitempty"`
	SecurityGroups []string `json:"security_groups,omitempty"`
	KeyName        string   `json:"key_name,omitempty"`
	UserData       string   `json:"user_data,omitempty"`
}

// ListInstances returns the instances in the credential's project.
func (s *ComputeService) ListInstances(ctx context.Context) ([]Instance, error) {
	var out struct {
		Instances []Instance `json:"instances"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/instances"}, &out)
	return out.Instances, err
}

// GetInstance returns one instance. It answers a 404 [APIError] when the id is
// unknown to the project.
func (s *ComputeService) GetInstance(ctx context.Context, id string) (*Instance, error) {
	var out struct {
		Instance *Instance `json:"instance"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/instances/" + url.PathEscape(id)}, &out)
	if err != nil {
		return nil, err
	}
	return out.Instance, nil
}

// CreateInstance queues an instance creation.
//
// It fails before sending anything when UserData breaks the platform's rules.
func (s *ComputeService) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*TaskRef, error) {
	if req.UserData != "" {
		if err := validateUserData(req.UserData); err != nil {
			return nil, err
		}
	}
	return s.client.submit(ctx, http.MethodPost, "/compute/instances", nil, req)
}

// CreateInstanceAndWait creates an instance and blocks until it is built,
// returning the new instance.
func (s *ComputeService) CreateInstanceAndWait(ctx context.Context, req CreateInstanceRequest) (*Instance, error) {
	ref, err := s.CreateInstance(ctx, req)
	if err != nil {
		return nil, err
	}
	id, _, err := s.client.waitForResourceID(ctx, ref, "instance_id")
	if err != nil {
		return nil, err
	}
	return s.GetInstance(ctx, id)
}

// DeleteInstance queues an instance deletion.
func (s *ComputeService) DeleteInstance(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/compute/instances/"+url.PathEscape(id), nil, nil)
}

// Start powers an instance on.
func (s *ComputeService) Start(ctx context.Context, id string) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "start", nil)
}

// Stop powers an instance off.
func (s *ComputeService) Stop(ctx context.Context, id string) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "stop", nil)
}

// Reboot restarts an instance. rebootType is "SOFT" (default) or "HARD".
func (s *ComputeService) Reboot(ctx context.Context, id, rebootType string) (*TaskRef, error) {
	body := map[string]string{}
	if rebootType != "" {
		body["type"] = rebootType
	}
	return s.instanceAction(ctx, id, "reboot", body)
}

// Pause freezes an instance in memory.
func (s *ComputeService) Pause(ctx context.Context, id string) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "pause", nil)
}

// Unpause resumes a paused instance.
func (s *ComputeService) Unpause(ctx context.Context, id string) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "unpause", nil)
}

// Suspend writes an instance's memory to disk and stops it.
func (s *ComputeService) Suspend(ctx context.Context, id string) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "suspend", nil)
}

// Resume restores a suspended instance.
func (s *ComputeService) Resume(ctx context.Context, id string) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "resume", nil)
}

// Resize changes an instance's sizing. All three values are required, and the
// API bounds them: 1-64 vCPUs, 1024-262144 MB of RAM, 10-5000 GB of disk.
func (s *ComputeService) Resize(ctx context.Context, id string, sizing Sizing) (*TaskRef, error) {
	return s.instanceAction(ctx, id, "resize", sizing)
}

// SetSecurityGroups replaces an instance's security groups with the given list;
// it does not add to the current ones.
//
// An empty slice removes all the groups, which is how to stop filtering an
// instance's traffic without deleting it. A nil slice is sent as an empty JSON
// array and has the same effect.
func (s *ComputeService) SetSecurityGroups(ctx context.Context, id string, groupIDs []string) (*TaskRef, error) {
	if groupIDs == nil {
		groupIDs = []string{}
	}
	return s.instanceAction(ctx, id, "security-groups", map[string][]string{"security_groups": groupIDs})
}

// instanceAction posts one of the PUT lifecycle verbs. Every action routed
// here is a PUT; snapshot and console are POSTs and are written out in full.
func (s *ComputeService) instanceAction(ctx context.Context, id, action string, body any) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPut, "/compute/instances/"+url.PathEscape(id)+"/"+action, nil, body)
}

// ListSnapshots returns snapshots in the project. A non-empty instanceID
// narrows the result to one instance.
func (s *ComputeService) ListSnapshots(ctx context.Context, instanceID string) ([]Snapshot, error) {
	q := url.Values{}
	if instanceID != "" {
		q.Set("instance_id", instanceID)
	}
	var out struct {
		Snapshots []Snapshot `json:"snapshots"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/snapshots", query: q}, &out)
	return out.Snapshots, err
}

// CreateSnapshot queues a snapshot of an instance's system disk.
//
// The task stays running for as long as the disk takes to reach the object
// store (minutes, or hours for a large disk), and the snapshot is listed with
// status "creating" in the meantime. Its result carries "snapshot_id" and
// "size_bytes".
func (s *ComputeService) CreateSnapshot(ctx context.Context, instanceID, name string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost,
		"/compute/instances/"+url.PathEscape(instanceID)+"/snapshot", nil,
		map[string]string{"name": name})
}

// DeleteSnapshot queues a snapshot deletion. A snapshot that is being created
// or restored cannot be deleted (409).
func (s *ComputeService) DeleteSnapshot(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/compute/snapshots/"+url.PathEscape(id), nil, nil)
}

// RestoreSnapshot queues the restoration of a snapshot onto the instance it was
// taken from.
//
// The data is first restored beside the instance while it keeps running, then
// its system disk is swapped: the instance is stopped only for the swap and
// started again if it was running. Everything written since the snapshot is
// lost. Addresses, security groups and attached volumes are left as they are,
// and the disk keeps its current size. Only snapshots with Restorable set can
// be restored.
func (s *ComputeService) RestoreSnapshot(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/compute/snapshots/"+url.PathEscape(id)+"/restore", nil, nil)
}

// ListImages returns the bootable images.
func (s *ComputeService) ListImages(ctx context.Context) ([]Image, error) {
	var out struct {
		Images []Image `json:"images"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/images"}, &out)
	return out.Images, err
}

// ListFlavors returns the organization's flavors. Requires the compute-flavors
// organization feature; without it the API answers 403.
func (s *ComputeService) ListFlavors(ctx context.Context) ([]Flavor, error) {
	var out struct {
		Flavors []Flavor `json:"flavors"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/flavors"}, &out)
	return out.Flavors, err
}

// FlavorRequest is a flavor's mutable shape.
type FlavorRequest struct {
	Name         string `json:"name"`
	VCPUs        int    `json:"vcpus"`
	RAMMB        int    `json:"ram_mb"`
	DiskGB       int    `json:"disk_gb"`
	ServiceLevel string `json:"service_level,omitempty"`
}

// CreateFlavor defines a flavor. Unlike most writes this one is synchronous and
// returns the flavor itself.
func (s *ComputeService) CreateFlavor(ctx context.Context, req FlavorRequest) (*Flavor, error) {
	var out struct {
		Flavor *Flavor `json:"flavor"`
	}
	err := s.client.do(ctx, request{method: http.MethodPost, path: "/compute/flavors", body: req}, &out)
	if err != nil {
		return nil, err
	}
	return out.Flavor, nil
}

// UpdateFlavor replaces a flavor's definition.
func (s *ComputeService) UpdateFlavor(ctx context.Context, id string, req FlavorRequest) (*Flavor, error) {
	var out struct {
		Flavor *Flavor `json:"flavor"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodPatch, path: "/compute/flavors/" + url.PathEscape(id), body: req,
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Flavor, nil
}

// DeleteFlavor removes a flavor.
func (s *ComputeService) DeleteFlavor(ctx context.Context, id string) error {
	return s.client.do(ctx, request{method: http.MethodDelete, path: "/compute/flavors/" + url.PathEscape(id)}, nil)
}

// GetPricing returns the region's rate card.
func (s *ComputeService) GetPricing(ctx context.Context) (map[string]any, error) {
	var out struct {
		Pricing map[string]any `json:"pricing"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/pricing"}, &out)
	return out.Pricing, err
}

// ListKeypairs returns the project's SSH keypairs.
func (s *ComputeService) ListKeypairs(ctx context.Context) ([]Keypair, error) {
	var out struct {
		Keypairs []Keypair `json:"keypairs"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/keypairs"}, &out)
	return out.Keypairs, err
}

// CreatedKeypair is the answer to a keypair creation.
//
// PrivateKey is set only when the platform generated the key, that is, when
// the request carried no public key. It is not stored and cannot be fetched
// again, so save it from this response.
type CreatedKeypair struct {
	Name       string
	PublicKey  string
	PrivateKey string
	Task       *Task
}

// CreateKeypair registers an SSH public key, or generates an Ed25519 keypair
// when publicKey is empty.
//
// This endpoint is synchronous: it returns the result instead of a task
// reference, because a generated private key is only available in this
// response.
func (s *ComputeService) CreateKeypair(ctx context.Context, name, publicKey string) (*CreatedKeypair, error) {
	body := map[string]string{"name": name}
	if publicKey != "" {
		body["public_key"] = publicKey
	}
	var task Task
	err := s.client.do(ctx, request{method: http.MethodPost, path: "/compute/keypairs", body: body}, &task)
	if err != nil {
		return nil, err
	}
	return &CreatedKeypair{
		Name:       name,
		PublicKey:  task.ResultString("public_key"),
		PrivateKey: task.ResultString("private_key"),
		Task:       &task,
	}, nil
}

// DeleteKeypair removes a keypair by name, not by id.
//
// A name containing a slash cannot be addressed here: the API routes on the
// decoded path, so such a keypair can be created and never deleted. Avoid
// slashes in keypair names.
func (s *ComputeService) DeleteKeypair(ctx context.Context, name string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/compute/keypairs/"+url.PathEscape(name), nil, nil)
}

// MetricsPeriods are the periods the metrics endpoints accept.
var MetricsPeriods = []string{"1h", "6h", "24h", "7d"}

// ValidateMetricsPeriod checks period against [MetricsPeriods]; empty is valid
// and means the default (1h). The server enforces the same list: this only
// saves a round trip.
func ValidateMetricsPeriod(period string) error {
	if period == "" || slices.Contains(MetricsPeriods, period) {
		return nil
	}
	return fmt.Errorf("nemau: invalid metrics period %q: want one of %s", period, strings.Join(MetricsPeriods, ", "))
}

// InstanceMetrics returns an instance's usage metrics. period is 1h (default),
// 6h, 24h or 7d.
func (s *ComputeService) InstanceMetrics(ctx context.Context, id, period string) (map[string]any, error) {
	if err := ValidateMetricsPeriod(period); err != nil {
		return nil, err
	}
	q := url.Values{}
	if period != "" {
		q.Set("period", period)
	}
	var out map[string]any
	err := s.client.do(ctx, request{
		method: http.MethodGet, path: "/compute/instances/" + url.PathEscape(id) + "/metrics", query: q,
	}, &out)
	return out, err
}
