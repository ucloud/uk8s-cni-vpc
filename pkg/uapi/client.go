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
	"github.com/ucloud/ucloud-sdk-go/ucloud/config"
	"github.com/ucloud/ucloud-sdk-go/ucloud/metadata"
	"github.com/ucloud/uk8s-cni-vpc/pkg/version"
)

const (
	uApiDefaultEndpoint = "http://api.service.ucloud.cn"
	characterName       = "Uk8sServiceCharacter"
)

type ApiClient struct {
	instanceId string
	zoneId     string
	vpcId      string
	subnetId   string

	cfg *config.Config
}

func (c *ApiClient) VPCClient() (*vpc.VPCClient, error) {
	cre, err := c.CreateCredential()
	if err != nil {
		return nil, err
	}
	return vpc.NewClient(c.cfg, cre), nil
}

func (c *ApiClient) UNetClient() (*unet.UNetClient, error) {
	cre, err := c.CreateCredential()
	if err != nil {
		return nil, err
	}
	return unet.NewClient(c.cfg, cre), nil
}

func (c *ApiClient) UK8SClient() (*uk8s.UK8SClient, error) {
	cre, err := c.CreateCredential()
	if err != nil {
		return nil, err
	}
	return uk8s.NewClient(c.cfg, cre), nil
}

func (c *ApiClient) UHostClient() (*uhost.UHostClient, error) {
	cre, err := c.CreateCredential()
	if err != nil {
		return nil, err
	}
	return uhost.NewClient(c.cfg, cre), nil
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

func (c *ApiClient) CreateCredential() (*auth.Credential, error) {
	var credential auth.Credential
	// In latest uk8s clusters, we removed api key in cm uk8sconfig
	config, err := external.LoadSTSConfig(external.AssumeRoleRequest{RoleName: characterName})
	if err != nil {
		logWarningf("Cannot get sts token for role %v, %v, credential will be invalid", characterName, err)
		// In past uk8s clusters, we injected api key in cm uk8sconfig
		credential.PublicKey = os.Getenv("UCLOUD_API_PUBKEY")
		credential.PrivateKey = os.Getenv("UCLOUD_API_PRIKEY")
		if len(credential.PublicKey) == 0 || len(credential.PrivateKey) == 0 {
			return nil, fmt.Errorf("cannot get uapi credential, sts call failed, %v and env UCLOUD_API_PUB/PRI/KEY empty", err)
		}
	} else {
		credential = *config.Credential()
	}
	return &credential, nil
}

func LocalRegion() string {
	self, err := getMyself()
	if err != nil {
		return self.Region
	} else {
		return os.Getenv("UCLOUD_REGION_ID")
	}
}

func NewClient() (*ApiClient, error) {
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

	uApi := &ApiClient{
		instanceId: self.InstanceId,
		zoneId:     self.AvailabilityZone,
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
