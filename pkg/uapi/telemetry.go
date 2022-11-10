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
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/version"

	"github.com/google/uuid"

	"github.com/ucloud/ucloud-sdk-go/ucloud"
	uerr "github.com/ucloud/ucloud-sdk-go/ucloud/error"
	"github.com/ucloud/ucloud-sdk-go/ucloud/request"
	"github.com/ucloud/ucloud-sdk-go/ucloud/response"
)

const (
	telemetryMetricCNIAPIStatus  = "uk8s.cni.uapistatus"
	telemetryMetricCNIAPILatency = "uk8s.cni.uapilatency"

	telemetryAPIEndpoint = "http://umon.transfer.service.ucloud.cn/api/update"
	telemetryToken       = "07d985806dd54f109329b074ce876e9d"
)

type MetricValues struct {
	Metric     string  `json:"metric"`
	Endpoint   string  `json:"endpoint"`
	Tags       string  `json:"tags"`
	Value      float64 `json:"value"`
	Timestamp  int64   `json:"timestamp"`
	MetricType string  `json:"metrictype"`
}

type Report struct {
	SessionID    string         `json:"sessionid"`
	IP           string         `json:"ip"`
	MetricValues []MetricValues `json:"metricvalues"`
	Token        string         `json:"token"`
}

type ReportResponse struct {
	Total     int64
	Invalid   int64
	Message   string
	SessionId string
}

var (
	instanceId = ""
	subnetId   = ""
	vpcId      = ""
	zone       = ""
)

func init() {
	self, err := getMyself()
	if err == nil {
		instanceId = self.InstanceId
		if len(self.UHost.NetworkInterfaces) > 0 {
			subnetId = self.UHost.NetworkInterfaces[0].SubnetId
			vpcId = self.UHost.NetworkInterfaces[0].VpcId
		}
		zone = self.AvailabilityZone
	}
}

func buildBaseTags() (tags string) {
	segs := []string{}
	if len(instanceId) > 0 {
		segs = append(segs, "InstanceID="+instanceId)
	}
	if len(os.Getenv("UCLOUD_UK8S_CLUSTER_ID")) > 0 {
		segs = append(segs, "UK8SClusterID="+os.Getenv("UCLOUD_UK8S_CLUSTER_ID"))
	}
	if len(vpcId) > 0 {
		segs = append(segs, "VPCID="+vpcId)
	}
	if len(subnetId) > 0 {
		segs = append(segs, "SubnetID="+subnetId)
	}
	if len(zone) > 0 {
		segs = append(segs, "AZ="+zone)
	}
	if len(os.Getenv("KUBE_NODE_NAME")) > 0 {
		segs = append(segs, "KubeNode="+os.Getenv("KUBE_NODE_NAME"))
	}
	if len(version.CNIVersion) > 0 {
		segs = append(segs, "CNIVersion="+version.CNIVersion)
	}

	return strings.Join(segs, ",")
}

func (mv *MetricValues) addTag(key, val string) {
	mv.Tags += fmt.Sprintf(",%v=%v", key, val)
}

func buildMetricValue(metric, endpoint, metricType string, value float64) MetricValues {
	mv := MetricValues{
		Metric:     metric,
		Endpoint:   endpoint,
		MetricType: metricType,
		Value:      value,
		Tags:       buildBaseTags(),
		Timestamp:  time.Now().Unix(),
	}
	return mv
}

func buildReportData(mvs []MetricValues) *Report {
	SessionID, err := uuid.NewUUID()
	if err != nil {
		logErrorf("Cannot generate a new UUID, %v", err)
	}

	IP, err := getNodeIPAddress()
	if err != nil {
		logErrorf("Cannot get node master network interface ip, %v", err)
	}

	r := &Report{
		SessionID:    SessionID.String(),
		IP:           IP,
		Token:        telemetryToken,
		MetricValues: mvs,
	}
	return r
}

func telemetryReport(rp *Report) error {
	content, err := json.Marshal(*rp)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", telemetryAPIEndpoint, bytes.NewBuffer(content))
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req.Header.Set("X-SessionId", rp.SessionID)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry report[request id:%v] response status %v", rp.SessionID, resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var rr ReportResponse
	err = json.Unmarshal(body, &rr)
	if err != nil {
		return err
	}
	return nil
}

func cniAPIReport(c *ucloud.Client, req request.Common, rsp response.Common, resperr error) (response.Common, error) {
	var subject string
	if len(os.Getenv("UCLOUD_UK8S_CLUSTER_ID")) == 0 {
		subject = "none"
	} else {
		subject = os.Getenv("UCLOUD_UK8S_CLUSTER_ID")
	}
	mv := make([]MetricValues, 0)

	var statusValue int = 0

	if e, ok := resperr.(uerr.Error); resperr != nil && ok {
		switch e.Name() {
		case uerr.ErrRetCode:
			statusValue = e.Code()
		case uerr.ErrHTTPStatus:
			statusValue = -e.StatusCode()
		default:
			statusValue = -1
		}
	}

	// Status
	mvs := buildMetricValue(telemetryMetricCNIAPIStatus, subject, "gauge", float64(statusValue))
	// Latency
	latency := time.Since(req.GetRequestTime())
	mvl := buildMetricValue(telemetryMetricCNIAPILatency, subject, "gauge", float64(int64(latency)/(1000000)))

	if len(req.GetAction()) > 0 {
		mvs.addTag("Action", req.GetAction())
		mvl.addTag("Action", req.GetAction())
	}

	mv = append(mv, mvs)
	mv = append(mv, mvl)

	rp := buildReportData(mv)
	err := telemetryReport(rp)
	if err != nil {
		logErrorf("Failed to do telemetry report for %+v, %v", rp, err)
	}
	return rsp, resperr
}

// Get node master network interface ip address
func getNodeIPAddress() (string, error) {
	i, e := net.InterfaceByIndex(2)
	if e != nil {
		return "", e
	}
	a, e := i.Addrs()
	if e != nil || len(a) == 0 {
		return "", e
	}
	acidr := a[0].String()
	ip, _, e := net.ParseCIDR(acidr)
	return ip.String(), e
}
