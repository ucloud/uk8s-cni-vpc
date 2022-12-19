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

package kubeclient

import (
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	crdclientset "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/clientset/versioned"
)

var (
	client *kubernetes.Clientset
	crd    *crdclientset.Clientset

	clientOnce sync.Once
	crdOnce    sync.Once

	config     *rest.Config
	configOnce sync.Once
)

func GetConfig() (*rest.Config, error) {
	var err error
	configOnce.Do(func() {
		config, err = rest.InClusterConfig()
		if err != nil {
			err = fmt.Errorf("failed to generate kubernetes client config: %v", err)
			return
		}
	})
	return config, err
}

func Get() (*kubernetes.Clientset, error) {
	var err error
	clientOnce.Do(func() {
		var cfg *rest.Config
		cfg, err = GetConfig()
		if err != nil {
			return
		}
		client, err = kubernetes.NewForConfig(cfg)
		if err != nil {
			err = fmt.Errorf("failed to generate kubernetes client: %v", err)
			return
		}
	})
	return client, err
}

func GetCRD() (*crdclientset.Clientset, error) {
	var err error
	crdOnce.Do(func() {
		var cfg *rest.Config
		cfg, err = GetConfig()
		if err != nil {
			return
		}
		crd, err = crdclientset.NewForConfig(cfg)
		if err != nil {
			err = fmt.Errorf("failed to generate crd client: %v", err)
			return
		}
	})
	return crd, err
}
