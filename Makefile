.PHONY: test vet fmt tidy check
test:
	go test ./...
vet:
	go vet ./...
fmt:
	gofmt -w .
tidy:
	go mod tidy
check: fmt vet test
