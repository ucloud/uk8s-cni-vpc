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
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionHTTPServer(t *testing.T) {
	path := writeTestCNIBinary(t, "printf 'ucloud-uk8s-cnivpc version 2.0.5-rc2\\n' >&2\n")
	version, err := loadCNIVersion(context.Background(), path)
	if err != nil {
		t.Fatalf("loadCNIVersion() error = %v", err)
	}

	handler := newVersionHTTPServer(version, nil).Handler

	t.Run("returns the cached CNI version", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, versionEndpointPath, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("GET /version status = %d, want %d", response.Code, http.StatusOK)
		}
		if got := response.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("GET /version Content-Type = %q, want application/json", got)
		}

		var body versionResponse
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("decode GET /version response: %v", err)
		}
		if body.Version != "2.0.5-rc2" {
			t.Errorf("GET /version version = %q, want %q", body.Version, "2.0.5-rc2")
		}
	})

	t.Run("rejects mutation methods", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, versionEndpointPath, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST /version status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("does not expose other resources", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Errorf("GET / status = %d, want %d", response.Code, http.StatusNotFound)
		}
	})

	t.Run("reports an unavailable cached version", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, versionEndpointPath, nil)
		response := httptest.NewRecorder()
		newVersionHTTPServer("", context.DeadlineExceeded).Handler.ServeHTTP(response, request)

		if response.Code != http.StatusServiceUnavailable {
			t.Errorf(
				"GET /version status = %d, want %d",
				response.Code,
				http.StatusServiceUnavailable,
			)
		}
	})
}

func TestLogTailHTTPServer(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cnivpc.log")
	if err := os.WriteFile(logPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write node log: %v", err)
	}

	server := httptest.NewServer(newVersionHTTPServerWithLog("test", nil, logPath).Handler)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL+logTailEndpointPath+"?lines=2",
		nil,
	)
	if err != nil {
		t.Fatalf("create log tail request: %v", err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", logTailEndpointPath, err)
	}
	t.Cleanup(func() {
		response.Body.Close()
	})

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", logTailEndpointPath, response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("GET %s Content-Type = %q, want text/plain; charset=utf-8", logTailEndpointPath, got)
	}

	reader := bufio.NewReader(response.Body)
	assertStreamLine(t, reader, "second\n")
	assertStreamLine(t, reader, "third\n")

	appendTestLog(t, logPath, "fourth\n")
	assertStreamLine(t, reader, "fourth\n")

	if err := os.Rename(logPath, logPath+".1"); err != nil {
		t.Fatalf("rotate node log: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("after rotation\n"), 0o644); err != nil {
		t.Fatalf("write rotated node log: %v", err)
	}
	assertStreamLine(t, reader, "after rotation\n")

	cancel()
}

func TestLogTailHTTPServerErrors(t *testing.T) {
	handler := newVersionHTTPServerWithLog("test", nil, filepath.Join(t.TempDir(), "missing.log")).Handler

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{
			name:       "rejects an invalid line count",
			method:     http.MethodGet,
			target:     logTailEndpointPath + "?lines=all",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "reports an unavailable node log",
			method:     http.MethodGet,
			target:     logTailEndpointPath,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "rejects mutation methods",
			method:     http.MethodPost,
			target:     logTailEndpointPath,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Errorf("%s %s status = %d, want %d", test.method, test.target, response.Code, test.wantStatus)
			}
		})
	}
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

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCNIVersion(test.output)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseCNIVersion() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("parseCNIVersion() = %q, want %q", got, test.want)
			}
		})
	}
}

func writeTestCNIBinary(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cnivpc")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write test CNI binary: %v", err)
	}
	return path
}

func appendTestLog(t *testing.T, path, content string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open node log for append: %v", err)
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		t.Fatalf("append node log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close appended node log: %v", err)
	}
}

func assertStreamLine(t *testing.T, reader *bufio.Reader, want string) {
	t.Helper()

	got, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read streamed log line: %v", err)
	}
	if got != want {
		t.Fatalf("streamed log line = %q, want %q", got, want)
	}
}
