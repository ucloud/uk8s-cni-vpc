# Generate code

Some code needs to be dynamically generated:

- GRPC code, generated from [protobuf]().
- kubernetes CRD deepcopy, clientset, informer code, generated from [apis]().

## Generate grpc

Install prerequisites:

```bash
sudo pacman -S protoc  # For archlinux
brew install protoc    # For mac
go install github.com/golang/protobuf/protoc-gen-go@latest
```

You define ipamd grpc specification by changing [grpc/ipamd.proto](../grpc/ipamd.proto) file.

After changing done, run the following command to re-generate code:

```bash
make generate-grpc
```

## Generate CRD code

### Change CRD fields

If you want to change existsing CRD, such as `ipamds.vpc.uk8s.ucloud.cn`, you should edit its corresponding `types.go` file, such as [apis/ipamd/v1beta1/types.go](../apis/ipamd/v1beta1/types.go).

The CRD definition should also be modified, it is always be placed in [deploy](../deploy) directory, such as [deploy/ipamd.yaml](../deploy/ipamd.yaml), you can find CRD definition like:

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ipamds.vpc.uk8s.ucloud.cn
spec:
  group: vpc.uk8s.ucloud.cn
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
              # TODO: change spec fields here.
            status:
              type: object
              properties:
              # TODO: change status fields here.
  scope: Namespaced
  names:
    plural: ipamds
    singular: ipamd
    kind: Ipamd

```

**The `yaml definition` must be consistent with the code definition in `types.go`.**

Then, re-generate code:

```bash
make generate-k8s
```

### Add a new CRD

First, you need to confirm the name, group, and version for the CRD. Suppose we want to create a CRD as follows:

- name: `exmaple`
- group: `exmaple.uk8s.ucloud.cn`
- version: `v1beta1`

Add group register for your CRD, create file `apis/example/register.go`:

```go
// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package vipcontroller

// GroupName is the group name used in this package
const (
	GroupName = "exmaple.uk8s.ucloud.cn"
)
```

You should replace the `GroupName` to your own value.

Add `types.go` file, create file `apis/example/v1beta1/types.go`:

> If you are using the different version, change the `v1beta1` in the path to your own version.

```go
// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Example is a specification for a Example resource
type Example struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExampleSpec   `json:"spec"`
	Status ExampleStatus `json:"status"`
}

// ExampleSpec is the spec for a Example resource
type ExampleSpec struct {
    // TODO: fill with your own fields
}

// ExampleSpec is the status for Example resource
type IpamdStatus struct {
    // TODO: fill with your own fields
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ExmapleList is a list of Example resources
type ExampleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Example `json:"items"`
}
```

You should replace `Example` with your own type name, and fill in the spec and status type.

Edit [hack/update-codegen.sh](../hack/update-codegen.sh), find the following line, add the CRD, the format is `"name:version"`:

```bash
bash code-generator/generate-groups.sh "deepcopy,client,informer,lister" \
  github.com/ucloud/uk8s-cni-vpc/generated  github.com/ucloud/uk8s-cni-vpc/apis \
  "ipamd:v1beta1 vipcontroller:v1beta1 example:v1beta1" \   # add your new CRD here.
  --output-base generated_tmp \
  --go-header-file hack/boilerplate.go.txt
```

You should replace `exmaple:v1beta1` with your own type name and version.

Find the copy commands:

```bash
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/generated ./
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/apis/ipamd/v1beta1/zz_generated.deepcopy.go ./apis/ipamd/v1beta1
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/apis/vipcontroller/v1beta1/zz_generated.deepcopy.go ./apis/vipcontroller/v1beta1
```

Add a new command behind them to copy deepcopy file for your new CRD:

```bash
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/apis/exmaple/v1beta1/zz_generated.deepcopy.go ./apis/exmaple/v1beta1
```

You should replace `exmaple` with your own type name.

Okay, now you are ready to generate code:

```bash
make generate-k8s
```

Donot forget to add CRD definition in [deploy](../deploy).