package nemau

import "time"

// The structs below mirror the JSON the API returns, and their tags follow the
// server exactly: infrastructure resources use snake_case, while tasks and
// Kubernetes clusters use camelCase.

// Ref is the {id, name} envelope used for an instance's image, flavor and
// security groups.
type Ref struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Sizing is a node's CPU/RAM/disk shape.
type Sizing struct {
	VCPUs  int `json:"vcpus"`
	RAMMB  int `json:"ram_mb"`
	DiskGB int `json:"disk_gb"`
}

// Instance is a compute instance.
//
// Fields the platform cannot report are omitted instead of zeroed, so an empty
// Addresses means the addresses are unknown. Created is a pointer for the same
// reason: a zero time.Time would read as year 1.
type Instance struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Status         string   `json:"status"` // running | stopped | paused | pending
	TenantID       string   `json:"tenant_id"`
	ProjectID      string   `json:"project_id,omitempty"`
	UserID         string   `json:"user_id,omitempty"`
	Flavor         *Ref     `json:"flavor,omitempty"`
	Sizing         Sizing   `json:"sizing"`
	Image          *Ref     `json:"image,omitempty"`
	KeyName        string   `json:"key_name,omitempty"`
	Addresses      []string `json:"addresses,omitempty"`
	SecurityGroups []Ref    `json:"security_groups,omitempty"`
	// InstanceGroupID is the group the instance is a member of, empty for an
	// instance created on its own.
	InstanceGroupID string     `json:"instance_group_id,omitempty"`
	Created         *time.Time `json:"created,omitempty"`

	// BackupSchedule is the instance's scheduled-backup policy: BackupNone (the
	// default), BackupDaily or BackupWeekly. It is always reported.
	BackupSchedule string `json:"backup_schedule"`
	// BackupHour is the UTC hour, 0 to 23, at which a scheduled backup starts.
	// It is nil when the platform reports no policy.
	BackupHour *int `json:"backup_hour,omitempty"`
	// BackupWeekday is 0 (Sunday) to 6 (Saturday). It only applies to
	// BackupWeekly.
	BackupWeekday *int `json:"backup_weekday,omitempty"`
	// BackupRetentionDays is how long, in days, each scheduled snapshot is kept.
	BackupRetentionDays *int `json:"backup_retention_days,omitempty"`
	// UserDataSHA256 is the hex SHA-256 of the cloud-init document given at
	// creation. Empty when the instance was created without one. The document
	// itself is never returned.
	UserDataSHA256 string `json:"user_data_sha256,omitempty"`
}

// Values of Instance.BackupSchedule and BackupSchedule in an Update.
const (
	BackupNone   = "none"
	BackupDaily  = "daily"
	BackupWeekly = "weekly"
)

// Values of Snapshot.Origin.
const (
	SnapshotOriginManual    = "manual"
	SnapshotOriginScheduled = "scheduled"
)

// Snapshot is a crash-consistent copy of an instance's system disk.
//
// Storage says where it lives. "object_store" snapshots are compressed archives
// on the platform's object store: they are billed on SizeBytes and can be
// restored. "hypervisor" snapshots are older ones: they can be listed and
// deleted, are not billed, and cannot be restored. Block volumes attached to
// the instance are never part of a snapshot.
//
// SizeBytes, MonthlyCost and Currency are set once an object-store snapshot is
// active; they are nil while it is being created and on hypervisor snapshots.
type Snapshot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`  // creating | active | restoring
	Storage    string `json:"storage"` // object_store | hypervisor
	// SizeBytes is the size of the compressed archive in the object store, which
	// is what is billed.
	SizeBytes *int64 `json:"size_bytes,omitempty"`
	// SourceDiskGB is the capacity of the system disk that was captured.
	SourceDiskGB *int `json:"source_disk_gb,omitempty"`
	// MonthlyCost is the price, excluding VAT, of keeping SizeBytes stored for
	// a 730-hour month, in Currency.
	MonthlyCost *float64   `json:"monthly_cost,omitempty"`
	Currency    string     `json:"currency,omitempty"`
	Restorable  bool       `json:"restorable"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`

	// Origin is SnapshotOriginManual for a snapshot taken by hand, which never
	// expires, or SnapshotOriginScheduled for one taken by a backup policy.
	Origin string `json:"origin"`
	// ExpiresAt is when the platform deletes a scheduled snapshot. It is nil for
	// a manual snapshot.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Image is a bootable template.
type Image struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OSName     string `json:"os_name"`
	Status     string `json:"status"`
	DiskFormat string `json:"disk_format"`
}

// Flavor is an organization-defined instance shape. Flavors require the
// compute-flavors organization feature; without it the endpoints answer 403.
type Flavor struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Name         string    `json:"name"`
	VCPUs        int       `json:"vcpus"`
	RAMMB        int       `json:"ram_mb"`
	DiskGB       int       `json:"disk_gb"`
	ServiceLevel string    `json:"service_level"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"CreatedAt"`
	UpdatedAt    time.Time `json:"UpdatedAt"`
}

// Keypair is an SSH public key usable as an instance's key_name.
type Keypair struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Type        string `json:"type,omitempty"`
}

// Network is a virtual network.
type Network struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	TenantID    string `json:"tenant_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	External    bool   `json:"external,omitempty"`
}

// AllocationPool bounds the addresses a subnet hands out.
type AllocationPool struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// IP versions, for [Subnet.IPVersion], [FloatingIP.IPVersion] and the
// requests that choose one.
const (
	IPv4 = 4
	IPv6 = 6
)

// Subnet is an IP range inside a network.
//
// An IPv6 subnet is a /64 the platform assigns from the region's routed prefix:
// its CIDR and gateway come back here, and it has no allocation pools.
type Subnet struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	NetworkID         string           `json:"network_id"`
	CIDR              string           `json:"cidr"`
	IPVersion         int              `json:"ip_version,omitempty"` // 4 or 6; 0 (from an older region) means 4
	GatewayIP         string           `json:"gateway_ip"`
	AddressAssignment string           `json:"address_assignment"`
	DNSServers        []string         `json:"dns_servers"`
	AllocationPools   []AllocationPool `json:"allocation_pools"`
}

// InterfaceAddress is one address carried by a network interface.
type InterfaceAddress struct {
	SubnetID string `json:"subnet_id"`
	Address  string `json:"address"`
}

// NetworkInterface attaches an instance to a network.
type NetworkInterface struct {
	ID           string             `json:"id"`
	Slot         string             `json:"slot"`
	NetworkID    string             `json:"network_id"`
	InstanceID   string             `json:"instance_id"`
	InstanceName string             `json:"instance_name"`
	MACAddress   string             `json:"mac_address,omitempty"`
	Status       string             `json:"status"`
	Addresses    []InterfaceAddress `json:"addresses"`
}

// FloatingIP is a public address that can front an instance or a load balancer.
//
// An IPv6 floating IP is translated to the target's private IPv4 address on the
// region's exit node: the guest keeps an IPv4-only configuration and sees IPv6
// clients as addresses of the translation range. A target holds at most one.
type FloatingIP struct {
	ID                string `json:"id"`
	Address           string `json:"address"`
	IPVersion         int    `json:"ip_version,omitempty"` // 4 or 6; 0 (from an older region) means 4
	Description       string `json:"description,omitempty"`
	ExternalNetworkID string `json:"external_network_id,omitempty"`
	PrivateAddress    string `json:"private_address,omitempty"`
	TargetType        string `json:"target_type,omitempty"` // instance | load_balancer
	TargetID          string `json:"target_id,omitempty"`
	Status            string `json:"status"`
}

// NetworkCapabilities reports which optional network features the region
// offers. Both are off in a region that has no IPv6 prefix.
type NetworkCapabilities struct {
	IPv6FloatingIPs bool `json:"ipv6_floating_ips"`
	IPv6Subnets     bool `json:"ipv6_subnets"`
}

// SecurityGroup is a stateful filter applied to instances.
type SecurityGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	TenantID    string `json:"tenant_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	// EgressFiltering is read-only. True for a group whose egress rules are
	// enforced: all new groups, and older groups once converted with
	// NetworkService.EnableSecurityGroupEgressFiltering. False for a group
	// created before egress filtering existed: it lets all outbound traffic
	// through and its egress rules have no effect.
	EgressFiltering bool                `json:"egress_filtering"`
	Rules           []SecurityGroupRule `json:"rules"`
}

// SecurityGroupRule is one filter entry.
type SecurityGroupRule struct {
	ID              string `json:"id"`
	SecurityGroupID string `json:"security_group_id"`
	Direction       string `json:"direction"`
	Action          string `json:"action"`
	Protocol        string `json:"protocol,omitempty"`
	PortRangeMin    int    `json:"port_range_min,omitempty"`
	PortRangeMax    int    `json:"port_range_max,omitempty"`
	RemoteIPPrefix  string `json:"remote_ip_prefix,omitempty"`
}

// VolumeAttachment records a volume's presence on an instance.
type VolumeAttachment struct {
	ID         string    `json:"id"`
	ServerID   string    `json:"server_id"`
	ServerName string    `json:"server_name,omitempty"`
	Device     string    `json:"device"`
	AttachedAt time.Time `json:"attached_at"`
}

// Volume is a block device. Size is in GiB.
type Volume struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Size        int                `json:"size"`
	Status      string             `json:"status"`
	TenantID    string             `json:"tenant_id,omitempty"`
	ProjectID   string             `json:"project_id,omitempty"`
	Attachments []VolumeAttachment `json:"attachments"`
	CreatedAt   *time.Time         `json:"created_at,omitempty"`
	UpdatedAt   *time.Time         `json:"updated_at,omitempty"`
}

// Container is an object-storage bucket.
type Container struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Count int64  `json:"count"`
	// S3Bucket is the bucket name an S3 tool addresses with one of this
	// container's keys. It differs from Name: the object store gives each
	// container an opaque bucket name.
	S3Bucket string `json:"s3_bucket"`
	// Protection is what was requested at creation, nil for an ordinary
	// container. For what the store enforces, read
	// [StorageService.ContainerProtection].
	Protection *ContainerProtection `json:"protection,omitempty"`
}

// ContainerProtection is the versioning and object-lock state of a container.
type ContainerProtection struct {
	// Versioning is "Enabled", "Suspended", or empty.
	Versioning    string `json:"versioning"`
	ObjectLock    bool   `json:"object_lock"`
	RetentionMode string `json:"retention_mode,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

// Immutable reports whether WORM is enforced, which requires both versioning
// and object lock. Without object lock, a delete still destroys a version.
func (p *ContainerProtection) Immutable() bool {
	return p != nil && p.ObjectLock && p.Versioning == "Enabled"
}

// Object is one stored object's metadata.
type Object struct {
	Name         string    `json:"name"`
	Bytes        int64     `json:"bytes"`
	ContentType  string    `json:"content_type"`
	LastModified time.Time `json:"last_modified"`
}

// LoadBalancer fronts a pool of backends.
type LoadBalancer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	VipAddress  string `json:"vip_address,omitempty"`
	VipSubnetID string `json:"vip_subnet_id,omitempty"`
	// Provider is the engine the balancer runs, read-only. New balancers are
	// always "auto", which serves every listener protocol; older ones may
	// report "haproxy" (no UDP listeners) or "nginx" (UDP listeners only).
	Provider           string     `json:"provider"`
	ProvisioningStatus string     `json:"provisioning_status"`
	OperatingStatus    string     `json:"operating_status,omitempty"`
	TenantID           string     `json:"tenant_id,omitempty"`
	ProjectID          string     `json:"project_id,omitempty"`
	AdminStateUp       bool       `json:"admin_state_up"`
	Listeners          []Listener `json:"listeners"`
	Pools              []Pool     `json:"pools"`
	CreatedAt          *time.Time `json:"created_at,omitempty"`
	// Flavor is the tier: lb-small, lb-ha, lb-medium or lb-large. It is fixed
	// at creation.
	Flavor string `json:"flavor,omitempty"`
	// HighAvailability is the live state of the active/standby pair of a
	// high-availability tier; nil on a single-appliance tier.
	HighAvailability *LoadBalancerHA `json:"high_availability,omitempty"`
}

// LoadBalancerHA is the observed state of a high-availability load balancer's
// two appliances.
//
// Status is REDUNDANT (one active, one standby, on two hosts), DEGRADED or
// UNKNOWN. Reason says why a pair is DEGRADED: SPLIT_BRAIN,
// NO_ACTIVE_APPLIANCE, APPLIANCE_UNAVAILABLE or SINGLE_HOST (both appliances
// share one host, so the pair would not survive losing it).
type LoadBalancerHA struct {
	Status     string                  `json:"status"`
	Reason     string                  `json:"reason,omitempty"`
	Appliances []LoadBalancerAppliance `json:"appliances"`
}

// LoadBalancerAppliance is one appliance of a high-availability pair.
//
// State is ACTIVE (holds the VIP), STANDBY (ready to take over), DOWN or
// UNKNOWN. Address is the appliance's own address on the VIP subnet: health
// checks and proxied connections leave from it, so members' security groups
// must admit both appliances.
type LoadBalancerAppliance struct {
	Index   int    `json:"index"`
	State   string `json:"state"`
	Address string `json:"address,omitempty"`
}

// LoadBalancerFlavor is one load-balancer tier. VCPUs and RAMMB size each
// appliance; a high-availability tier runs Appliances of them and is billed as
// one load balancer.
type LoadBalancerFlavor struct {
	Name             string  `json:"name"`
	Appliances       int     `json:"appliances"`
	VCPUs            int     `json:"vcpus"`
	RAMMB            int     `json:"ram_mb"`
	HighAvailability bool    `json:"high_availability"`
	HourlyPrice      float64 `json:"hourly_price"`
	MonthlyPrice     float64 `json:"monthly_price"`
	Currency         string  `json:"currency"`
}

// Listener is a load balancer's front-end port.
type Listener struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	LoadBalancerID  string `json:"loadbalancer_id,omitempty"`
	Protocol        string `json:"protocol"`
	ProtocolPort    int    `json:"protocol_port"`
	DefaultPoolID   string `json:"default_pool_id,omitempty"`
	ConnectionLimit int    `json:"connection_limit"`
	// TLSCertificateRef is the ID of the [Certificate] a TERMINATED_HTTPS
	// listener serves.
	TLSCertificateRef  string `json:"tls_certificate_ref,omitempty"`
	AdminStateUp       bool   `json:"admin_state_up"`
	ProvisioningStatus string `json:"provisioning_status"`
	OperatingStatus    string `json:"operating_status"`
}

// SessionPersistence pins a client to one backend.
type SessionPersistence struct {
	Type       string `json:"type"`
	CookieName string `json:"cookie_name,omitempty"`
}

// Pool is a load balancer's back-end group.
type Pool struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Description        string             `json:"description,omitempty"`
	LoadBalancerID     string             `json:"loadbalancer_id,omitempty"`
	ListenerID         string             `json:"listener_id,omitempty"`
	Protocol           string             `json:"protocol"`
	LBAlgorithm        string             `json:"lb_algorithm"`
	SessionPersistence SessionPersistence `json:"session_persistence,omitempty"`
	AdminStateUp       bool               `json:"admin_state_up"`
	ProvisioningStatus string             `json:"provisioning_status"`
	OperatingStatus    string             `json:"operating_status"`
	Members            []PoolMember       `json:"members"`
	HealthMonitors     []HealthMonitor    `json:"health_monitors"`
}

// PoolMember is one backend in a pool.
type PoolMember struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	PoolID             string `json:"pool_id,omitempty"`
	Address            string `json:"address"`
	SubnetID           string `json:"subnet_id,omitempty"`
	ProtocolPort       int    `json:"protocol_port"`
	Weight             int    `json:"weight"`
	AdminStateUp       bool   `json:"admin_state_up"`
	ProvisioningStatus string `json:"provisioning_status"`
	OperatingStatus    string `json:"operating_status"`
}

// HealthMonitor probes a pool's members.
//
// UDP pools report UDP_CONNECT: failures on them are detected passively, without
// an active probe.
type HealthMonitor struct {
	ID                 string `json:"id"`
	PoolID             string `json:"pool_id"`
	Type               string `json:"type"`
	Delay              int    `json:"delay"`
	Timeout            int    `json:"timeout"`
	MaxRetries         int    `json:"max_retries"`
	HTTPMethod         string `json:"http_method,omitempty"`
	URLPath            string `json:"url_path,omitempty"`
	ExpectedCodes      string `json:"expected_codes,omitempty"`
	AdminStateUp       bool   `json:"admin_state_up"`
	ProvisioningStatus string `json:"provisioning_status"`
}

// DNSZone is an authoritative zone. ID is the platform's identifier (a UUID)
// and is what every zone method takes; Name is the canonical zone name
// ("example.com."), which is not an identifier.
//
// A new zone is not served until its owner proves control of the domain:
// Status is [DNSZoneStatusPendingVerification] and Verification names the TXT
// record to publish at the current DNS host. Once [DNSService.VerifyZone] has
// found it, Status becomes [DNSZoneStatusActive] and Verification is nil.
type DNSZone struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Email        string               `json:"email,omitempty"`
	Description  string               `json:"description,omitempty"`
	TTL          int                  `json:"ttl,omitempty"`
	Status       string               `json:"status"`
	Verification *DNSZoneVerification `json:"verification,omitempty"`
	TenantID     string               `json:"tenant_id,omitempty"`
	ProjectID    string               `json:"project_id,omitempty"`
	CreatedAt    string               `json:"created_at,omitempty"`
}

// DNSZoneVerification is the record that proves control of a pending zone's
// domain. Name is fully qualified ("_nemau-challenge.example.com."), Type is
// "TXT" and Value is the exact text the record must hold.
type DNSZoneVerification struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// DNSRecordSet groups the records sharing a name and a type.
//
// Its ID is "canonical.name./TYPE". Because it contains a slash, the API takes
// it as a query parameter instead of a path segment.
type DNSRecordSet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	TTL         int      `json:"ttl,omitempty"`
	Records     []string `json:"records,omitempty"`
	Description string   `json:"description,omitempty"`
	Protected   bool     `json:"protected,omitempty"`
	Status      string   `json:"status"`
	ZoneID      string   `json:"zone_id,omitempty"`
}

// KubernetesCluster is a managed cluster's live Cluster API state.
type KubernetesCluster struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	CreatedAt *time.Time              `json:"createdAt,omitempty"`
	Spec      KubernetesClusterSpec   `json:"spec"`
	Status    KubernetesClusterStatus `json:"status"`
}

// KubernetesClusterSpec is the desired topology and the network the cluster
// lives in. NetworkID and SubnetID are the project network and the subnet its
// nodes are attached to; PodCIDR and ServiceCIDR are the address ranges the
// cluster was created with, and are empty when the platform chose the defaults
// and does not report them.
type KubernetesClusterSpec struct {
	Topology    KubernetesTopology `json:"topology"`
	NetworkID   string             `json:"network_id"`
	SubnetID    string             `json:"subnet_id"`
	PodCIDR     string             `json:"pod_cidr"`
	ServiceCIDR string             `json:"service_cidr"`
}

// KubernetesTopology is the cluster's shape.
type KubernetesTopology struct {
	Version              string         `json:"version"`
	Class                string         `json:"class"`
	ControlPlaneFlavorID string         `json:"controlPlaneFlavorId,omitempty"`
	ControlPlaneSizing   Sizing         `json:"controlPlaneSizing,omitempty"`
	ControlPlaneReplicas int32          `json:"controlPlaneReplicas"`
	NodePools            []NodePoolSpec `json:"nodePools"`
}

// NodePoolSpec is one worker group.
type NodePoolSpec struct {
	Name          string   `json:"name"`
	Class         string   `json:"class"`
	FlavorID      string   `json:"flavor_id,omitempty"`
	VCPUs         int      `json:"vcpus"`
	RAMMB         int      `json:"ram_mb"`
	DiskGB        int      `json:"disk_gb"`
	Replicas      int32    `json:"replicas"`
	AutoscalerMin int32    `json:"autoscalerMin,omitempty"`
	AutoscalerMax int32    `json:"autoscalerMax,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	Taints        []string `json:"taints,omitempty"`
}

// KubernetesClusterStatus is the observed state.
type KubernetesClusterStatus struct {
	Phase                string                       `json:"phase"`
	InfrastructureReady  bool                         `json:"infrastructureReady"`
	ControlPlaneReady    bool                         `json:"controlPlaneReady"`
	ControlPlaneEndpoint *KubernetesAPIEndpoint       `json:"controlPlaneEndpoint,omitempty"`
	Conditions           []KubernetesClusterCondition `json:"conditions"`
}

// KubernetesAPIEndpoint is the cluster's API server address.
type KubernetesAPIEndpoint struct {
	Host string `json:"host"`
	Port int32  `json:"port"`
}

// KubernetesClusterCondition is one Cluster API condition.
type KubernetesClusterCondition struct {
	Type               string     `json:"type"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason,omitempty"`
	LastTransitionTime *time.Time `json:"lastTransitionTime,omitempty"`
}

// KubernetesClusterSummary is one row of the cluster listing.
//
// It is the platform's record of the cluster, refreshed periodically, so a
// cluster created a moment ago may not have all its specs yet. Status is the
// lowercase lifecycle status ("active", "building", "upgrading", "deleting",
// "error"); the Cluster API phase, capitalised, is in Specs.Phase.
type KubernetesClusterSummary struct {
	ID        string                        `json:"id"`
	Name      string                        `json:"name"`
	Status    string                        `json:"status"`
	ProjectID string                        `json:"projectId,omitempty"`
	Specs     KubernetesClusterSummarySpecs `json:"specs"`
	CreatedAt *time.Time                    `json:"createdAt,omitempty"`
	UpdatedAt *time.Time                    `json:"updatedAt,omitempty"`
}

// KubernetesClusterSummarySpecs are the figures the listing carries about a
// cluster. Every field is optional; keys the platform adds later are ignored.
type KubernetesClusterSummarySpecs struct {
	Phase                string `json:"phase,omitempty"`
	InfrastructureReady  bool   `json:"infrastructure_ready,omitempty"`
	ControlPlaneReady    bool   `json:"control_plane_ready,omitempty"`
	KubernetesVersion    string `json:"kubernetes_version,omitempty"`
	ClusterClass         string `json:"cluster_class,omitempty"`
	NodePoolsCount       int    `json:"node_pools_count,omitempty"`
	ControlPlaneReplicas int    `json:"control_plane_replicas,omitempty"`
	ControlPlaneVCPUs    int    `json:"control_plane_vcpus,omitempty"`
	ControlPlaneRAMMB    int    `json:"control_plane_ram_mb,omitempty"`
	ControlPlaneDiskGB   int    `json:"control_plane_disk_gb,omitempty"`
	WorkerReplicas       int    `json:"worker_replicas,omitempty"`
	WorkerVCPUs          int    `json:"worker_vcpus,omitempty"`
	WorkerRAMMB          int    `json:"worker_ram_mb,omitempty"`
	WorkerDiskGB         int    `json:"worker_disk_gb,omitempty"`
}

// KubernetesPreflight is the answer to a preflight request: the stored
// report's id and the report itself.
type KubernetesPreflight struct {
	PreflightID string                    `json:"preflight_id"`
	Report      KubernetesPreflightReport `json:"report"`
}

// KubernetesPreflightReport says whether a cluster may be upgraded now.
type KubernetesPreflightReport struct {
	ClusterName    string                     `json:"clusterName"`
	CurrentVersion string                     `json:"currentVersion"`
	TargetVersion  string                     `json:"targetVersion,omitempty"`
	Ready          bool                       `json:"ready"`
	Checks         []KubernetesPreflightCheck `json:"checks"`
	BlockingIssues []string                   `json:"blockingIssues,omitempty"`
}

// KubernetesPreflightCheck is one check of a preflight report.
type KubernetesPreflightCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

// MaintenanceWindow is a period during which a cluster may be upgraded.
type MaintenanceWindow struct {
	ID          string     `json:"id"`
	ClusterName string     `json:"clusterName"`
	Name        string     `json:"name"`
	StartsAt    *time.Time `json:"startsAt,omitempty"`
	EndsAt      *time.Time `json:"endsAt,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
}

// ClusterClass is a deployable cluster template on the management cluster.
type ClusterClass struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// Region is a deployment region.
type Region struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Country     string `json:"country"`
	Enabled     bool   `json:"enabled"`
}

// RegionStatus is a region's health.
type RegionStatus struct {
	Slug      string `json:"slug"`
	Healthy   bool   `json:"healthy"`
	CheckedAt string `json:"checkedAt"`
	Error     string `json:"error,omitempty"`
}
