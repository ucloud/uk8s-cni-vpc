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

package main

import (
	"fmt"
	"time"

	v1beta1 "github.com/ucloud/uk8s-cni-vpc/pkg/apis/vipcontroller/v1beta1"
	crdclientset "github.com/ucloud/uk8s-cni-vpc/pkg/generated/clientset/versioned"
	crdinformers "github.com/ucloud/uk8s-cni-vpc/pkg/generated/informers/externalversions/vipcontroller/v1beta1"
	crdlisters "github.com/ucloud/uk8s-cni-vpc/pkg/generated/listers/vipcontroller/v1beta1"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type VipController struct {
	vipClient        crdclientset.Interface
	kubeClient       clientset.Interface
	vipLister        crdlisters.VpcIpClaimLister
	vipSynced        cache.InformerSynced
	deleteQueue      workqueue.RateLimitingInterface
	workerLoopPeriod time.Duration
}

func NewVipController(vipInformer crdinformers.VpcIpClaimInformer, vpcIpClaimclientset crdclientset.Interface, kubeClient clientset.Interface) *VipController {
	c := &VipController{
		vipClient:        vpcIpClaimclientset,
		kubeClient:       kubeClient,
		deleteQueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "vpcipclaims"),
		workerLoopPeriod: 50 * time.Millisecond,
	}

	vipInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(del interface{}) {
			vip := del.(*v1beta1.VpcIpClaim)
			c.deleteQueue.Add(vip)
		},
	})

	c.vipLister = vipInformer.Lister()
	c.vipSynced = vipInformer.Informer().HasSynced

	return c
}

func (c *VipController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.deleteQueue.ShutDown()

	klog.Infof("Starting vpcipclaim controller")
	defer klog.Warningf("Shutting down vpcipclaim controller")

	if !cache.WaitForNamedCacheSync("vpcipclaims", stopCh, c.vipSynced) {
		klog.Errorf("Cannot finish WaitForNamedCacheSync for vpcipclaims")
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(c.worker, c.workerLoopPeriod, stopCh)
	}

	<-stopCh
}

func (c *VipController) worker() {
	for {
		c.processDelete()
	}
}

func (c *VipController) processDelete() {
	s, quit := c.deleteQueue.Get()
	if quit {
		klog.Warningf("Empty deleteQueue")
		return
	}
	defer c.deleteQueue.Forget(s)
	vip := s.(*v1beta1.VpcIpClaim)
	if err := c.onVipDelete(vip); err == nil {
		c.deleteQueue.Done(s)
	}
}

func (c *VipController) onVipDelete(vip *v1beta1.VpcIpClaim) error {
	notRunning, err := ensureStaticIpPodNotRunning(c.kubeClient, vip.Namespace, vip.Name)
	if err == nil && notRunning {
		klog.Infof("VpcIpClaim %s/%s %s is deleted, now release secondary ip", vip.Namespace, vip.Name, vip.Spec.Ip)
		return releaseVPCIp(*vip)
	} else {
		return fmt.Errorf("cannot delete vip %s/%s %s, podNotRunning %v, get pod err %v", vip.Namespace, vip.Name, vip.Spec.Ip, notRunning, err)
	}
}
