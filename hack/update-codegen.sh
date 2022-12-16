#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

if [ ! -d code-generator ]; then
	git clone https://github.com/kubernetes/code-generator.git code-generator
	git -C code-generator checkout v0.21.3
fi

bash code-generator/generate-groups.sh "deepcopy,client,informer,lister" \
  github.com/ucloud/uk8s-cni-vpc/generated  github.com/ucloud/uk8s-cni-vpc/apis \
  "ipamd:v1beta1 vipcontroller:v1beta1" \
  --output-base generated_tmp \
  --go-header-file hack/boilerplate.go.txt

cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/generated ./
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/apis/ipamd/v1beta1/zz_generated.deepcopy.go ./apis/ipamd/v1beta1
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/apis/vipcontroller/v1beta1/zz_generated.deepcopy.go ./apis/vipcontroller/v1beta1

rm -rf generated_tmp
