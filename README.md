# nemau-sdk-go

nemau-sdk-go is the Go client library for the Nemau Cloud public API. It uses
the Go standard library only.

Requirements: Go 1.27 or later.

## Getting started

Add the module to your project:

```sh
go get github.com/nemau-cloud/nemau-sdk-go@latest
```

Create an access key in the Nemau Cloud console and export it:

```sh
export NEMAU_ACCESS_KEY_ID=...
export NEMAU_ACCESS_KEY_SECRET=...
```

Called without options, `nemau.NewClient` reads the credential from these
variables. When no access key is set, it uses a service-account token from
`NEMAU_TOKEN`. Two more variables are optional:

- `NEMAU_PROJECT_ID` selects the project to act on. Without it, requests act on
  the project the credential resolves to.
- `NEMAU_API_URL` sets the API origin, `https://api.nemau-cloud.com` by default.

The following program lists the instances of the project:

```go
package main

import (
	"context"
	"fmt"
	"log"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

func main() {
	client, err := nemau.NewClient()
	if err != nil {
		log.Fatal(err)
	}

	instances, err := client.Compute.ListInstances(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, instance := range instances {
		fmt.Println(instance.ID, instance.Name, instance.Status)
	}
}
```

It prints one line per instance, with its id, name and status:

```text
6f1c0e8a-2b7d-4c39-9e51-3a8f0d4b7c12 web-1 running
```

Most writes are asynchronous. A create, update or delete returns a
`*nemau.TaskRef` once the API has accepted it, and `client.WaitFor` waits until
the task has finished. The methods ending in `AndWait` do both and return the
resource. See [Asynchronous writes](docs/asynchronous-writes.md).

[`examples/provision`](examples/provision) is a complete program that creates a
network, a subnet, a security group and an instance, then deletes them.

## Documentation

- [Guide](docs/README.md)
  - [Authentication](docs/authentication.md)
  - [Client configuration](docs/configuration.md)
  - [Organizations and projects](docs/projects.md)
  - [Asynchronous writes](docs/asynchronous-writes.md)
  - [Updating resources](docs/updating-resources.md)
  - [Errors and retries](docs/errors-and-retries.md)
  - [Compute](docs/compute.md) and [snapshots](docs/snapshots.md)
  - [Networking](docs/networking.md)
  - [Load balancers](docs/load-balancers.md)
  - [Block storage](docs/block-storage.md) and
    [object storage](docs/object-storage.md)
  - [DNS](docs/dns.md)
  - [Kubernetes](docs/kubernetes.md)
  - [Quotas](docs/quotas.md), [metering](docs/metering.md) and
    [regions](docs/regions.md)
- [API reference](https://pkg.go.dev/github.com/nemau-cloud/nemau-sdk-go) on
  pkg.go.dev
- [Nemau Cloud documentation](https://docs.nemau-cloud.com/en/tools/sdks)

## Getting help

- Report bugs and request features in the
  [issue tracker](https://github.com/nemau-cloud/nemau-sdk-go/issues).
- Send general questions to contact@nemau-cloud.com.
- Report security issues privately, as described in [SECURITY.md](SECURITY.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, test and propose a
change.

## License

This SDK is distributed under the Apache License 2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).

Changes between releases are listed in [CHANGELOG.md](CHANGELOG.md). Until
version 1.0, a minor release may change the API.
