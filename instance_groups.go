package nemau

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// InstanceGroupsService covers /compute/instance-groups: groups of identical
// instances that the platform keeps between a minimum and a maximum size, and
// that scale with target-tracking policies and scheduled actions.
//
// A group and its policies and schedules are free. Its instances are billed as
// any instance is. Growing a group needs a billable organization.
type InstanceGroupsService struct{ client *Client }

// Limits of an instance group, mirroring the platform's rules.
const (
	InstanceGroupMaxSize       = 50
	InstanceGroupMaxNameLength = 56
	InstanceGroupMaxPools      = 4
	InstanceGroupMaxSecurity   = 5
	InstanceGroupMaxPolicies   = 2
	InstanceGroupMaxSchedules  = 10
	// Bounds of a refresh's shares: unavailable share 0 to 50 percent,
	// surge 1 to 10 members.
	InstanceGroupMaxUnavailablePct = 50
	InstanceGroupMinSurge          = 1
	InstanceGroupMaxSurge          = 10
)

// Values of [InstanceGroupRefresh].Status.
const (
	// RefreshIdle means no refresh has run since the group was created.
	RefreshIdle = "idle"
	// RefreshInProgress means the group is replacing members with an older template version.
	RefreshInProgress = "in_progress"
	// RefreshHold means the refresh is held, and RefreshBlocked that it cannot
	// move until the group has room or a member is allowed out (a size change).
	RefreshHold    = "hold"
	RefreshBlocked = "blocked"
	// RefreshCompleted, RefreshCancelled and RefreshFailed are the three ends of a refresh.
	RefreshCompleted = "completed"
	RefreshCancelled = "cancelled"
	RefreshFailed    = "failed"
)

// Values of the group's enumerated fields.
const (
	// ServiceLevelBestEffort and ServiceLevel999 are the two service levels of
	// a member instance. The empty string means the default tier.
	ServiceLevelBestEffort = "best-effort"
	ServiceLevel999        = "99.9%"
	// HealthCheckInstance is the only health check available: a member is
	// unhealthy when its guest stops answering the platform.
	HealthCheckInstance = "instance"
	// MetricCPUPercent is the only scaling metric available.
	MetricCPUPercent = "cpu_percent"
)

var (
	instanceGroupNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	instanceGroupImageRef    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	cronFieldPattern         = regexp.MustCompile(`^[0-9A-Za-z*/,-]{1,32}$`)
)

// InstanceGroup is a group as the API reports it. Members and Activities are
// filled by [InstanceGroupsService.Get]; List returns them as empty slices.
// The view names the pools "pools", while the create and update requests send
// them as "load_balancer_pools", the key the API takes. The asymmetry is the
// API's; the SDK keeps each key as the API names it.
// The API always sends the list fields as arrays, so an empty list decodes to
// a non-nil, zero-length slice. The SDK does not normalise a missing or null
// key: such a field stays nil.
type InstanceGroup struct {
	ID                      string             `json:"id"`
	Name                    string             `json:"name"`
	Description             string             `json:"description"`
	Status                  string             `json:"status"`
	HealthCheck             string             `json:"health_check"`
	HealthCheckGraceSeconds int                `json:"health_check_grace_seconds"`
	DrainSeconds            int                `json:"drain_seconds"`
	MinSize                 int                `json:"min_size"`
	MaxSize                 int                `json:"max_size"`
	DesiredCapacity         int                `json:"desired_capacity"`
	InstanceCount           int                `json:"instance_count"`
	MaxSurge                int                `json:"max_surge"`
	MaxUnavailablePct       int                `json:"max_unavailable_pct"`
	TemplateVersion         int                `json:"template_version"`
	CreatedAt               time.Time          `json:"created_at"`
	UpdatedAt               time.Time          `json:"updated_at"`
	LaunchTemplate          LaunchTemplateInfo `json:"launch_template"`
	Pools                   []GroupPool        `json:"pools"`
	ScalingPolicies         []ScalingPolicy    `json:"scaling_policies"`
	Schedules               []Schedule         `json:"schedules"`
	// Refresh is the state of the last rolling refresh. It is nil when the API
	// sends none.
	Refresh    *InstanceGroupRefresh   `json:"refresh"`
	Members    []InstanceGroupMember   `json:"members"`
	Activities []InstanceGroupActivity `json:"activities"`
}

// InstanceGroupRefresh is the state of a rolling refresh: the replacement of
// members so that every one runs TargetVersion of the launch template.
type InstanceGroupRefresh struct {
	// Status is one of the Refresh* constants.
	Status        string    `json:"status"`
	TargetVersion int       `json:"target_version"`
	StartedAt     time.Time `json:"started_at"`
	// CompletedAt is nil until the refresh ends.
	CompletedAt *time.Time `json:"completed_at"`
	// SkippedProtected counts the protected members the refresh left on an
	// older template version.
	SkippedProtected int `json:"skipped_protected"`
}

// RefreshInstanceGroupRequest starts a rolling refresh. A nil share keeps the
// group's stored setting. The shares of the refresh are the ones it runs with.
type RefreshInstanceGroupRequest struct {
	// MaxUnavailablePct is the share of members that may be replaced at once,
	// 0 to 50.
	MaxUnavailablePct *int `json:"max_unavailable_pct,omitempty"`
	// MaxSurge is how many extra members may run during a replacement, 1 to 10.
	MaxSurge *int `json:"max_surge,omitempty"`
}

// LaunchTemplateInfo is the template of a group as the API reports it, at
// TemplateVersion. The user_data document is never returned: UserDataPresent
// says whether one was set, and UserDataSHA256 identifies it without showing it.
type LaunchTemplateInfo struct {
	Version          int      `json:"version"`
	FlavorID         string   `json:"flavor_id"`
	ImageID          string   `json:"image_id"`
	VCPUs            int      `json:"vcpus"`
	RAMMB            int      `json:"ram_mb"`
	DiskGB           int      `json:"disk_gb"`
	NetworkID        string   `json:"network_id"`
	SubnetID         string   `json:"subnet_id"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	KeyName          string   `json:"key_name"`
	UserDataPresent  bool     `json:"user_data_present"`
	UserDataSHA256   string   `json:"user_data_sha256"`
}

// InstanceGroupMember is one instance of a group.
type InstanceGroupMember struct {
	InstanceID           string    `json:"instance_id"`
	Status               string    `json:"status"`
	ProtectedFromScaleIn bool      `json:"protected_from_scale_in"`
	TemplateVersion      int       `json:"template_version"`
	JoinedAt             time.Time `json:"joined_at"`
}

// InstanceGroupActivity is one decision of the group's controller, such as a
// scale-out or the replacement of an unhealthy member. InstanceID is empty when
// the decision is not about one instance, such as a capacity change.
type InstanceGroupActivity struct {
	ID         int64     `json:"id"`
	Type       string    `json:"type"`
	Reason     string    `json:"reason"`
	InstanceID string    `json:"instance_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// GroupPool is a load balancer pool the group registers its members in, and
// the port of the members within it.
type GroupPool struct {
	PoolID       string `json:"pool_id"`
	ProtocolPort int    `json:"protocol_port"`
}

// LaunchTemplate is what each member of a group is created from. On an update
// it is a new template version, and its fields replace the stored ones.
//
// Sizing is either FlavorID or all of VCPUs, RAMMB and DiskGB. A create needs
// an ImageID. UserData is an optional cloud-init document (see
// [MaxUserDataBytes]). It may hold secrets: the platform seals it and never
// returns it. Empty means none.
type LaunchTemplate struct {
	ImageID      string `json:"image_id,omitempty"`
	FlavorID     string `json:"flavor_id,omitempty"`
	VCPUs        int    `json:"vcpus,omitempty"`
	RAMMB        int    `json:"ram_mb,omitempty"`
	DiskGB       int    `json:"disk_gb,omitempty"`
	KeyName      string `json:"key_name,omitempty"`
	ServiceLevel string `json:"service_level,omitempty"`
	UserData     string `json:"user_data,omitempty"`
}

// ScalingPolicy is a target-tracking policy: the group adds or removes members
// to keep Metric near Target. Zero values of the optional fields take the
// platform's defaults. The read form carries the effective values.
type ScalingPolicy struct {
	Metric                  string `json:"metric"`
	Target                  int    `json:"target"`
	WindowSeconds           int    `json:"window_seconds,omitempty"`
	ScaleOutCooldownSeconds int    `json:"scale_out_cooldown_seconds,omitempty"`
	ScaleInCooldownSeconds  int    `json:"scale_in_cooldown_seconds,omitempty"`
	ScaleInWindowSeconds    int    `json:"scale_in_window_seconds,omitempty"`
	MaxStepOut              int    `json:"max_step_out,omitempty"`
	MaxStepIn               int    `json:"max_step_in,omitempty"`
	// ScaleInEnabled is nil to take the default.
	ScaleInEnabled *bool `json:"scale_in_enabled,omitempty"`
}

// Schedule is a scheduled action: at each UTC cron time it sets the bounds it
// names. ID is set by the platform when the schedule is read.
type Schedule struct {
	ID              string `json:"id,omitempty"`
	Cron            string `json:"cron"`
	MinSize         *int   `json:"min_size,omitempty"`
	MaxSize         *int   `json:"max_size,omitempty"`
	DesiredCapacity *int   `json:"desired_capacity,omitempty"`
	// LastSlot is the last UTC time the schedule fired. The platform sets it
	// when the schedule is read; it is never sent.
	LastSlot *time.Time `json:"last_slot,omitempty"`
}

// CreateInstanceGroupRequest describes a new group. MinSize and MaxSize are
// required. DesiredCapacity defaults to MinSize.
type CreateInstanceGroupRequest struct {
	Name                    string          `json:"name"`
	NetworkID               string          `json:"network_id"`
	SubnetID                string          `json:"subnet_id"`
	MinSize                 int             `json:"min_size"`
	MaxSize                 int             `json:"max_size"`
	DesiredCapacity         *int            `json:"desired_capacity,omitempty"`
	SecurityGroups          []string        `json:"security_groups,omitempty"`
	LaunchTemplate          *LaunchTemplate `json:"launch_template"`
	LoadBalancerPools       []GroupPool     `json:"load_balancer_pools,omitempty"`
	HealthCheck             string          `json:"health_check,omitempty"`
	HealthCheckGraceSeconds *int            `json:"health_check_grace_seconds,omitempty"`
	DrainSeconds            *int            `json:"drain_seconds,omitempty"`
	ScalingPolicies         []ScalingPolicy `json:"scaling_policies,omitempty"`
}

// UpdateInstanceGroupRequest changes a group. A nil field is left as it is.
//
// The slice fields are pointers to slices so that an empty list can be told
// from an absent one: &[]string{} clears the security groups, nil keeps them.
// The same holds for LoadBalancerPools, ScalingPolicies and Schedules, where a
// list replaces the stored one.
type UpdateInstanceGroupRequest struct {
	MinSize                 *int             `json:"min_size,omitempty"`
	MaxSize                 *int             `json:"max_size,omitempty"`
	MaxUnavailablePct       *int             `json:"max_unavailable_pct,omitempty"`
	MaxSurge                *int             `json:"max_surge,omitempty"`
	SecurityGroups          *[]string        `json:"security_groups,omitempty"`
	LaunchTemplate          *LaunchTemplate  `json:"launch_template,omitempty"`
	LoadBalancerPools       *[]GroupPool     `json:"load_balancer_pools,omitempty"`
	HealthCheck             string           `json:"health_check,omitempty"`
	HealthCheckGraceSeconds *int             `json:"health_check_grace_seconds,omitempty"`
	DrainSeconds            *int             `json:"drain_seconds,omitempty"`
	ScalingPolicies         *[]ScalingPolicy `json:"scaling_policies,omitempty"`
	Schedules               *[]Schedule      `json:"schedules,omitempty"`
}

// List returns the instance groups of the credential's project.
func (s *InstanceGroupsService) List(ctx context.Context) ([]InstanceGroup, error) {
	var out struct {
		InstanceGroups []InstanceGroup `json:"instance_groups"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/compute/instance-groups"}, &out)
	return out.InstanceGroups, err
}

// Get returns one group with its members and its recent activities.
func (s *InstanceGroupsService) Get(ctx context.Context, id string) (*InstanceGroup, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	var out struct {
		InstanceGroup *InstanceGroup `json:"instance_group"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: groupPath(id)}, &out)
	if err != nil {
		return nil, err
	}
	return out.InstanceGroup, nil
}

// Activities returns the group's recent decisions, newest first. The platform
// keeps the last 200.
func (s *InstanceGroupsService) Activities(ctx context.Context, id string) ([]InstanceGroupActivity, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	var out struct {
		Activities []InstanceGroupActivity `json:"activities"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: groupPath(id) + "/activities"}, &out)
	return out.Activities, err
}

// Create queues a group creation.
func (s *InstanceGroupsService) Create(ctx context.Context, req CreateInstanceGroupRequest) (*TaskRef, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	return s.client.submit(ctx, http.MethodPost, "/compute/instance-groups", nil, req)
}

// Update queues a change of the group's bounds, template, pools, health check,
// policies or schedules. A template given is a new version of it.
func (s *InstanceGroupsService) Update(ctx context.Context, id string, req UpdateInstanceGroupRequest) (*TaskRef, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	return s.client.submit(ctx, http.MethodPatch, groupPath(id), nil, req)
}

// Refresh queues a rolling refresh that replaces the group's members so that
// they run its current launch template version, within the shares given.
func (s *InstanceGroupsService) Refresh(ctx context.Context, id string, req RefreshInstanceGroupRequest) (*TaskRef, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	if req.MaxUnavailablePct != nil && (*req.MaxUnavailablePct < 0 || *req.MaxUnavailablePct > InstanceGroupMaxUnavailablePct) {
		return nil, fmt.Errorf("nemau: max unavailable percent must be between 0 and %d", InstanceGroupMaxUnavailablePct)
	}
	if req.MaxSurge != nil && (*req.MaxSurge < InstanceGroupMinSurge || *req.MaxSurge > InstanceGroupMaxSurge) {
		return nil, fmt.Errorf("nemau: max surge must be between %d and %d", InstanceGroupMinSurge, InstanceGroupMaxSurge)
	}
	return s.client.submit(ctx, http.MethodPost, groupPath(id)+"/refresh", nil, req)
}

// CancelRefresh queues the cancellation of the group's refresh in progress.
func (s *InstanceGroupsService) CancelRefresh(ctx context.Context, id string) (*TaskRef, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	return s.client.submit(ctx, http.MethodPost, groupPath(id)+"/refresh/cancel", nil, nil)
}

// SetCapacity queues a new desired capacity. Growing the group past its current
// desired capacity needs a billable organization; shrinking it does not.
func (s *InstanceGroupsService) SetCapacity(ctx context.Context, id string, desiredCapacity int) (*TaskRef, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	if err := checkGroupSize("desired capacity", desiredCapacity); err != nil {
		return nil, err
	}
	body := struct {
		DesiredCapacity int `json:"desired_capacity"`
	}{desiredCapacity}
	return s.client.submit(ctx, http.MethodPut, groupPath(id)+"/capacity", nil, body)
}

// SetMemberProtection marks one member as protected from scale-in, or unmarks
// it. The platform refuses an instance that is not a member of this group.
func (s *InstanceGroupsService) SetMemberProtection(ctx context.Context, groupID, instanceID string, protected bool) (*TaskRef, error) {
	if err := checkID("instance group id", groupID); err != nil {
		return nil, err
	}
	if err := checkID("instance id", instanceID); err != nil {
		return nil, err
	}
	body := struct {
		Protected bool `json:"protected"`
	}{protected}
	path := groupPath(groupID) + "/members/" + url.PathEscape(instanceID) + "/protection"
	return s.client.submit(ctx, http.MethodPut, path, nil, body)
}

// Delete queues a group deletion. A group that still has members is refused
// unless deleteInstances is true, which deletes its members with it.
func (s *InstanceGroupsService) Delete(ctx context.Context, id string, deleteInstances bool) (*TaskRef, error) {
	if err := checkID("instance group id", id); err != nil {
		return nil, err
	}
	var query url.Values
	if deleteInstances {
		query = url.Values{"delete_instances": {"true"}}
	}
	return s.client.submit(ctx, http.MethodDelete, groupPath(id), query, nil)
}

// groupPath is the path of one group, with its id escaped as one segment.
func groupPath(id string) string {
	return "/compute/instance-groups/" + url.PathEscape(id)
}

// checkGroupSize applies the 0 to 50 range of a size or a desired capacity.
func checkGroupSize(what string, v int) error {
	if v < 0 || v > InstanceGroupMaxSize {
		return fmt.Errorf("nemau: %s must be between 0 and %d", what, InstanceGroupMaxSize)
	}
	return nil
}

// checkGroupName applies the name rule: 1 to 56 characters of lowercase
// letters, digits and hyphens, starting and ending with a letter or a digit.
func checkGroupName(name string) error {
	if len(name) > InstanceGroupMaxNameLength || !instanceGroupNamePattern.MatchString(name) {
		return errors.New("nemau: instance group name must be 1 to 56 lowercase letters, digits or hyphens, starting and ending with a letter or digit")
	}
	return nil
}

// checkBounds judges min <= desired <= max.
func checkBounds(minSize, maxSize, desired int) error {
	if minSize < 0 || maxSize > InstanceGroupMaxSize || !(minSize <= desired && desired <= maxSize) {
		return errors.New("nemau: min_size, desired_capacity and max_size must satisfy min_size <= desired_capacity <= max_size")
	}
	return nil
}

// check judges a template. A create needs an image and sizing;
// an update needs sizing only when it restates some of it.
func (t LaunchTemplate) check(create bool) error {
	if t.ImageID == "" && create {
		return errors.New("launch_template.image_id is required")
	}
	if t.ImageID != "" && !instanceGroupImageRef.MatchString(t.ImageID) {
		return errors.New("launch_template.image_id is not a valid image id")
	}
	if t.FlavorID != "" && !instanceGroupImageRef.MatchString(t.FlavorID) {
		return errors.New("launch_template.flavor_id is not a valid flavor id")
	}
	switch strings.ToLower(strings.TrimSpace(t.ServiceLevel)) {
	case "", "besteffort", "best-effort", "best effort", "99.9", "99.9%":
	default:
		return errors.New("launch_template.service_level must be 99.9% or best-effort")
	}
	sizing := t.VCPUs != 0 || t.RAMMB != 0 || t.DiskGB != 0
	if t.FlavorID == "" && (create || sizing) {
		if t.VCPUs == 0 || t.RAMMB == 0 || t.DiskGB == 0 {
			return errors.New("launch_template needs flavor_id or vcpus, ram_mb and disk_gb together")
		}
		if t.VCPUs < 1 || t.VCPUs > 64 {
			return errors.New("launch_template.vcpus must be between 1 and 64")
		}
		if t.RAMMB < 1024 || t.RAMMB > 262144 || t.RAMMB%256 != 0 {
			return errors.New("launch_template.ram_mb must be a multiple of 256 between 1024 and 262144")
		}
		if t.DiskGB < 10 || t.DiskGB > 5000 {
			return errors.New("launch_template.disk_gb must be between 10 and 5000")
		}
	}
	if t.UserData != "" {
		if err := validateUserData(t.UserData); err != nil {
			return err
		}
	}
	return nil
}

// checkPools judges the pools: at most 4, each a pool id and a port from 1 to
// 65535, no pool listed twice.
func checkPools(pools []GroupPool) error {
	if len(pools) > InstanceGroupMaxPools {
		return fmt.Errorf("nemau: a group accepts at most %d load balancer pools", InstanceGroupMaxPools)
	}
	seen := make(map[string]bool, len(pools))
	for _, p := range pools {
		if err := checkUUID("load balancer pool id", p.PoolID); err != nil {
			return err
		}
		if p.ProtocolPort < 1 || p.ProtocolPort > 65535 {
			return errors.New("nemau: load balancer pool protocol_port must be between 1 and 65535")
		}
		if seen[p.PoolID] {
			return errors.New("nemau: a load balancer pool is listed twice")
		}
		seen[p.PoolID] = true
	}
	return nil
}

func checkSecurityGroups(ids []string) error {
	if len(ids) > InstanceGroupMaxSecurity {
		return fmt.Errorf("nemau: a group accepts at most %d security groups", InstanceGroupMaxSecurity)
	}
	for _, id := range ids {
		if err := checkUUID("security group id", id); err != nil {
			return err
		}
	}
	return nil
}

// checkPolicies judges the policies: at most 2, one per metric, cpu_percent
// only, and each value inside its range.
func checkPolicies(policies []ScalingPolicy) error {
	if len(policies) > InstanceGroupMaxPolicies {
		return fmt.Errorf("nemau: a group accepts at most %d scaling policies", InstanceGroupMaxPolicies)
	}
	seen := make(map[string]bool, len(policies))
	for _, p := range policies {
		if p.Metric != MetricCPUPercent {
			return errors.New("nemau: scaling policy metric must be cpu_percent")
		}
		if seen[p.Metric] {
			return errors.New("nemau: a scaling policy metric is listed twice")
		}
		seen[p.Metric] = true
		if err := checkRange("target", p.Target, 10, 90); err != nil {
			return err
		}
		for _, r := range []struct {
			field    string
			value    int
			min, max int
		}{
			{"window_seconds", p.WindowSeconds, 180, 900},
			{"scale_out_cooldown_seconds", p.ScaleOutCooldownSeconds, 60, 3600},
			{"scale_in_cooldown_seconds", p.ScaleInCooldownSeconds, 60, 3600},
			{"scale_in_window_seconds", p.ScaleInWindowSeconds, 300, 3600},
			{"max_step_out", p.MaxStepOut, 1, 10},
			{"max_step_in", p.MaxStepIn, 1, 5},
		} {
			// Zero is the default, which the platform applies.
			if r.value == 0 {
				continue
			}
			if err := checkRange(r.field, r.value, r.min, r.max); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkRange(field string, v, min, max int) error {
	if v < min || v > max {
		return fmt.Errorf("nemau: scaling policy %s must be between %d and %d", field, min, max)
	}
	return nil
}

// checkSchedules judges the schedules: at most 10, each a 5-field UTC cron and
// at least one bound, every bound in range, min <= max, and
// min <= desired <= max where they are set together.
func checkSchedules(schedules []Schedule) error {
	if len(schedules) > InstanceGroupMaxSchedules {
		return fmt.Errorf("nemau: a group accepts at most %d schedules", InstanceGroupMaxSchedules)
	}
	for _, s := range schedules {
		if err := checkCron(s.Cron); err != nil {
			return err
		}
		if s.MinSize == nil && s.MaxSize == nil && s.DesiredCapacity == nil {
			return errors.New("nemau: a schedule sets at least one of min_size, max_size, desired_capacity")
		}
		for _, v := range []*int{s.MinSize, s.MaxSize, s.DesiredCapacity} {
			if v != nil {
				if err := checkGroupSize("schedule size", *v); err != nil {
					return err
				}
			}
		}
		if s.MinSize != nil && s.MaxSize != nil && *s.MinSize > *s.MaxSize {
			return errors.New("nemau: a schedule's min_size must not exceed its max_size")
		}
		if s.MinSize != nil && s.DesiredCapacity != nil && s.MaxSize != nil {
			if err := checkBounds(*s.MinSize, *s.MaxSize, *s.DesiredCapacity); err != nil {
				return errors.New("nemau: a schedule must satisfy min_size <= desired_capacity <= max_size")
			}
		}
	}
	return nil
}

// checkCron accepts five whitespace-separated cron fields and nothing else.
// The platform parses the expression for real.
func checkCron(spec string) error {
	fields := strings.Fields(spec)
	if len(fields) != 5 || len(strings.Join(fields, " ")) > 128 {
		return errors.New("nemau: schedule cron must be 5 fields, in UTC")
	}
	for _, f := range fields {
		if !cronFieldPattern.MatchString(f) {
			return errors.New("nemau: schedule cron must be 5 fields, in UTC")
		}
	}
	return nil
}

// checkScaling judges what create and update share: pools, health check and
// its timings, and the policies. Absent (zero) fields take the defaults.
func checkScaling(pools []GroupPool, healthCheck string, grace, drain int, policies []ScalingPolicy) error {
	if err := checkPools(pools); err != nil {
		return err
	}
	switch healthCheck {
	case "", HealthCheckInstance:
	default:
		return errors.New("nemau: health_check must be instance")
	}
	if grace != 0 && (grace < 60 || grace > 1800) {
		return errors.New("nemau: health_check_grace_seconds must be between 60 and 1800")
	}
	if drain < 0 || drain > 300 {
		return errors.New("nemau: drain_seconds must be between 0 and 300")
	}
	return checkPolicies(policies)
}

func (r CreateInstanceGroupRequest) validate() error {
	if err := checkGroupName(r.Name); err != nil {
		return err
	}
	if err := checkUUID("network id", r.NetworkID); err != nil {
		return err
	}
	if err := checkUUID("subnet id", r.SubnetID); err != nil {
		return err
	}
	if err := checkGroupSize("min_size", r.MinSize); err != nil {
		return err
	}
	if err := checkGroupSize("max_size", r.MaxSize); err != nil {
		return err
	}
	desired := r.MinSize
	if r.DesiredCapacity != nil {
		if err := checkGroupSize("desired_capacity", *r.DesiredCapacity); err != nil {
			return err
		}
		desired = *r.DesiredCapacity
	}
	if r.MinSize > r.MaxSize || !(r.MinSize <= desired && desired <= r.MaxSize) {
		return errors.New("nemau: min_size, desired_capacity and max_size must satisfy min_size <= desired_capacity <= max_size")
	}
	if r.LaunchTemplate == nil {
		return errors.New("nemau: launch_template is required")
	}
	if err := r.LaunchTemplate.check(true); err != nil {
		return err
	}
	if err := checkSecurityGroups(r.SecurityGroups); err != nil {
		return err
	}
	return checkScaling(r.LoadBalancerPools, r.HealthCheck, deref(r.HealthCheckGraceSeconds), deref(r.DrainSeconds), r.ScalingPolicies)
}

func (r UpdateInstanceGroupRequest) validate() error {
	if r.MinSize == nil && r.MaxSize == nil && r.MaxUnavailablePct == nil && r.MaxSurge == nil &&
		r.SecurityGroups == nil && r.LaunchTemplate == nil && r.LoadBalancerPools == nil && r.HealthCheck == "" &&
		r.HealthCheckGraceSeconds == nil && r.DrainSeconds == nil && r.ScalingPolicies == nil && r.Schedules == nil {
		return errors.New("nemau: the update names no field")
	}
	for _, v := range []*int{r.MinSize, r.MaxSize} {
		if v != nil {
			if err := checkGroupSize("size", *v); err != nil {
				return err
			}
		}
	}
	if r.MinSize != nil && r.MaxSize != nil && *r.MinSize > *r.MaxSize {
		return errors.New("nemau: min_size must not exceed max_size")
	}
	if r.MaxUnavailablePct != nil && (*r.MaxUnavailablePct < 0 || *r.MaxUnavailablePct > 50) {
		return errors.New("nemau: max_unavailable_pct must be between 0 and 50")
	}
	if r.MaxSurge != nil && (*r.MaxSurge < 1 || *r.MaxSurge > 10) {
		return errors.New("nemau: max_surge must be between 1 and 10")
	}
	if r.SecurityGroups != nil {
		if err := checkSecurityGroups(*r.SecurityGroups); err != nil {
			return err
		}
	}
	if r.LaunchTemplate != nil {
		if err := r.LaunchTemplate.check(false); err != nil {
			return err
		}
	}
	var pools []GroupPool
	if r.LoadBalancerPools != nil {
		pools = *r.LoadBalancerPools
	}
	var policies []ScalingPolicy
	if r.ScalingPolicies != nil {
		policies = *r.ScalingPolicies
	}
	if err := checkScaling(pools, r.HealthCheck, deref(r.HealthCheckGraceSeconds), deref(r.DrainSeconds), policies); err != nil {
		return err
	}
	if r.Schedules != nil {
		return checkSchedules(*r.Schedules)
	}
	return nil
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
