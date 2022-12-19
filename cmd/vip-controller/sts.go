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
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	v1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/vipcontroller/v1beta1"
	crdclientset "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/clientset/versioned"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformer "k8s.io/client-go/informers/apps/v1"
	clientset "k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics/prometheus/ratelimiter"
	"k8s.io/klog/v2"
)

const stsEnableStaticIpNote = "network.beta.kubernetes.io/ucloud-statefulset-static-ip"

type StsController struct {
	client           clientset.Interface
	vipClient        crdclientset.Interface
	stsLister        appslisters.StatefulSetLister
	stsSynced        cache.InformerSynced
	updateQueue      workqueue.RateLimitingInterface
	deleteQueue      workqueue.RateLimitingInterface
	workerLoopPeriod time.Duration
}

func NewStsController(stsInformer appsinformer.StatefulSetInformer, client clientset.Interface, vpcIpClaimclientset crdclientset.Interface) *StsController {
	if client != nil && client.CoreV1().RESTClient().GetRateLimiter() != nil {
		ratelimiter.RegisterMetricAndTrackRateLimiterUsage("", client.CoreV1().RESTClient().GetRateLimiter())
	}
	sts := &StsController{
		client:           client,
		vipClient:        vpcIpClaimclientset,
		updateQueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "statefulsets"),
		deleteQueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "statefulsets"),
		workerLoopPeriod: 10 * time.Millisecond,
	}

	stsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, now interface{}) {
			s := now.(*appsv1.StatefulSet)
			if needHandleVpcIpClaim(s) {
				sts.updateQueue.AddAfter(s.Namespace+"/"+s.Name, 30*time.Second)
			}
		},
		DeleteFunc: func(del interface{}) {
			s := del.(*appsv1.StatefulSet)
			if needHandleVpcIpClaim(s) {
				klog.Infof("Sts %s/%s deleted", s.Namespace, s.Name)
				sts.deleteQueue.Add(s)
			}
		},
	})

	sts.stsLister = stsInformer.Lister()
	sts.stsSynced = stsInformer.Informer().HasSynced

	return sts
}

func (c *StsController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.updateQueue.ShutDown()
	defer c.deleteQueue.ShutDown()

	klog.Infof("Starting statefulset controller")
	defer klog.Infof("Shutting down statefulset controller")

	if !cache.WaitForNamedCacheSync("statefulsets", stopCh, c.stsSynced) {
		klog.Errorf("Cannot finish WaitForNamedCacheSync for statefulsets")
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(c.worker, c.workerLoopPeriod, stopCh)
	}

	<-stopCh
}

func (c *StsController) worker() {
	go func() {
		for {
			c.processUpdate()
		}
	}()

	for {
		c.processDelete()
	}
}

func (c *StsController) processUpdate() {
	s, quit := c.updateQueue.Get()
	if quit {
		klog.Warningf("Empty updateQueue")
		return
	}

	defer c.updateQueue.Forget(s)
	if err := c.onStsUpdate(s.(string)); err == nil {
		c.updateQueue.Done(s)
	}
}

func (c *StsController) onStsUpdate(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	vipList, err := c.vipClient.VipcontrollerV1beta1().VpcIpClaims(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("owner-statefulset=%s", name)})
	if err != nil {
		klog.Errorf("Cannot list vpcipclaims owned by %s/%s, %v", namespace, name, err)
		return err
	}

	sts, err := c.stsLister.StatefulSets(namespace).Get(name)
	if err != nil {
		klog.Errorf("Cannot get statefulset %s/%s, %v", namespace, name, err)
		// Sts may being deleted afterwards
		if k8serr.IsNotFound(err) {
			return nil
		}
		return err
	}

	for i, _ := range vipList.Items {
		vip := &(vipList.Items[i])
		segs := strings.Split(vip.Name, "-")
		if len(segs) < 1 {
			return fmt.Errorf("cannot split vpcipclaim name %s to get index number", vip.Name)
		}
		idxStr := segs[len(segs)-1]
		if idx, err := strconv.Atoi(idxStr); err == nil {
			if idx > int(*sts.Spec.Replicas)-1 {
				vip := &(vipList.Items[i])
				if vip.Status.Attached == true {
					notRunning, err := ensureStaticIpPodNotRunning(c.client, vip.Namespace, vip.Name)
					if err == nil && notRunning {
						c.markVPCIpClaimDetached(vip)
					}
				}
			}
		} else {
			klog.Errorf("Cannot parse idx number %s, %v", vip.Name, err)
			return err
		}
	}

	return nil
}

func (c *StsController) processDelete() {
	s, quit := c.deleteQueue.Get()
	if quit {
		klog.Warningf("Empty deleteQueue")
		return
	}
	defer c.deleteQueue.Forget(s)
	sts := s.(*appsv1.StatefulSet)
	if err := c.onStsDelete(sts); err == nil {
		c.deleteQueue.Done(s)
	}
}

func (c *StsController) onStsDelete(sts *appsv1.StatefulSet) error {
	klog.Infof("Mark all vpcipclaims of sts %s/%s detached", sts.Namespace, sts.Name)
	vipList, err := c.vipClient.VipcontrollerV1beta1().VpcIpClaims(sts.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("owner-statefulset=%s", sts.Name)})
	if err != nil {
		klog.Errorf("Cannot list vpcipclaims owned by %s/%s, %v", sts.Namespace, sts.Name, err)
		return err
	}

	// Mark detach
	for idx, _ := range vipList.Items {
		c.markVPCIpClaimDetached(&(vipList.Items[idx]))
	}

	return nil
}

func (c *StsController) markVPCIpClaimDetached(vip *v1beta1.VpcIpClaim) error {
	klog.Infof("Mark vpcipclaim %s/%s %s detached", vip.Namespace, vip.Name, vip.Spec.Ip)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		vip, localErr := c.vipClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Get(context.TODO(), vip.Name, metav1.GetOptions{})
		if localErr != nil {
			klog.Infof("Cannot get latest vpcipclaim %s/%s, %v", vip.Namespace, vip.Name, localErr)
			return localErr
		}
		if vip.Labels != nil {
			vip.Labels["attached"] = "false"
		} else {
			vip.Labels = map[string]string{"attached": "false"}
		}
		vip.ObjectMeta.Finalizers = nil
		vip.Status.Attached = false
		vip.Status.SandboxId = ""
		vip.Status.LastDetachTime = time.Now().Format("2006-01-02 15:04:05")
		_, localErr = c.vipClient.VipcontrollerV1beta1().VpcIpClaims(vip.Namespace).Update(context.TODO(), vip, metav1.UpdateOptions{})
		return localErr
	})
	if err != nil {
		klog.Errorf("Mark crd vpcipclaim %s/%s as detached failed, %v", vip.Namespace, vip.Name, err)
	}
	return err
}

func needHandleVpcIpClaim(s *appsv1.StatefulSet) bool {
	if val, found := s.Spec.Template.ObjectMeta.Annotations[stsEnableStaticIpNote]; found {
		if strings.ToLower(val) == "true" {
			return true
		}
	}
	return false
}

func ensureStaticIpPodNotRunning(kubeClient clientset.Interface, namespace, name string) (bool, error) {
	pod, err := kubeClient.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		// Make sure the pod no longer exists
		if k8serr.IsNotFound(err) {
			return true, nil
		} else {
			klog.Errorf("Get pod %s/%s failed, %v", namespace, name, err)
			return false, err
		}
	}

	// Check if owned by statefulSet
	ownedByStatefulSet := false
	for _, of := range pod.OwnerReferences {
		if of.Kind == "StatefulSet" {
			ownedByStatefulSet = true
			break
		}
	}
	if !ownedByStatefulSet {
		return true, nil
	}

	// Check if need ip preservation by annotation
	if val, found := pod.Annotations[stsEnableStaticIpNote]; found {
		if strings.ToLower(val) != "true" {
			return true, nil
		}
	}

	if pod.Status.Phase != v1.PodRunning {
		return true, nil
	}
	klog.Infof("Pod %s/%s phase is %v, ip preservation enabled", namespace, name, pod.Status.Phase)
	return false, nil
}
