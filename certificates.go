package nemau

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Certificate is a TLS certificate imported into the project, for
// TERMINATED_HTTPS listeners.
//
// It carries the certificate's own details and never the private key: the
// platform stores the key encrypted and only sends it to the load-balancer
// appliances. A certificate is immutable; to renew one, import
// the new certificate, point the listener at it with
// [LoadBalancerService.UpdateListener] and delete the old one.
type Certificate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// FingerprintSHA256 has the form `openssl x509 -fingerprint -sha256` prints.
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	Subject           string    `json:"subject"`
	Domains           []string  `json:"domains"`
	Issuer            string    `json:"issuer"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	// KeyAlgorithm is "RSA-2048" to "RSA-4096", "ECDSA-P256" or "ECDSA-P384".
	KeyAlgorithm string `json:"key_algorithm"`
	// ChainLength counts the intermediate certificates served with the leaf.
	ChainLength int              `json:"chain_length"`
	UsedBy      []CertificateUse `json:"used_by"`
	CreatedAt   time.Time        `json:"created_at"`
}

// CertificateUse names a listener that serves the certificate.
type CertificateUse struct {
	ListenerID     string `json:"listener_id"`
	ListenerName   string `json:"listener_name"`
	LoadBalancerID string `json:"load_balancer_id,omitempty"`
}

// ImportCertificateRequest carries PEM-encoded material.
//
// Certificate holds the leaf, optionally followed by its chain (a
// fullchain.pem); Chain adds intermediates. PrivateKey must not be
// passphrase-protected. Accepted keys: RSA 2048 to 4096 bits, ECDSA P-256 and
// P-384.
type ImportCertificateRequest struct {
	Name        string `json:"name"`
	Certificate string `json:"certificate"`
	Chain       string `json:"chain,omitempty"`
	PrivateKey  string `json:"private_key"`
}

// Reasons an [APIError] carries when a certificate is refused.
const (
	ReasonCertificateInvalidPEM         = "certificate_invalid_pem"
	ReasonCertificateTooLarge           = "certificate_too_large"
	ReasonCertificateKeyInvalid         = "certificate_key_invalid"
	ReasonCertificateKeyEncrypted       = "certificate_key_encrypted"
	ReasonCertificateKeyUnsupported     = "certificate_key_unsupported"
	ReasonCertificateKeyMismatch        = "certificate_key_mismatch"
	ReasonCertificateExpired            = "certificate_expired"
	ReasonCertificateNotYetValid        = "certificate_not_yet_valid"
	ReasonCertificateWeakSignature      = "certificate_weak_signature"
	ReasonCertificateChainInvalid       = "certificate_chain_invalid"
	ReasonCertificateNameInvalid        = "certificate_name_invalid"
	ReasonCertificateNameTaken          = "certificate_name_taken"
	ReasonCertificateInUse              = "certificate_in_use"
	ReasonCertificateStorageUnavailable = "certificate_storage_unavailable"
)

// IsCertificateInUse reports whether err is the 409 refusing to delete a
// certificate a listener still serves.
func IsCertificateInUse(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Reason == ReasonCertificateInUse
}

const certificatesPath = "/network/load-balancer-certificates"

// ImportCertificate validates and stores a certificate. Synchronous: the
// certificate is usable by a listener as soon as this returns. The private key
// is not part of the response and cannot be read back.
func (s *LoadBalancerService) ImportCertificate(ctx context.Context, req ImportCertificateRequest) (*Certificate, error) {
	var out Certificate
	if err := s.client.do(ctx, request{method: http.MethodPost, path: certificatesPath, body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListCertificates returns the project's certificates and the listeners that
// serve them.
func (s *LoadBalancerService) ListCertificates(ctx context.Context) ([]Certificate, error) {
	var out struct {
		Certificates []Certificate `json:"certificates"`
	}
	err := s.client.do(ctx, request{method: http.MethodGet, path: certificatesPath}, &out)
	return out.Certificates, err
}

// GetCertificate returns one certificate of the project.
func (s *LoadBalancerService) GetCertificate(ctx context.Context, id string) (*Certificate, error) {
	var out Certificate
	if err := s.client.do(ctx, request{method: http.MethodGet, path: certificatesPath + "/" + url.PathEscape(id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCertificate removes a certificate that no listener serves. While one
// does, the API answers 409; see [IsCertificateInUse].
func (s *LoadBalancerService) DeleteCertificate(ctx context.Context, id string) error {
	return s.client.do(ctx, request{method: http.MethodDelete, path: certificatesPath + "/" + url.PathEscape(id)}, nil)
}
