# Authentication

Every request carries one credential. The SDK supports three kinds:

| Type | Sent as | Use |
| --- | --- | --- |
| `nemau.AccessKey{ID, Secret}` | `X-Access-Key-Id` and `X-Access-Key-Secret` headers | An IAM user's programmatic key. The usual choice for scripts, the CLI and Terraform. |
| `nemau.BearerToken` | `Authorization: Bearer` header | A service-account token, valid for 15 minutes. |
| `nemau.SessionToken` | `x-session-token` header | A console session. Writes from a session are CSRF-protected, so in practice it can only read. |

## Credentials from the environment

`nemau.NewClient` reads these environment variables. Options passed to
`NewClient` take precedence over them.

| Variable | Constant | Meaning |
| --- | --- | --- |
| `NEMAU_ACCESS_KEY_ID` | `nemau.EnvAccessKeyID` | Access key id. |
| `NEMAU_ACCESS_KEY_SECRET` | `nemau.EnvAccessKeySecret` | Access key secret. |
| `NEMAU_TOKEN` | `nemau.EnvToken` | Service-account token, used when the access key pair is incomplete or absent. |
| `NEMAU_PROJECT_ID` | `nemau.EnvProject` | Project to act on. See [Organizations and projects](projects.md). |
| `NEMAU_API_URL` | `nemau.EnvAPIURL` | API origin. Defaults to `nemau.DefaultBaseURL`, `https://api.nemau-cloud.com`. |

The access key pair takes precedence over `NEMAU_TOKEN`, so that an expired
token left in a shell does not hide a valid key. Leading and trailing
whitespace is removed from each value. `nemau.CredentialFromEnv()` returns the credential
these variables describe, or nil when none is set.

## Passing a credential

`nemau.WithCredential` sets the credential explicitly:

```go
client, err := nemau.NewClient(nemau.WithCredential(nemau.AccessKey{
	ID:     keyID,
	Secret: keySecret,
}))
if err != nil {
	return err
}
```

## Access keys

An access key belongs to an IAM user and has the permissions of that user's
groups. The project it acts on is the user's active project, unless the client
selects another one.

The console issues a key for 90 days by default and 365 days at most, and a
user can hold five active keys. After expiry the API answers `401`, which
`nemau.IsUnauthorized(err)` recognises. Create the next key before the current
one expires.

## Service-account tokens

A service-account token comes from the API's token endpoint
(`POST /api/v1/iam/service-accounts/token`). It carries the account's pinned
project and is valid for 15 minutes. `nemau.BearerToken` holds a fixed string
and does not refresh it: a long-running process must request a new token and
build a new client with it.

```go
client, err := nemau.NewClient(nemau.WithCredential(nemau.BearerToken(token)))
```

## Calls without a credential

A missing credential is not an error when the client is built. The region
endpoints need no authentication (see [Regions](regions.md)); any other call
fails with a `401` `*nemau.APIError`.

## Checking the credential in use

`client.DescribeCredential()` returns a description without the secret, such as
`access-key <id>`, `service-account token`, `console session`, or `none` when
the client has no credential:

```go
fmt.Println("credential:", client.DescribeCredential())
```
