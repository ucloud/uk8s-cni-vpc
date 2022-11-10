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

package uapi

import (
	"fmt"
	"os"
	"strings"

	"github.com/ucloud/ucloud-sdk-go/external"
	"github.com/ucloud/ucloud-sdk-go/services/uhost"
	"github.com/ucloud/ucloud-sdk-go/services/uk8s"
	"github.com/ucloud/ucloud-sdk-go/services/unet"
	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/ucloud-sdk-go/ucloud/auth"
	"github.com/ucloud/ucloud-sdk-go/ucloud/metadata"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"
)

const (
	uApiDefaultEndpoint = "http://api.service.ucloud.cn"
	characterName       = "Uk8sServiceCharacter"
)

type ApiClient struct {
	unetClient  *unet.UNetClient
	vpcClient   *vpc.VPCClient
	uk8sClient  *uk8s.UK8SClient
	uhostClient *uhost.UHostClient
	instanceId  string
	zoneId      string
	vpcId       string
	subnetId    string
}

func (c *ApiClient) VPCClient() *vpc.VPCClient {
	return c.vpcClient
}
func (c *ApiClient) UNetClient() *unet.UNetClient {
	return c.unetClient
}
func (c *ApiClient) UK8SClient() *uk8s.UK8SClient {
	return c.uk8sClient
}
func (c *ApiClient) UHostClient() *uhost.UHostClient {
	return c.uhostClient
}

func (c *ApiClient) InstanceID() string {
	return c.instanceId
}

func (c *ApiClient) AvailabilityZone() string {
	return c.zoneId
}

func (c *ApiClient) VPCID() string {
	return c.vpcId
}
func (c *ApiClient) SubnetID() string {
	return c.subnetId
}

func LocalRegion() string {
	self, err := getMyself()
	if err != nil {
		return self.Region
	} else {
		return os.Getenv("UCLOUD_REGION_ID")
	}
}

func NewApiClient() (*ApiClient, error) {
	self, err := getMyself()
	if err != nil {
		return nil, fmt.Errorf("cannot get uapi metadata information, %v", err)
	}
	cfg := ucloud.NewConfig()
	cfg.Region = self.Region
	if strings.HasPrefix(self.InstanceId, "uhost") {
		cfg.ProjectId = self.UHost.ProjectId
	} else if strings.HasPrefix(self.InstanceId, "upm") {
		cfg.ProjectId = self.UPHost.ProjectId
	}

	if len(cfg.ProjectId) == 0 {
		cfg.ProjectId = os.Getenv("UCLOUD_PROJECT_ID")
	}

	cfg.UserAgent = version.CNIVersion
	// Don't set zone
	cfg.Zone = ""

	if len(os.Getenv("UCLOUD_API_ENDPOINT")) > 0 {
		cfg.BaseUrl = os.Getenv("UCLOUD_API_ENDPOINT")
	} else {
		cfg.BaseUrl = uApiDefaultEndpoint
	}

	var credential auth.Credential
	// In latest uk8s clusters, we removed api key in cm uk8sconfig
	c, err := external.LoadSTSConfig(external.AssumeRoleRequest{RoleName: characterName})
	if err != nil {
		logWarningf("Cannot get sts token for role %v, %v, credential will be invalid", characterName, err)
		// In past uk8s clusters, we injected api key in cm uk8sconfig
		credential.PublicKey = os.Getenv("UCLOUD_API_PUBKEY")
		credential.PrivateKey = os.Getenv("UCLOUD_API_PRIKEY")
		if len(credential.PublicKey) == 0 || len(credential.PrivateKey) == 0 {
			return nil, fmt.Errorf("cannot get uapi credential, sts call failed, %v and env UCLOUD_API_PUB/PRI/KEY empty", err)
		}
	} else {
		credential = *c.Credential()
	}
	vpcClient := vpc.NewClient(&cfg, &credential)
	vpcClient.AddResponseHandler(cniAPIReport)
	unetClient := unet.NewClient(&cfg, &credential)
	unetClient.AddResponseHandler(cniAPIReport)
	uk8sClient := uk8s.NewClient(&cfg, &credential)
	uk8sClient.AddResponseHandler(cniAPIReport)
	uhostClient := uhost.NewClient(&cfg, &credential)
	uhostClient.AddResponseHandler(cniAPIReport)

	uApi := &ApiClient{
		vpcClient:   vpcClient,
		unetClient:  unetClient,
		uk8sClient:  uk8sClient,
		uhostClient: uhostClient,
		instanceId:  self.InstanceId,
		zoneId:      self.AvailabilityZone,
	}
	// Get vpcId and subnetId
	if len(self.UHost.NetworkInterfaces) > 0 {
		uApi.vpcId = self.UHost.NetworkInterfaces[0].VpcId
		uApi.subnetId = self.UHost.NetworkInterfaces[0].SubnetId
	} else {
		uApi.vpcId = os.Getenv("UCLOUD_VPC_ID")
		uApi.subnetId = os.Getenv("UCLOUD_SUBNET_ID")
	}
	return uApi, nil
}

func getMyself() (*metadata.Metadata, error) {
	client := metadata.NewClient()
	md, err := client.GetInstanceIdentityDocument()
	if err != nil {
		logErrorf("cannot get instance metadata, %v", err)
		return nil, err
	} else {
		return &md, nil
	}
}
