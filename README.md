<h1 align="center">UCloud K8s CNI-VPC Plugin</h1>

This is a [CNI plugin](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/) to implement the underlay container networking based on [UCloud VPC 2.0](https://docs.ucloud.cn/vpc/README).

## Install

The `uk8s-cni-vpc` will be installed in the uk8s cluster by default, and you do not need to perform additional installation operations.

You can login to any machine in the uk8s cluster and confirm the plugin version with the following command:

```bash
/opt/cni/bin/cnivpc version
```

The [ipamd](https://docs.ucloud.cn/uk8s/network/ipamd) is an independent component of the plugin that manages the IP addresses of Pods. It is deployed in the form of [DaemonSet](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/), and you can check the version of ipamd through the following command:

```bash
kubectl get -n kube-system ds cni-vpc-ipamd -oyaml
```

For more information, please refer to our documentation.

## Documentation

You can find complete documentation about the UK8S cluster network [here](https://docs.ucloud.cn/uk8s/network/uk8s_network).

## Contribution

### Generate grpc code

If you modify the file [rpc/ipamd.proto](rpc/ipamd.proto), the grpc code needs to be regenerated.

The grpc code generation is based on the [protoc](https://developers.google.com/protocol-buffers/docs/downloads) tool. You have to make sure it is properly installed on your system. You can install it through different ways, such as package managers of different systems:

```bash
sudo pacman -S protoc        # For archlinux
brew install protoc          # For macos
sudo apt-get install protoc  # For ubuntu or debain
yum install protoc           # For centos
```

In addition, [protoc-gen-go](https://pkg.go.dev/github.com/golang/protobuf/protoc-gen-go) needs to be installed, which can be done by the following command:

```bash
go install github.com/golang/protobuf/protoc-gen-go@latest
```

Now, you can regenerate the grpc code:

```bash
make generate-grpc
```

View the differences:

```bash
git diff rpc/*.go
```

### Generate kubernetes code

`uk8s-cni-vpc` needs to use some [Kubernetes Custom Resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/). These Custom Resources are defined in the [kubernetes/apis](kubernetes/apis) directory.

If you modify the `types.go` file, the code needs to be regenerated, you can run the following command:

```bash
make generate-k8s
```

If you want to create a new Custom Resource, you need to create the corresponding files under the [kubernetes/apis](kubernetes/apis) directory. You can use the Custom Resource has already been created as example, such as [kubernetes/apis/ipamd](kubernetes/apis/ipamd).

The new Custom Resource also needs to be added in [hack/update-codegen.sh](hack/update-codegen.sh).

There are two places that need to be changed. First, find the `bash code-generator/generate-groups.sh` command and add your new Custom Resource to the args. Suppose the new Custom Resource is called `example` and the version is `v1beta1`, the updated command will be:

```bash
bash code-generator/generate-groups.sh "deepcopy,client,informer,lister" \
  github.com/ucloud/uk8s-cni-vpc/kubernetes/generated  github.com/ucloud/uk8s-cni-vpc/kubernetes/apis \
  "ipamd:v1beta1 vipcontroller:v1beta1 example:v1beta1" \ # Add your new CR here
  --output-base generated_tmp \
  --go-header-file hack/boilerplate.go.txt
```

Then, you also need to add the copy command of the deepcopy file before `rm -rf generated_tmp`:

```bash
cp -r generated_tmp/github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/exmaple/v1beta1/zz_generated.deepcopy.go ./kubernetes/apis/example/v1beta1
```

Don't forget to replace `exmaple` and `v1beta1` with your own Custom Resource name and version.

Finally, use `make generate-k8s`, the new Custom Resource code will be generated.

In addition, Custom Resource's yaml needs to be written manually, which is generally defined under [deploy](deploy) directory, you can refer to the following example:

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

### Build

Requires your machine to have **Docker** and **Golang** to build cnivpc. Please use the following command to build:

```bash
make cnivpc-bin     # Build cnivpc binary files only
make cnivpc         # Build cnivpc binary files and docker image
make ipamd          # Build ipamd binary file and docker image
make vip-controller # Build vip-controller binary file and docker image
```

### Release

As long as you create a tag, all docker images will be automatically built and published through [Github Action](https://docs.github.com/en/actions). See: [release workflow](.github/workflows/release.yml).
