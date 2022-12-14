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

// Code generated by informer-gen. DO NOT EDIT.

package v1beta1

import (
	"context"
	time "time"

	vipcontrollerv1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/vipcontroller/v1beta1"
	versioned "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/clientset/versioned"
	internalinterfaces "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/informers/externalversions/internalinterfaces"
	v1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/listers/vipcontroller/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// VpcIpClaimInformer provides access to a shared informer and lister for
// VpcIpClaims.
type VpcIpClaimInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1beta1.VpcIpClaimLister
}

type vpcIpClaimInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewVpcIpClaimInformer constructs a new informer for VpcIpClaim type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewVpcIpClaimInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredVpcIpClaimInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredVpcIpClaimInformer constructs a new informer for VpcIpClaim type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredVpcIpClaimInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.VipcontrollerV1beta1().VpcIpClaims(namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.VipcontrollerV1beta1().VpcIpClaims(namespace).Watch(context.TODO(), options)
			},
		},
		&vipcontrollerv1beta1.VpcIpClaim{},
		resyncPeriod,
		indexers,
	)
}

func (f *vpcIpClaimInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredVpcIpClaimInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *vpcIpClaimInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&vipcontrollerv1beta1.VpcIpClaim{}, f.defaultInformer)
}

func (f *vpcIpClaimInformer) Lister() v1beta1.VpcIpClaimLister {
	return v1beta1.NewVpcIpClaimLister(f.Informer().GetIndexer())
}
