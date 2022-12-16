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

// Code generated by lister-gen. DO NOT EDIT.

package v1beta1

import (
	v1beta1 "github.com/ucloud/uk8s-cni-vpc/apis/ipamd/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// IpamdLister helps list Ipamds.
// All objects returned here must be treated as read-only.
type IpamdLister interface {
	// List lists all Ipamds in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.Ipamd, err error)
	// Ipamds returns an object that can list and get Ipamds.
	Ipamds(namespace string) IpamdNamespaceLister
	IpamdListerExpansion
}

// ipamdLister implements the IpamdLister interface.
type ipamdLister struct {
	indexer cache.Indexer
}

// NewIpamdLister returns a new IpamdLister.
func NewIpamdLister(indexer cache.Indexer) IpamdLister {
	return &ipamdLister{indexer: indexer}
}

// List lists all Ipamds in the indexer.
func (s *ipamdLister) List(selector labels.Selector) (ret []*v1beta1.Ipamd, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.Ipamd))
	})
	return ret, err
}

// Ipamds returns an object that can list and get Ipamds.
func (s *ipamdLister) Ipamds(namespace string) IpamdNamespaceLister {
	return ipamdNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// IpamdNamespaceLister helps list and get Ipamds.
// All objects returned here must be treated as read-only.
type IpamdNamespaceLister interface {
	// List lists all Ipamds in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.Ipamd, err error)
	// Get retrieves the Ipamd from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1beta1.Ipamd, error)
	IpamdNamespaceListerExpansion
}

// ipamdNamespaceLister implements the IpamdNamespaceLister
// interface.
type ipamdNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all Ipamds in the indexer for a given namespace.
func (s ipamdNamespaceLister) List(selector labels.Selector) (ret []*v1beta1.Ipamd, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.Ipamd))
	})
	return ret, err
}

// Get retrieves the Ipamd from the indexer for a given namespace and name.
func (s ipamdNamespaceLister) Get(name string) (*v1beta1.Ipamd, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1beta1.Resource("ipamd"), name)
	}
	return obj.(*v1beta1.Ipamd), nil
}
