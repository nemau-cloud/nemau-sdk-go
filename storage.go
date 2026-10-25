package nemau

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// StorageService covers /storage: block volumes and object containers.
//
// Volume operations send the volume id as ?id= instead of a path segment, and
// object operations send the key as ?key=, because a volume id or an object key
// can contain slashes.
type StorageService struct{ client *Client }

// MaxObjectSize is the platform's per-object upload and download cap.
const MaxObjectSize = 100 * 1024 * 1024

// ListVolumes returns the project's block volumes.
func (s *StorageService) ListVolumes(ctx context.Context) ([]Volume, error) {
	var out struct {
		Volumes []Volume `json:"volumes"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/storage/volumes"}, &out)
	return out.Volumes, err
}

// GetVolume returns one volume, by listing and filtering: the API has no
// per-volume read.
func (s *StorageService) GetVolume(ctx context.Context, id string) (*Volume, error) {
	items, err := s.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, notFound("volume", id, "/storage/volumes")
}

// CreateVolume queues a volume creation. sizeGB is in GiB.
func (s *StorageService) CreateVolume(ctx context.Context, name, description string, sizeGB int) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/storage/volumes", nil, map[string]any{
		"name": name, "description": description, "size": sizeGB,
	})
}

// CreateVolumeAndWait creates a volume and returns it once available.
func (s *StorageService) CreateVolumeAndWait(ctx context.Context, name, description string, sizeGB int) (*Volume, error) {
	ref, err := s.CreateVolume(ctx, name, description, sizeGB)
	if err != nil {
		return nil, err
	}
	id, _, err := s.client.waitForResourceID(ctx, ref, "volume_id")
	if err != nil {
		return nil, err
	}
	return s.GetVolume(ctx, id)
}

// DeleteVolume queues a volume deletion.
func (s *StorageService) DeleteVolume(ctx context.Context, id string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/storage/volumes", volumeQuery(id), nil)
}

// AttachVolume queues attaching a volume to an instance. The completed task's
// result carries "device_id", the stable /dev/disk/by-id name inside the guest.
func (s *StorageService) AttachVolume(ctx context.Context, volumeID, instanceID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/storage/volumes/attach", volumeQuery(volumeID),
		map[string]string{"instance_id": instanceID})
}

// DetachVolume queues detaching a volume from an instance.
func (s *StorageService) DetachVolume(ctx context.Context, volumeID, instanceID string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPost, "/storage/volumes/detach", volumeQuery(volumeID),
		map[string]string{"instance_id": instanceID})
}

// ExtendVolume queues growing a volume to newSizeGB. Volumes only grow.
func (s *StorageService) ExtendVolume(ctx context.Context, volumeID string, newSizeGB int) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodPut, "/storage/volumes/extend", volumeQuery(volumeID),
		map[string]int{"new_size": newSizeGB})
}

func volumeQuery(id string) url.Values { return url.Values{"id": {id}} }

// ListContainers returns the project's object containers.
func (s *StorageService) ListContainers(ctx context.Context) ([]Container, error) {
	var out struct {
		Containers []Container `json:"containers"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/storage/containers"}, &out)
	return out.Containers, err
}

// GetContainer returns one container, by listing and filtering.
func (s *StorageService) GetContainer(ctx context.Context, name string) (*Container, error) {
	items, err := s.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Name == name {
			return &items[i], nil
		}
	}
	return nil, notFound("container", name, "/storage/containers")
}

// Retention modes for an immutable container.
//
// GOVERNANCE can be lifted by the platform; COMPLIANCE cannot be lifted by
// anyone until the retention period elapses. A tenant credential can lift
// neither: it is never granted the bypass permission.
const (
	// MaxRetentionDays is the longest default retention the API accepts: seven
	// years. A COMPLIANCE lock cannot be lifted, so the server refuses more.
	MaxRetentionDays = 2555

	RetentionGovernance = "GOVERNANCE"
	RetentionCompliance = "COMPLIANCE"
)

// CreateContainerRequest describes a container and its protection.
//
// Protection can only be set at creation, because S3 does not allow adding
// object locking to an existing bucket. A container for immutable backups must
// be created with ObjectLock set.
type CreateContainerRequest struct {
	Name string `json:"name"`
	// Versioning keeps previous versions of an object. Implied by ObjectLock.
	Versioning bool `json:"versioning,omitempty"`
	// ObjectLock makes stored versions immutable for RetentionDays, for example
	// to protect backups against ransomware.
	ObjectLock bool `json:"object_lock,omitempty"`
	// RetentionMode is [RetentionGovernance] (default) or [RetentionCompliance].
	RetentionMode string `json:"retention_mode,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

// CreateContainer queues a container creation.
func (s *StorageService) CreateContainer(ctx context.Context, name string) (*TaskRef, error) {
	return s.CreateContainerWithOptions(ctx, CreateContainerRequest{Name: name})
}

// CreateContainerWithOptions queues a container creation, optionally versioned
// and immutable.
func (s *StorageService) CreateContainerWithOptions(ctx context.Context, req CreateContainerRequest) (*TaskRef, error) {
	if !req.ObjectLock && (req.RetentionDays > 0 || req.RetentionMode != "") {
		return nil, errors.New("nemau: a retention policy requires object_lock")
	}
	if req.RetentionDays > MaxRetentionDays {
		return nil, fmt.Errorf("nemau: retention_days must not exceed %d", MaxRetentionDays)
	}
	switch req.RetentionMode {
	case "", RetentionGovernance, RetentionCompliance:
	default:
		return nil, fmt.Errorf("nemau: retention_mode must be %q or %q",
			RetentionGovernance, RetentionCompliance)
	}
	return s.client.submit(ctx, http.MethodPost, "/storage/containers", nil, req)
}

// ContainerProtection reads the protection the object store enforces for a
// container.
//
// [Container.Protection] reports what was requested at creation; this method
// reports what is in force. Comparing the two lets an auditor verify that the
// container is immutable.
func (s *StorageService) ContainerProtection(ctx context.Context, container string) (*ContainerProtection, error) {
	var out struct {
		Container  string               `json:"container"`
		Protection *ContainerProtection `json:"protection"`
		Immutable  bool                 `json:"immutable"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/storage/containers/" + url.PathEscape(container) + "/protection",
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Protection, nil
}

// DeleteContainer queues a container deletion.
func (s *StorageService) DeleteContainer(ctx context.Context, name string) (*TaskRef, error) {
	return s.client.submit(ctx, http.MethodDelete, "/storage/containers/"+url.PathEscape(name), nil, nil)
}

// ObjectPage is one page of a container listing.
type ObjectPage struct {
	Objects []Object `json:"objects"`
	// NextMarker is empty on the last page; pass it back as marker to continue.
	// It is an opaque cursor: never build or edit one.
	NextMarker string `json:"next_marker"`
	Count      int    `json:"count"`
}

// Listing page sizes: the server answers 100 objects when no limit is given and
// refuses a limit outside 1 to 1000.
const (
	DefaultListObjectsLimit = 100
	MaxListObjectsLimit     = 1000
)

// ListObjects returns one page of a container's contents. prefix, marker and
// limit are all optional.
//
// limit is 1 to [MaxListObjectsLimit]; zero means the server default
// ([DefaultListObjectsLimit]). marker is the NextMarker of the previous page,
// passed back untouched. To walk a whole container use
// [StorageService.IterateObjects], which follows the cursor for you.
func (s *StorageService) ListObjects(ctx context.Context, container, prefix, marker string, limit int) (*ObjectPage, error) {
	if limit < 0 || limit > MaxListObjectsLimit {
		return nil, fmt.Errorf("nemau: list limit %d out of range: want 1 to %d", limit, MaxListObjectsLimit)
	}
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if marker != "" {
		q.Set("marker", marker)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out ObjectPage
	err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/storage/containers/" + url.PathEscape(container) + "/objects",
		query:  q,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// IterateObjects yields every object of a container under prefix, fetching one
// page at a time and following the server's cursor. Stop early by breaking out
// of the loop: no further page is requested.
//
//	for obj, err := range client.Storage.IterateObjects(ctx, "photos", "2026/") {
//		if err != nil {
//			return err
//		}
//		fmt.Println(obj.Name)
//	}
//
// A failure is yielded once, as the error of the last iteration, and ends the
// sequence. Objects written while the walk is running may or may not appear.
func (s *StorageService) IterateObjects(ctx context.Context, container, prefix string) iter.Seq2[Object, error] {
	return func(yield func(Object, error) bool) {
		marker := ""
		for {
			page, err := s.ListObjects(ctx, container, prefix, marker, MaxListObjectsLimit)
			if err != nil {
				yield(Object{}, err)
				return
			}
			for _, o := range page.Objects {
				if !yield(o, nil) {
					return
				}
			}
			if page.NextMarker == "" {
				return
			}
			// A cursor that does not move would repeat the same page for ever.
			if page.NextMarker == marker {
				yield(Object{}, errors.New("nemau: object listing did not advance (repeated next_marker)"))
				return
			}
			marker = page.NextMarker
		}
	}
}

// UploadedObject is the answer to an upload.
type UploadedObject struct {
	ObjectName string `json:"object_name"`
	Size       int64  `json:"size"`
}

// UploadObject streams an object into a container.
//
// Unlike most storage methods, it is synchronous. size must be the exact byte
// length: the API requires a Content-Length and refuses anything over
// [MaxObjectSize]. The request is not retried, because a streamed body cannot
// be sent twice.
func (s *StorageService) UploadObject(ctx context.Context, container, key, contentType string, body io.Reader, size int64) (*UploadedObject, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var out UploadedObject
	err := s.client.do(ctx, request{
		method:        http.MethodPost,
		path:          "/storage/containers/" + url.PathEscape(container) + "/objects",
		query:         url.Values{"key": {key}},
		rawBody:       body,
		contentLength: size,
		contentType:   contentType,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteObject removes one object. Synchronous; answers 204.
func (s *StorageService) DeleteObject(ctx context.Context, container, key string) error {
	return s.client.do(ctx, request{
		method: http.MethodDelete,
		path:   "/storage/containers/" + url.PathEscape(container) + "/objects",
		query:  url.Values{"key": {key}},
	}, nil)
}

// MaxCopySize is the largest object [StorageService.CopyObject] copies: S3's
// ceiling for one server-side copy request. Beyond it, copy with an S3 access
// key for the container ([StorageService.CreateCredential]) and a tool such as
// `rclone copy`, which splits the copy into parts.
const MaxCopySize = 5 * 1024 * 1024 * 1024

// CopyObjectRequest names a source object and where its copy goes.
type CopyObjectRequest struct {
	// Container and Key name the source object.
	Container string `json:"-"`
	Key       string `json:"key"`
	// DestinationContainer defaults to Container; it must belong to the same
	// project. DestinationKey defaults to Key.
	DestinationContainer string `json:"destination_container,omitempty"`
	DestinationKey       string `json:"destination_key,omitempty"`
	// ReplaceMetadata swaps the source's user metadata for Metadata, and its
	// content type for ContentType when one is given. Left false, both are
	// copied as they are. Copying an object onto itself is only accepted with
	// ReplaceMetadata: it is how metadata is rewritten in place.
	ReplaceMetadata bool              `json:"replace_metadata,omitempty"`
	ContentType     string            `json:"content_type,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// ObjectRetention is the lock an object was written with.
type ObjectRetention struct {
	// Mode is [RetentionGovernance] or [RetentionCompliance].
	Mode        string    `json:"mode"`
	RetainUntil time.Time `json:"retain_until"`
}

// CopiedObject is the object a copy produced.
type CopiedObject struct {
	Container    string    `json:"container"`
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag"`
	VersionID    string    `json:"version_id,omitempty"`
	LastModified time.Time `json:"last_modified"`
	// Retention is set when the destination container holds a default
	// retention: the copy is locked with it.
	Retention *ObjectRetention `json:"retention,omitempty"`
}

// CopyObject copies one object inside the object store, within its container
// or into another container of the same project.
//
// The copy happens inside the object store, and the data does not pass through
// the API or this client. [MaxObjectSize] does not apply; [MaxCopySize] does,
// and a larger object is refused with a 413. Like upload and delete, the call
// is synchronous: there is no task to wait for.
//
// A copy into a container under object lock is written with the destination
// container's default retention. The source's retention does not follow the
// copy, and no shorter one can be requested.
func (s *StorageService) CopyObject(ctx context.Context, req CopyObjectRequest) (*CopiedObject, error) {
	if !req.ReplaceMetadata && (req.ContentType != "" || len(req.Metadata) > 0) {
		return nil, errors.New("nemau: content_type and metadata require ReplaceMetadata")
	}
	var out CopiedObject
	err := s.client.do(ctx, request{
		method: http.MethodPost,
		path:   "/storage/containers/" + url.PathEscape(req.Container) + "/copy",
		body:   req,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadObject streams an object out. The caller must close the returned
// reader.
func (s *StorageService) DownloadObject(ctx context.Context, container, key string) (io.ReadCloser, error) {
	req, err := s.client.newHTTPRequest(ctx, request{
		method: http.MethodGet,
		path:   "/storage/containers/" + url.PathEscape(container) + "/download",
		query:  url.Values{"key": {key}},
	}, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if apiErr := decodeError(resp); apiErr != nil {
		resp.Body.Close()
		return nil, apiErr
	}
	return resp.Body, nil
}

// PresignedObject is a time-limited URL onto one object, usable directly
// against the object store.
type PresignedObject struct {
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxBytes  int64     `json:"max_bytes,omitempty"`
}

// PresignTTLCap is the longest expiry the API accepts.
const PresignTTLCap = time.Hour

// PresignGet returns a URL that downloads one object directly from the object
// store, without going through the Nemau API.
//
// Use it for large objects and bulk export: the other object methods pass the
// data through the API and are limited to [MaxObjectSize] per object. Any HTTP
// client can fetch a presigned URL, whatever the object's size.
//
// Treat the result as a secret: it is a bearer token for that object until it
// expires. A zero ttl uses the platform default (15 minutes).
func (s *StorageService) PresignGet(ctx context.Context, container, key string, ttl time.Duration) (*PresignedObject, error) {
	return s.presign(ctx, container, key, http.MethodGet, ttl)
}

// PresignPut returns a URL that uploads one object directly to the object
// store, without going through the Nemau API, so [MaxObjectSize] does not
// apply. Use it to import large objects.
func (s *StorageService) PresignPut(ctx context.Context, container, key string, ttl time.Duration) (*PresignedObject, error) {
	return s.presign(ctx, container, key, http.MethodPut, ttl)
}

func (s *StorageService) presign(ctx context.Context, container, key, method string, ttl time.Duration) (*PresignedObject, error) {
	if ttl > PresignTTLCap {
		return nil, fmt.Errorf("nemau: requested expiry (%s) exceeds the %s cap", ttl, PresignTTLCap)
	}
	body := map[string]any{"key": key, "method": method}
	if ttl > 0 {
		body["expires_in"] = int(ttl.Seconds())
	}
	var out PresignedObject
	err := s.client.do(ctx, request{
		method: http.MethodPost,
		path:   "/storage/containers/" + url.PathEscape(container) + "/presign",
		body:   body,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// FetchPresigned downloads a presigned URL.
//
// It does not send the client's credential: a presigned URL is already
// authorized, and sending an access key to the object store would expose that
// key to a host outside the API. The caller must close the returned reader.
func (c *Client) FetchPresigned(ctx context.Context, presigned *PresignedObject) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, presigned.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		resp.Body.Close()
		return nil, 0, &APIError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
			Method:     http.MethodGet,
			// The signed query is left out because error strings end up in logs,
			// and the signature is a valid credential.
			Path: "(presigned url)",
		}
	}
	return resp.Body, resp.ContentLength, nil
}

// CredentialAccess is what an S3 access key may do inside its container.
type CredentialAccess string

const (
	// CredentialReadOnly lists and reads objects.
	CredentialReadOnly CredentialAccess = "read_only"
	// CredentialReadWrite also writes and deletes objects. There is no
	// administrative level: a key never changes container settings.
	CredentialReadWrite CredentialAccess = "read_write"
)

func (a CredentialAccess) valid() bool { return a == CredentialReadOnly || a == CredentialReadWrite }

// StorageCredential is an S3 access key scoped to a single object container. It
// never carries the secret, which is only returned in the [IssuedCredential]
// when the key is created.
type StorageCredential struct {
	AccessKeyID string           `json:"access_key_id"`
	Access      CredentialAccess `json:"access"`
	Container   string           `json:"container"`
	CreatedAt   time.Time        `json:"created_at"`
	// ExpiresAt is nil for a key that never expires.
	ExpiresAt *time.Time `json:"expires_at"`
}

// IssuedCredential is the result of creating a key.
//
// SecretAccessKey is only returned here. The platform does not store it, so a
// caller that loses it must create a new key. Printing or logging the value
// with fmt, %v or log/slog redacts the secret; read the field explicitly when
// you need it.
type IssuedCredential struct {
	StorageCredential
	SecretAccessKey string `json:"secret_access_key"`
	// S3Bucket is the bucket name to address with this key from an S3 tool
	// (`aws s3 sync s3://<S3Bucket>`, `rclone sync remote:<S3Bucket>`). It is
	// not the container name: the object store gives each container an opaque
	// bucket name, and the key's policy refers to that name.
	S3Bucket string `json:"s3_bucket"`
	Region   string `json:"region"`
	// Endpoint is the S3 endpoint to use, or empty when the platform has no
	// reachable endpoint to show (a demonstration environment).
	Endpoint string `json:"endpoint"`
}

const redacted = "[redacted]"

// String implements [fmt.Stringer] without the secret.
func (c IssuedCredential) String() string {
	return fmt.Sprintf("IssuedCredential{AccessKeyID:%s Container:%s S3Bucket:%s Access:%s Region:%s Endpoint:%s SecretAccessKey:%s}",
		c.AccessKeyID, c.Container, c.S3Bucket, c.Access, c.Region, c.Endpoint, redacted)
}

// GoString implements [fmt.GoStringer] (%#v) without the secret.
func (c IssuedCredential) GoString() string { return c.String() }

// LogValue implements [slog.LogValuer] without the secret.
func (c IssuedCredential) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("access_key_id", c.AccessKeyID),
		slog.String("container", c.Container),
		slog.String("s3_bucket", c.S3Bucket),
		slog.String("access", string(c.Access)),
		slog.String("secret_access_key", redacted),
	)
}

// CreateCredentialRequest describes a key to create.
type CreateCredentialRequest struct {
	// Access is required.
	Access CredentialAccess
	// ExpiresAt, when non-zero, revokes the key at that instant. It must be in
	// the future and within the platform's maximum lifetime (the server decides).
	ExpiresAt time.Time
}

func credentialsPath(container string) string {
	return "/storage/containers/" + url.PathEscape(container) + "/credentials"
}

// CreateCredential creates an S3 access key scoped to one container.
//
// Presigned URLs cover one object at a time; a key works with `aws s3 sync`,
// `rclone sync`, `s3cmd` and anything else that speaks S3. Each container holds
// a limited number of keys (see [IsCredentialLimit]) and each key can be revoked
// on its own.
//
// The caller needs permission to manage storage credentials
// (`storage:ManageCredentials`). When the cap is reached [IsCredentialLimit] is
// true and [CredentialLimit] reports how many keys are allowed and what that
// counts.
//
// The call is synchronous. It is the only call in this SDK that returns a
// long-lived secret.
func (s *StorageService) CreateCredential(ctx context.Context, container string, req CreateCredentialRequest) (*IssuedCredential, error) {
	if container == "" {
		return nil, errors.New("nemau: container is required")
	}
	if !req.Access.valid() {
		return nil, fmt.Errorf("nemau: access must be %q or %q", CredentialReadOnly, CredentialReadWrite)
	}
	body := struct {
		Access    CredentialAccess `json:"access"`
		ExpiresAt string           `json:"expires_at,omitempty"`
	}{Access: req.Access}
	if !req.ExpiresAt.IsZero() {
		body.ExpiresAt = req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	var out IssuedCredential
	err := s.client.do(ctx, request{method: http.MethodPost, path: credentialsPath(container), body: body}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListCredentials returns a container's access keys, without their secrets,
// which cannot be read again after creation.
func (s *StorageService) ListCredentials(ctx context.Context, container string) ([]StorageCredential, error) {
	if container == "" {
		return nil, errors.New("nemau: container is required")
	}
	var out struct {
		Credentials []StorageCredential `json:"credentials"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: credentialsPath(container)}, &out)
	return out.Credentials, err
}

// RevokeCredential deletes one access key of a container.
func (s *StorageService) RevokeCredential(ctx context.Context, container, accessKeyID string) error {
	if container == "" || accessKeyID == "" {
		return errors.New("nemau: container and access key id are required")
	}
	return s.client.do(ctx, request{
		method: http.MethodDelete,
		path:   credentialsPath(container) + "/" + url.PathEscape(accessKeyID),
	}, nil)
}
