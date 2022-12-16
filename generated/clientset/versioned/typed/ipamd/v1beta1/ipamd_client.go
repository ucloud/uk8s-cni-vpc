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

// Code generated by client-gen. DO NOT EDIT.

package v1beta1

import (
	v1beta1 "github.com/ucloud/uk8s-cni-vpc/apis/ipamd/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/generated/clientset/versioned/scheme"
	rest "k8s.io/client-go/rest"
)

type IpamdV1beta1Interface interface {
	RESTClient() rest.Interface
	IpamdsGetter
}

// IpamdV1beta1Client is used to interact with features provided by the ipamd.uk8s.com group.
type IpamdV1beta1Client struct {
	restClient rest.Interface
}

func (c *IpamdV1beta1Client) Ipamds(namespace string) IpamdInterface {
	return newIpamds(c, namespace)
}

// NewForConfig creates a new IpamdV1beta1Client for the given config.
func NewForConfig(c *rest.Config) (*IpamdV1beta1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}
	return &IpamdV1beta1Client{client}, nil
}

// NewForConfigOrDie creates a new IpamdV1beta1Client for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *IpamdV1beta1Client {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// New creates a new IpamdV1beta1Client for the given RESTClient.
func New(c rest.Interface) *IpamdV1beta1Client {
	return &IpamdV1beta1Client{c}
}

func setConfigDefaults(config *rest.Config) error {
	gv := v1beta1.SchemeGroupVersion
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	return nil
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *IpamdV1beta1Client) RESTClient() rest.Interface {
	if c == nil {
		return nil
	}
	return c.restClient
}
