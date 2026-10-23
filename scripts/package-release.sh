#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:?usage: package-release.sh VERSION}
archive="uk8s-cni-vpc_v${version}.tar.gz"
# Remove a stale archive before validation so failed builds cannot upload it.
rm -f "$archive"
test -s bin/cnivpc
test -s config/10-cnivpc.conf
tmp=$(mktemp "${archive}.XXXXXX")
trap 'rm -f "$tmp"' EXIT
tar -czf "$tmp" LICENSE README.md -C bin cnivpc -C ../config 10-cnivpc.conf
tar -tzf "$tmp"
mv "$tmp" "$archive"
