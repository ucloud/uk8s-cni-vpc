package common

import (
	"github.com/ucloud/ucloud-sdk-go/ucloud"
)

type SecondaryIP struct {
	IP      string
	Mask    string
	Gateway string
}

func ListSecondaryIP() ([]*SecondaryIP, error) {
	node := Node()

	vpcClient, err := UAPI().VPCClient()
	if err != nil {
		return nil, err
	}
	req := vpcClient.NewDescribeSecondaryIpRequest()
	req.Region = ucloud.String(node.Region)
	req.SubnetId = ucloud.String(node.SubnetID)
	req.VPCId = ucloud.String(node.VPCID)
	req.Mac = ucloud.String(node.MAC)

	resp, err := vpcClient.DescribeSecondaryIp(req)
	if err != nil {
		return nil, err
	}

	ips := make([]*SecondaryIP, len(resp.DataSet))
	for i, data := range resp.DataSet {
		ips[i] = &SecondaryIP{
			IP:      data.Ip,
			Mask:    data.Mask,
			Gateway: data.Gateway,
		}
	}

	return ips, nil
}
