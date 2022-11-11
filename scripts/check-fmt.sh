#!/bin/bash

set -e

make fmt
if [[ ! -z $(git status -s) ]]; then
	echo "Have unformatted files, please run 'make fmt' before committing"
	exit 1
fi
