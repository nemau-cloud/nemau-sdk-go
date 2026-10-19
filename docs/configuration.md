# Client configuration

`nemau.NewClient` builds a client from the environment (see
[Authentication](authentication.md)) and then applies its options, so an option
always takes precedence over an environment variable.

```go
client, err := nemau.NewClient(
	nemau.WithUserAgent("deploy-bot/1.4"),
	nemau.WithPollInterval(5*time.Second),
	nemau.WithHTTPClient(&http.Client{Timeout: 2 * time.Minute}),
)
if err != nil {
	return err
}
```

A `Client` is safe for concurrent use. Its zero value is not usable: always
build one with `NewClient`.

## Options

| Option | Effect | Default |
| --- | --- | --- |
| `WithBaseURL(origin)` | API origin, such as `https://api.nemau-cloud.com`. Pass the origin only: the client adds the `/api/v1` prefix. The URL must be absolute. | `NEMAU_API_URL`, then `nemau.DefaultBaseURL` |
| `WithCredential(cred)` | Credential sent on every request. See [Authentication](authentication.md). | From the environment |
| `WithProject(id)` | Project every request acts on. See [Organizations and projects](projects.md). | `NEMAU_PROJECT_ID`, then the credential's project |
| `WithHTTPClient(hc)` | The `*http.Client` used for every request, for a proxy, a TLS configuration or a different timeout. It must not be nil. | A client with a 60-second timeout |
| `WithUserAgent(ua)` | Identifier added in front of the SDK's own `User-Agent`. | `nemau-sdk-go/<version>` |
| `WithRetry(policy)` | Retry policy. See [Errors and retries](errors-and-retries.md). | `nemau.DefaultRetryPolicy()` |
| `WithPollInterval(d)` | How often `WaitFor` reads a task while waiting. It must be positive. | 2 seconds |

An option that receives an invalid value makes `NewClient` return an error.

## Timeouts

The HTTP client's timeout applies to each request, including the time spent
reading the response body. `StorageService.DownloadObject` and
`Client.FetchPresigned` read through the same HTTP client, so the default
60-second timeout also bounds a download. For large transfers, pass an
`*http.Client` with a longer timeout, or with no timeout and a deadline on the
context.

The time spent waiting for a task is bounded by the context. See
[Asynchronous writes](asynchronous-writes.md#timeouts-and-cancellation).

## Reading the configuration

| Method | Returns |
| --- | --- |
| `client.BaseURL()` | The API origin in use. |
| `client.Project()` | The selected project id, empty when the credential's project applies. |
| `client.PollInterval()` | The interval used by `WaitFor`. |
| `client.DescribeCredential()` | A description of the credential, without the secret. |

`nemau.Version` holds the SDK version reported in the `User-Agent`.
