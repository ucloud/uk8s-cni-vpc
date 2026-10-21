# Metadata isolation integration test

Run in a disposable privileged container, **never directly on a node**. The test
uses real nft and legacy rules, a Pod veth/netns and a separate upstream metadata
netns. It exercises forwarded Pod traffic, host traffic, ordinary traffic,
concurrent reconciliation, deletion/priority repair, malformed config and rollback.

From the repository root (Docker's Linux architecture must match GOARCH):

```sh
mkdir -p bin/metadata-test
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o bin/metadata-test/metadata.test ./pkg/hostnetwork
docker build -t cni-metadata-test tests/metadata
docker run --rm --privileged --network none \
  -e UK8S_METADATA_INTEGRATION=1 \
  -v "$PWD/bin/metadata-test:/tests:ro" \
  cni-metadata-test /tests/metadata.test -test.v -test.timeout=90s
```

Use `GOARCH=amd64` for an x86 Docker engine. Without the explicit environment flag,
the integration test skips rather than modifying the executing machine's network.
