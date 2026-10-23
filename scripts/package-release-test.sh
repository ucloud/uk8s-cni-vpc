#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture"/{scripts,bin,config}
cp "$root/scripts/package-release.sh" "$fixture/scripts/"
cp "$root/LICENSE" "$root/README.md" "$fixture/"
cp "$root/config/10-cnivpc.conf" "$fixture/config/"
printf 'fixture binary\n' > "$fixture/bin/cnivpc"
"$fixture/scripts/package-release.sh" test
for entry in LICENSE README.md cnivpc 10-cnivpc.conf; do
  tar -xOf "$fixture/uk8s-cni-vpc_vtest.tar.gz" "$entry" >/dev/null
done
rm "$fixture/config/10-cnivpc.conf"
if "$fixture/scripts/package-release.sh" test; then
  echo "packaging must fail when an input is missing" >&2
  exit 1
fi
test ! -e "$fixture/uk8s-cni-vpc_vtest.tar.gz"
echo "release packaging checks passed"
