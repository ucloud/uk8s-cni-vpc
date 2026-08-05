// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package ipamd

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
)

const (
	cniBinaryPath            = "/opt/cni/bin/cnivpc"
	cniVersionPrefix         = "ucloud-uk8s-cnivpc version "
	versionListenAddress     = "127.0.0.1:7313"
	versionEndpointPath      = "/version"
	nodeLogPath              = "/host/var/log/cnivpc.log"
	logTailEndpointPath      = "/logs/tail"
	versionLoadTimeout       = 5 * time.Second
	versionReadHeaderTimeout = 2 * time.Second
	versionIdleTimeout       = 30 * time.Second
)

type versionResponse struct {
	Version string `json:"version"`
}

func loadCNIVersion(ctx context.Context, path string) (string, error) {
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return "", errors.Wrapf(err, "ipamd.loadCNIVersion execute %s", errors.Safe(path))
	}

	version, err := parseCNIVersion(string(output))
	if err != nil {
		return "", err
	}
	return version, nil
}

func parseCNIVersion(output string) (string, error) {
	output = strings.TrimSpace(output)
	version, found := strings.CutPrefix(output, cniVersionPrefix)
	if !found || strings.TrimSpace(version) == "" {
		return "", errors.Errorf("ipamd.parseCNIVersion unexpected output %q", errors.Safe(output))
	}
	return strings.TrimSpace(version), nil
}

func newVersionHTTPServer(version string, versionErr error) *http.Server {
	return newVersionHTTPServerWithLog(version, versionErr, nodeLogPath)
}

func newVersionHTTPServerWithLog(version string, versionErr error, logPath string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+versionEndpointPath, func(writer http.ResponseWriter, _ *http.Request) {
		if versionErr != nil {
			http.Error(writer, "CNI version unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if err := json.NewEncoder(writer).Encode(versionResponse{Version: version}); err != nil {
			ulog.Errorf("Write CNI version response error: %+v", err)
		}
	})
	mux.HandleFunc("GET "+logTailEndpointPath, newLogTailHandler(logPath))

	return &http.Server{
		Addr:              versionListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: versionReadHeaderTimeout,
		IdleTimeout:       versionIdleTimeout,
	}
}
