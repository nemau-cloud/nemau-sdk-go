package nemau

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// DatabasesService covers /databases: managed PostgreSQL. A database is created
// in a project subnet, and reached by its clients over TLS with the CA that
// [DatabasesService.CACertificate] returns.
//
// Create, RotatePassword and Restore issue an administrator password. The
// platform returns it once, in the 202 of that request, and never again; see
// [DatabaseCredentials].
type DatabasesService struct{ client *Client }

// Limits of a managed database. They mirror the platform's rules, so a request
// that breaks one is refused before it leaves the process. The platform checks
// the same rules and its answer wins.
const (
	DatabaseStorageMinGB       = 10
	DatabaseStorageMaxGB       = 2000
	DatabaseRetentionMinDays   = 1
	DatabaseRetentionMaxDays   = 35
	DatabaseMaxSecurityGroups  = 5
	DatabaseMaxDescriptionSize = 256
)

// ApplyAt values of [ResizeDatabaseRequest].
const (
	// ApplyNow applies a resize at once. It is the platform's default. A
	// resize that stops the database is refused unless it is applied now.
	ApplyNow = "now"
	// ApplyMaintenanceWindow defers a resize to the database's next
	// maintenance window. The platform does not use it by default.
	ApplyMaintenanceWindow = "maintenance_window"
)

var (
	databaseNamePattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	databaseAdminUserPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
)

// DatabaseMaintenanceWindow is the one UTC hour a week in which the platform may apply
// minor updates and other maintenance.
type DatabaseMaintenanceWindow struct {
	// Weekday is 0 for Sunday through 6 for Saturday, in UTC.
	Weekday int `json:"weekday"`
	// Hour is 0 to 23, in UTC.
	Hour int `json:"hour"`
}

// PendingResize is a resize accepted on a database and not yet applied. It is
// set on [Database.PendingResize] until the platform applies it.
type PendingResize struct {
	// Flavor is the flavor the database moves to, empty for a storage-only resize.
	Flavor string `json:"flavor"`
	// StorageGB is the disk size the database moves to.
	StorageGB int `json:"storage_gb"`
	// ApplyAt is when the platform applies the resize.
	ApplyAt time.Time `json:"apply_at"`
}

// Database is a managed PostgreSQL database as the API reports it. The
// platform's internal identifiers never appear here.
type Database struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Status is building, active, error or deleted.
	Status string `json:"status"`
	// PendingResize is nil when no resize is waiting to be applied.
	PendingResize *PendingResize `json:"pending_resize"`
	// EarliestRestorableTime and LatestRestorableTime bound the points in time
	// a [RestoreDatabaseRequest] may target. They are nil before the first
	// backup. RestorableUntil is when the backups expire.
	EarliestRestorableTime *time.Time                 `json:"earliest_restorable_time,omitempty"`
	LatestRestorableTime   *time.Time                 `json:"latest_restorable_time,omitempty"`
	RestorableUntil        *time.Time                 `json:"restorable_until,omitempty"`
	Version                string                     `json:"version"`
	Flavor                 string                     `json:"flavor"`
	StorageGB              int                        `json:"storage_gb"`
	SubnetID               string                     `json:"subnet_id"`
	SecurityGroupIDs       []string                   `json:"security_group_ids"`
	AdminUsername          string                     `json:"admin_username"`
	Address                string                     `json:"address,omitempty"`
	Port                   int                        `json:"port"`
	BackupRetentionDays    int                        `json:"backup_retention_days"`
	MaintenanceWindow      *DatabaseMaintenanceWindow `json:"maintenance_window,omitempty"`
	CreatedAt              time.Time                  `json:"created_at"`
	UpdatedAt              time.Time                  `json:"updated_at"`
}

// redactedSecret is what a [DatabaseCredentials] prints in place of its password.
const redactedSecret = "[REDACTED]"

// DatabaseCredentials is the answer of a write that issues the administrator
// password of a database: [DatabasesService.Create], RotatePassword and Restore.
//
// Password is shown once. The platform keeps only a verifier of it, so a lost
// password is replaced with [DatabasesService.RotatePassword], never recovered.
// The type prints the password as [REDACTED] through String, GoString and
// MarshalJSON, so a log line or a debug dump cannot carry it; read the field
// deliberately.
type DatabaseCredentials struct {
	// TaskID is the background task that creates or changes the database. Wait
	// for it with [Client.WaitForTask] before the database accepts connections.
	TaskID string `json:"task_id"`
	// DatabaseID is set by RotatePassword. Create and Restore leave it empty:
	// the new database's id is known once its task has finished.
	DatabaseID string `json:"database_id,omitempty"`
	// Username is the administrator role the password belongs to.
	Username string `json:"username"`
	// Password is the administrator password, shown once.
	Password string `json:"password"`
}

// String returns the credentials with the password redacted.
func (c DatabaseCredentials) String() string {
	return fmt.Sprintf("nemau.DatabaseCredentials{task_id:%s database_id:%s username:%s password:%s}",
		c.TaskID, c.DatabaseID, c.Username, redactedSecret)
}

// GoString is String, for %#v.
func (c DatabaseCredentials) GoString() string { return c.String() }

// MarshalJSON encodes the credentials with the password redacted.
func (c DatabaseCredentials) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TaskID     string `json:"task_id"`
		DatabaseID string `json:"database_id,omitempty"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}{c.TaskID, c.DatabaseID, c.Username, redactedSecret})
}

// CreateDatabaseRequest describes a new managed database.
//
// Version and Flavor are taken from the region's offer; the platform refuses
// a value the offer does not list. AdminUsername is the administrator role
// the password is issued for. It cannot be postgres, and cannot start with
// pg_ or nemau.
type CreateDatabaseRequest struct {
	Name                string                     `json:"name"`
	Description         string                     `json:"description,omitempty"`
	Version             string                     `json:"version"`
	Flavor              string                     `json:"flavor"`
	StorageGB           int                        `json:"storage_gb"`
	SubnetID            string                     `json:"subnet_id"`
	SecurityGroupIDs    []string                   `json:"security_group_ids,omitempty"`
	AdminUsername       string                     `json:"admin_username"`
	BackupRetentionDays *int                       `json:"backup_retention_days,omitempty"`
	MaintenanceWindow   *DatabaseMaintenanceWindow `json:"maintenance_window,omitempty"`
}

// UpdateDatabaseRequest changes the fields a database allows to change in
// place. A nil field is left as it is. Raising BackupRetentionDays is billed.
//
// The maintenance window has two ways to be sent. MaintenanceWindow sets it.
// ClearMaintenanceWindow sends an explicit JSON null, which the platform reads
// as "remove the window"; a nil MaintenanceWindow alone would leave it as is.
// The two cannot be set together.
type UpdateDatabaseRequest struct {
	Name                   *string
	Description            *string
	BackupRetentionDays    *int
	MaintenanceWindow      *DatabaseMaintenanceWindow
	ClearMaintenanceWindow bool
}

// MarshalJSON encodes the set fields. It is the only encoder of the request:
// it is what lets ClearMaintenanceWindow put an explicit null on the wire.
func (r UpdateDatabaseRequest) MarshalJSON() ([]byte, error) {
	type wire struct {
		Name                *string         `json:"name,omitempty"`
		Description         *string         `json:"description,omitempty"`
		BackupRetentionDays *int            `json:"backup_retention_days,omitempty"`
		MaintenanceWindow   json.RawMessage `json:"maintenance_window,omitempty"`
	}
	w := wire{Name: r.Name, Description: r.Description, BackupRetentionDays: r.BackupRetentionDays}
	switch {
	case r.ClearMaintenanceWindow:
		w.MaintenanceWindow = json.RawMessage("null")
	case r.MaintenanceWindow != nil:
		b, err := json.Marshal(r.MaintenanceWindow)
		if err != nil {
			return nil, err
		}
		w.MaintenanceWindow = b
	}
	return json.Marshal(w)
}

// DatabaseFlavor is one size of the region's offer, as [DatabasesService.Flavors] lists it.
type DatabaseFlavor struct {
	Name     string `json:"name"`
	VCPUs    int    `json:"vcpus"`
	MemoryMB int    `json:"memory_mb"`
}

// ResizeDatabaseRequest grows a database. Flavor moves up the offer's catalogue;
// StorageGB only grows. At least one of the two is required. A nil StorageGB
// leaves the disk as it is.
type ResizeDatabaseRequest struct {
	Flavor    string `json:"flavor,omitempty"`
	StorageGB *int   `json:"storage_gb,omitempty"`
	// ApplyAt is [ApplyNow] or [ApplyMaintenanceWindow]. Empty means the
	// platform's default, [ApplyNow]: the resize starts at once.
	ApplyAt string `json:"apply_at,omitempty"`
}

// RestoreDatabaseRequest restores a database to a point in time, into a new
// database. The source database is not changed. TargetTime must be in the
// past and inside the source's restorable window.
type RestoreDatabaseRequest struct {
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	TargetTime       time.Time `json:"-"`
	Flavor           string    `json:"flavor"`
	StorageGB        int       `json:"storage_gb"`
	SubnetID         string    `json:"subnet_id"`
	SecurityGroupIDs []string  `json:"security_group_ids,omitempty"`
	AdminUsername    string    `json:"admin_username"`
}

// restoreWire is the body of a restore: the target time goes on the wire as an
// RFC 3339 string in UTC, which is what the platform parses.
type restoreWire struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	TargetTime       string   `json:"target_time"`
	Flavor           string   `json:"flavor"`
	StorageGB        int      `json:"storage_gb"`
	SubnetID         string   `json:"subnet_id"`
	SecurityGroupIDs []string `json:"security_group_ids,omitempty"`
	AdminUsername    string   `json:"admin_username"`
}

// List returns the managed databases of the credential's project.
func (s *DatabasesService) List(ctx context.Context) ([]Database, error) {
	var out struct {
		Databases []Database `json:"databases"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/databases"}, &out)
	return out.Databases, err
}

// Get returns one database. It answers a 404 [APIError] when the id is unknown
// to the project.
func (s *DatabasesService) Get(ctx context.Context, id string) (*Database, error) {
	if err := checkID("database id", id); err != nil {
		return nil, err
	}
	var out Database
	if err := s.client.do(ctx, request{method: http.MethodGet, path: "/databases/" + url.PathEscape(id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CACertificate returns the PEM certificate of the region's database CA. A
// client passes it to psql or libpq as sslrootcert, with sslmode=verify-full.
func (s *DatabasesService) CACertificate(ctx context.Context) (string, error) {
	var out struct {
		CACertificatePEM string `json:"ca_certificate_pem"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/databases/ca-certificate"}, &out)
	return out.CACertificatePEM, err
}

// Create queues a database creation and returns the one-time administrator
// password with it. Store the password where the application can read it; the
// platform cannot show it again.
func (s *DatabasesService) Create(ctx context.Context, req CreateDatabaseRequest) (*DatabaseCredentials, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	return s.issue(ctx, http.MethodPost, "/databases", req)
}

// Update queues a change of name, description, backup retention or maintenance
// window.
func (s *DatabasesService) Update(ctx context.Context, id string, req UpdateDatabaseRequest) (*TaskRef, error) {
	if err := checkID("database id", id); err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	return s.client.submit(ctx, http.MethodPatch, "/databases/"+url.PathEscape(id), nil, req)
}

// Resize queues a growth of the database's flavor, its storage, or both.
func (s *DatabasesService) Resize(ctx context.Context, id string, req ResizeDatabaseRequest) (*TaskRef, error) {
	if err := checkID("database id", id); err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	return s.client.submit(ctx, http.MethodPost, "/databases/"+url.PathEscape(id)+"/resize", nil, req)
}

// Restart queues a restart of the database and returns its task.
func (s *DatabasesService) Restart(ctx context.Context, id string) (*TaskRef, error) {
	if err := checkID("database id", id); err != nil {
		return nil, err
	}
	return s.client.submit(ctx, http.MethodPost, "/databases/"+url.PathEscape(id)+"/restart", nil, nil)
}

// Flavors returns the database flavors the region offers, in the offer's order.
// Create and Resize take one of their names.
func (s *DatabasesService) Flavors(ctx context.Context) ([]DatabaseFlavor, error) {
	var out struct {
		Flavors []DatabaseFlavor `json:"flavors"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/database-flavors"}, &out)
	return out.Flavors, err
}

// Versions returns the PostgreSQL versions the region offers. Create takes one of them.
func (s *DatabasesService) Versions(ctx context.Context) ([]string, error) {
	var out struct {
		Versions []string `json:"versions"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/database-versions"}, &out)
	return out.Versions, err
}

// RotatePassword replaces the administrator password. The previous password
// stops working once the task has run. The new one is returned once, here.
func (s *DatabasesService) RotatePassword(ctx context.Context, id string) (*DatabaseCredentials, error) {
	if err := checkID("database id", id); err != nil {
		return nil, err
	}
	return s.issue(ctx, http.MethodPost, "/databases/"+url.PathEscape(id)+"/rotate-password", nil)
}

// Restore queues a point-in-time restore of the database sourceID into a new
// database, and returns that database's administrator password once. The
// source is left as it is.
func (s *DatabasesService) Restore(ctx context.Context, sourceID string, req RestoreDatabaseRequest) (*DatabaseCredentials, error) {
	if err := checkID("source database id", sourceID); err != nil {
		return nil, err
	}
	if err := req.validate(time.Now()); err != nil {
		return nil, err
	}
	wire := restoreWire{
		Name: req.Name, Description: req.Description,
		TargetTime: req.TargetTime.UTC().Format(time.RFC3339),
		Flavor:     req.Flavor, StorageGB: req.StorageGB, SubnetID: req.SubnetID,
		SecurityGroupIDs: req.SecurityGroupIDs, AdminUsername: req.AdminUsername,
	}
	return s.issue(ctx, http.MethodPost, "/databases/"+url.PathEscape(sourceID)+"/restore", wire)
}

// Delete queues a deletion. Backups survive the database for its retention
// period unless purgeBackups is true, which erases them at once.
func (s *DatabasesService) Delete(ctx context.Context, id string, purgeBackups bool) (*TaskRef, error) {
	if err := checkID("database id", id); err != nil {
		return nil, err
	}
	var query url.Values
	if purgeBackups {
		query = url.Values{"purge_backups": {"true"}}
	}
	return s.client.submit(ctx, http.MethodDelete, "/databases/"+url.PathEscape(id), query, nil)
}

// issue performs a write whose 202 carries a one-time password.
func (s *DatabasesService) issue(ctx context.Context, method, path string, body any) (*DatabaseCredentials, error) {
	var out DatabaseCredentials
	if err := s.client.submitIssuingSecret(ctx, method, path, body, &out); err != nil {
		return nil, err
	}
	if out.TaskID == "" || out.Password == "" {
		// Reports only the shape of the failure: the body holds a secret.
		return nil, fmt.Errorf("nemau: %s %s: response carried no task id or password", method, path)
	}
	return &out, nil
}

// checkID refuses an empty id before a request is built.
func checkID(what, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("nemau: %s is required", what)
	}
	return nil
}

// checkDatabaseName applies the database name rule.
func checkDatabaseName(name string) error {
	if !databaseNamePattern.MatchString(name) {
		return errors.New("nemau: database name must match ^[a-z][a-z0-9-]{0,62}$")
	}
	return nil
}

// checkDatabaseDescription applies the description rule: at most 256 bytes and
// no control characters.
func checkDatabaseDescription(description string) error {
	if len(description) > DatabaseMaxDescriptionSize {
		return fmt.Errorf("nemau: database description must not exceed %d bytes", DatabaseMaxDescriptionSize)
	}
	for _, r := range description {
		if unicode.IsControl(r) {
			return errors.New("nemau: database description must not contain control characters")
		}
	}
	return nil
}

func checkDatabaseStorage(gb int) error {
	if gb < DatabaseStorageMinGB || gb > DatabaseStorageMaxGB {
		return fmt.Errorf("nemau: database storage must be between %d and %d GB", DatabaseStorageMinGB, DatabaseStorageMaxGB)
	}
	return nil
}

func checkRetentionDays(days int) error {
	if days < DatabaseRetentionMinDays || days > DatabaseRetentionMaxDays {
		return fmt.Errorf("nemau: backup retention must be between %d and %d days", DatabaseRetentionMinDays, DatabaseRetentionMaxDays)
	}
	return nil
}

func checkMaintenanceWindow(w DatabaseMaintenanceWindow) error {
	if w.Weekday < 0 || w.Weekday > 6 || w.Hour < 0 || w.Hour > 23 {
		return errors.New("nemau: maintenance window needs weekday 0 to 6 (Sunday is 0) and hour 0 to 23, in UTC")
	}
	return nil
}

func checkAdminUsername(name string) error {
	if !databaseAdminUserPattern.MatchString(name) {
		return errors.New("nemau: admin username must match ^[a-z][a-z0-9_]{2,31}$")
	}
	if name == "postgres" || strings.HasPrefix(name, "pg_") || strings.HasPrefix(name, "nemau") {
		return errors.New("nemau: admin username is reserved")
	}
	return nil
}

func checkDatabaseSecurityGroups(ids []string) error {
	if len(ids) > DatabaseMaxSecurityGroups {
		return fmt.Errorf("nemau: a database accepts at most %d security groups", DatabaseMaxSecurityGroups)
	}
	for _, id := range ids {
		if !uuidPattern.MatchString(id) {
			return errors.New("nemau: security group ids must be UUIDs")
		}
	}
	return nil
}

// validateDatabaseTarget applies the rules a new database shares with a
// restore: its name, description, sizing, network and administrator.
func validateDatabaseTarget(name, description, flavor string, storageGB int, subnetID string, securityGroups []string, admin string) error {
	if err := checkDatabaseName(name); err != nil {
		return err
	}
	if err := checkDatabaseDescription(description); err != nil {
		return err
	}
	if flavor == "" {
		return errors.New("nemau: database flavor is required")
	}
	if err := checkDatabaseStorage(storageGB); err != nil {
		return err
	}
	if err := checkUUID("subnet id", subnetID); err != nil {
		return err
	}
	if err := checkDatabaseSecurityGroups(securityGroups); err != nil {
		return err
	}
	return checkAdminUsername(admin)
}

func checkUUID(what, id string) error {
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("nemau: %s must be a UUID", what)
	}
	return nil
}

// validate applies the create rules. Version and flavor are only required
// here; the offer's list is the platform's to judge.
func (r CreateDatabaseRequest) validate() error {
	if r.Version == "" {
		return errors.New("nemau: database version is required")
	}
	if err := validateDatabaseTarget(r.Name, r.Description, r.Flavor, r.StorageGB, r.SubnetID, r.SecurityGroupIDs, r.AdminUsername); err != nil {
		return err
	}
	if r.BackupRetentionDays != nil {
		if err := checkRetentionDays(*r.BackupRetentionDays); err != nil {
			return err
		}
	}
	if r.MaintenanceWindow != nil {
		return checkMaintenanceWindow(*r.MaintenanceWindow)
	}
	return nil
}

func (r UpdateDatabaseRequest) validate() error {
	if r.Name == nil && r.Description == nil && r.BackupRetentionDays == nil && r.MaintenanceWindow == nil && !r.ClearMaintenanceWindow {
		return errors.New("nemau: the update names no field")
	}
	if r.ClearMaintenanceWindow && r.MaintenanceWindow != nil {
		return errors.New("nemau: set or clear the maintenance window, not both")
	}
	if r.Name != nil {
		if err := checkDatabaseName(*r.Name); err != nil {
			return err
		}
	}
	if r.Description != nil {
		if err := checkDatabaseDescription(*r.Description); err != nil {
			return err
		}
	}
	if r.BackupRetentionDays != nil {
		if err := checkRetentionDays(*r.BackupRetentionDays); err != nil {
			return err
		}
	}
	if r.MaintenanceWindow != nil {
		return checkMaintenanceWindow(*r.MaintenanceWindow)
	}
	return nil
}

func (r ResizeDatabaseRequest) validate() error {
	if r.Flavor == "" && r.StorageGB == nil {
		return errors.New("nemau: resize needs a flavor, a storage size, or both")
	}
	if r.StorageGB != nil {
		if err := checkDatabaseStorage(*r.StorageGB); err != nil {
			return err
		}
	}
	if !slices.Contains([]string{"", ApplyNow, ApplyMaintenanceWindow}, r.ApplyAt) {
		return errors.New("nemau: apply_at must be now or maintenance_window")
	}
	return nil
}

// validate applies the restore rules; now is passed in so that a test can pin
// the clock.
func (r RestoreDatabaseRequest) validate(now time.Time) error {
	if r.TargetTime.IsZero() || r.TargetTime.After(now) {
		return errors.New("nemau: restore target time must be set and not in the future")
	}
	return validateDatabaseTarget(r.Name, r.Description, r.Flavor, r.StorageGB, r.SubnetID, r.SecurityGroupIDs, r.AdminUsername)
}
