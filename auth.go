package nemau

import (
	"net/http"
	"os"
	"strings"
)

// Credential authenticates a request. The API accepts three kinds of
// credential; this package implements the two that work without a browser.
type Credential interface {
	apply(*http.Request)
	// describe returns a redacted, loggable identity for diagnostics.
	describe() string
}

// AccessKey is an IAM user's long-lived programmatic credential, presented as
// X-Access-Key-Id / X-Access-Key-Secret. It inherits the owning user's group
// policies, and the project it acts on is that user's active project.
//
// Use it for a CLI or a Terraform run: CSRF protection does not apply to it.
// A key expires 90 days after creation unless the owner chose another date, and
// 365 days at most. Issue the next key before it expires.
type AccessKey struct {
	ID     string
	Secret string
}

func (k AccessKey) apply(r *http.Request) {
	r.Header.Set("X-Access-Key-Id", k.ID)
	r.Header.Set("X-Access-Key-Secret", k.Secret)
}

func (k AccessKey) describe() string { return "access-key " + k.ID }

// BearerToken is a service-account token obtained from
// POST /api/v1/iam/service-accounts/token. It carries the account's pinned
// project and is valid for 15 minutes. This type holds a fixed string and does
// not refresh it, so a long-running process must request a new token itself.
type BearerToken string

func (t BearerToken) apply(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+string(t))
}

func (t BearerToken) describe() string { return "service-account token" }

// SessionToken is a human session from the console, presented as
// x-session-token. A session comes from a browser cookie and its writes are
// CSRF-protected, so in practice it is only usable for reads.
type SessionToken string

func (t SessionToken) apply(r *http.Request) {
	r.Header.Set("x-session-token", string(t))
}

func (t SessionToken) describe() string { return "console session" }

// Environment variables read by [CredentialFromEnv].
const (
	EnvAccessKeyID     = "NEMAU_ACCESS_KEY_ID"
	EnvAccessKeySecret = "NEMAU_ACCESS_KEY_SECRET"
	EnvToken           = "NEMAU_TOKEN"
	EnvAPIURL          = "NEMAU_API_URL"
	EnvProject         = "NEMAU_PROJECT_ID"
)

// CredentialFromEnv builds a credential from the environment, or returns nil
// when none is configured.
//
// The access-key pair takes precedence over NEMAU_TOKEN, so that an expired
// token left in a shell does not hide a valid key.
func CredentialFromEnv() Credential {
	id := envOrEmpty(EnvAccessKeyID)
	secret := envOrEmpty(EnvAccessKeySecret)
	if id != "" && secret != "" {
		return AccessKey{ID: id, Secret: secret}
	}
	if token := envOrEmpty(EnvToken); token != "" {
		return BearerToken(token)
	}
	return nil
}

// DescribeCredential returns a redacted description of the client's credential,
// for a `whoami`-style command. It never returns the secret.
func (c *Client) DescribeCredential() string {
	if c.credential == nil {
		return "none"
	}
	return c.credential.describe()
}

func envOrEmpty(key string) string { return strings.TrimSpace(os.Getenv(key)) }
