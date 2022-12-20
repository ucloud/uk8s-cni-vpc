English | [简体中文](README_cn.md)

<h1 align="center">UCloud K8s CNI-VPC Plugin</h1>

This a [CNI plugin](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/) to implement underlay container network based on [UCloud VPC 2.0](https://docs.ucloud.cn/vpc/README).

## Install

The `uk8s-cni-vpc` will be installed in the uk8s cluster by default, and you do not need to perform additional installation operations.

You can login to any machine in the uk8s cluster and confirm the plugin version with the following command:

```bash
/opt/cni/bin/cnivpc version
```

The [ipamd]() is an independent component of the plugin that manages the IP addresses of Pods. It is deployed in the form of [DaemonSet](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/), and you can check the version of ipamd through the following command:

```bash
kubectl get -n kube-system ds cni-vpc-ipamd -oyaml
```

For more information, please refer to our documentation.

## Documentation

You can find complete documentation about the UK8S cluster network [here](https://docs.ucloud.cn/uk8s/network/uk8s_network).

## Contribution

If you find any bug, or have suggestion, you can [create an issue](https://github.com/ucloud/uk8s-cni-vpc/issues/new/choose) directly. We will give timely feedback after receiving the issue. When reporting a bug, it is recommended that you provide enough information, including version, error log, etc., so that we can locate the problem faster.

If you would like to contribute code, you can [create a PR](https://github.com/ucloud/uk8s-cni-vpc/compare) directly, but it is recommended to read the following carefully before contributing.

### Golang

`uk8s-cni-vpc` uses some very new features of Go, such as the [Generics](https://go.dev/blog/intro-generics). So please make sure your Go version is up to date, otherwise compilation errors may occur.

You can find the recommanded version of Go in our [Github workflow file](https://github.com/ucloud/uk8s-cni-vpc/blob/main/.github/workflows/build-go.yml#L21).

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

**`uk8s-cni-vpc` can only be built in Linux.** If you are using a Linux system, you can directly use the following command to build:

```bash
make build
```

If you are using other systems, such as macos, you need to use docker to build:

```bash
make docker-build
make docker-build-cni
```

In some newer Linux systems, due to glibc compatibility issues, the directly built cni binary files may not be able to run on the UK8S machine. If this happens, use `make docker-build-cni` to build the cni binary.

### Testing

First of all, if you want to test, you need to create a test uk8s cluster, which will incur a certain fee, so you can contact our [spt support](https://spt.ucloud.cn/) to let us assist you in the testing.

If you want to test cni, just replace the remote binary file directly (the `{node-ip}` indicates the machine you need to test):

```bash
make docker-build-cni  # Build the testing cni binary file
scp ./bin/cnivpc root@{node-ip}:/opt/cni/bin/cnivpc  # Replace the binary file
```

If you want to test ipamd, you need to rebuild the ipamd image, and then update the daemonset in the test cluster:

```bash
make docker-deploy  # Build and deploy the test images
kubectl -n kube-system edit ds cni-vpc-ipamd  # Update the ipamd image
```

### Deploy

As long as you create a tag, all docker images will be automatically built and published through [Github Action](https://docs.github.com/en/actions). See: [release workflow](.github/workflows/release.yml).

The image name is: `uhub.service.ucloud.cn/uk8s/{name}:{tag}`.
