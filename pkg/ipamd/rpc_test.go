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

package ipamd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type cniVersionTestServer struct {
	rpc.UnimplementedCNIIpamServer
	path string
}

func (s *cniVersionTestServer) GetCNIVersion(
	ctx context.Context,
	req *rpc.GetCNIVersionRequest,
) (*rpc.GetCNIVersionResponse, error) {
	return getCNIVersionAtPath(ctx, s.path)
}

func TestGetCNIVersion(t *testing.T) {
	t.Run("returns the installed CNI version", func(t *testing.T) {
		path := writeTestCNIBinary(t, "printf 'ucloud-uk8s-cnivpc version 2.0.4\\n' >&2\n")
		client := newCNIVersionTestClient(t, path)

		response, err := client.GetCNIVersion(context.Background(), &rpc.GetCNIVersionRequest{})
		if err != nil {
			t.Fatalf("GetCNIVersion() error = %v", err)
		}
		if response.GetCode() != rpc.CNIErrorCode_CNISuccess {
			t.Errorf("GetCNIVersion() code = %s, want %s", response.GetCode(), rpc.CNIErrorCode_CNISuccess)
		}
		if response.GetVersion() != "2.0.4" {
			t.Errorf("GetCNIVersion() version = %q, want %q", response.GetVersion(), "2.0.4")
		}
		if response.GetPath() != path {
			t.Errorf("GetCNIVersion() path = %q, want %q", response.GetPath(), path)
		}
		if response.GetSize() <= 0 {
			t.Errorf("GetCNIVersion() size = %d, want a positive value", response.GetSize())
		}
	})

	t.Run("returns NotFound when the CNI binary is absent", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cnivpc")
		client := newCNIVersionTestClient(t, path)

		response, err := client.GetCNIVersion(context.Background(), &rpc.GetCNIVersionRequest{})
		if response != nil {
			t.Fatalf("GetCNIVersion() response = %+v, want nil", response)
		}
		if got := status.Code(err); got != codes.NotFound {
			t.Errorf("GetCNIVersion() code = %s, want %s", got, codes.NotFound)
		}
	})

	t.Run("returns Internal when the version output cannot be parsed", func(t *testing.T) {
		path := writeTestCNIBinary(t, "printf 'unexpected output\\n' >&2\n")
		client := newCNIVersionTestClient(t, path)

		response, err := client.GetCNIVersion(context.Background(), &rpc.GetCNIVersionRequest{})
		if response != nil {
			t.Fatalf("GetCNIVersion() response = %+v, want nil", response)
		}
		if got := status.Code(err); got != codes.Internal {
			t.Errorf("GetCNIVersion() code = %s, want %s", got, codes.Internal)
		}
	})
}

func TestParseCNIVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name:   "parses a stable release",
			output: "ucloud-uk8s-cnivpc version 2.0.2\n",
			want:   "2.0.2",
		},
		{
			name:   "parses a historical release",
			output: "ucloud-uk8s-cnivpc version 1.0.0\n",
			want:   "1.0.0",
		},
		{
			name:   "parses a prerelease",
			output: "ucloud-uk8s-cnivpc version 2.0.0-beta.3\n",
			want:   "2.0.0-beta.3",
		},
		{
			name:    "rejects unexpected output",
			output:  "2.0.2",
			wantErr: true,
		},
		{
			name:    "rejects an empty version",
			output:  "ucloud-uk8s-cnivpc version ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCNIVersion(tt.output)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCNIVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseCNIVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func newCNIVersionTestClient(t *testing.T, path string) rpc.CNIIpamClient {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	rpc.RegisterCNIIpamServer(server, &cniVersionTestServer{path: path})
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, err := grpc.DialContext(
		ctx,
		"bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.DialContext() error = %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close gRPC connection: %v", err)
		}
	})
	return rpc.NewCNIIpamClient(conn)
}

func writeTestCNIBinary(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cnivpc")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write test CNI binary: %v", err)
	}
	return path
}
