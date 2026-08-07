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
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/fsnotify/fsnotify"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
)

const (
	defaultTailLines   = 100
	tailBlockSize      = 1024
	tailForceCheckTime = time.Second
)

func newLogTailHandler(logPath string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		lines, err := parseTailLines(request)
		if err != nil {
			http.Error(writer, "lines must be a non-negative integer", http.StatusBadRequest)
			return
		}
		flusher, ok := writer.(http.Flusher)
		if !ok {
			http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		file, err := openLogTail(logPath, lines)
		if err != nil {
			ulog.Errorf("Open node log tail error: %+v", err)
			http.Error(writer, "node log unavailable", http.StatusServiceUnavailable)
			return
		}
		watcher, err := fsnotify.NewWatcher()
		if err == nil {
			err = watcher.Add(filepath.Dir(logPath))
		}
		if err != nil {
			if watcher != nil {
				err = errors.CombineErrors(err, errors.Wrap(watcher.Close(), "ipamd.newLogTailHandler close watcher"))
			}
			err = errors.CombineErrors(err, errors.Wrap(file.Close(), "ipamd.newLogTailHandler close log"))
			ulog.Errorf("Watch node log error: %+v", errors.Wrap(err, "ipamd.newLogTailHandler watch"))
			http.Error(writer, "node log unavailable", http.StatusServiceUnavailable)
			return
		}

		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusOK)
		flusher.Flush()

		if err := followLogTail(request.Context(), writer, flusher, file, watcher, logPath); err != nil && request.Context().Err() == nil {
			ulog.Errorf("Stream node log error: %+v", err)
		}
	}
}

func parseTailLines(request *http.Request) (int64, error) {
	value := request.URL.Query().Get("lines")
	if value == "" {
		return defaultTailLines, nil
	}
	lines, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.Wrap(err, "ipamd.parseTailLines parse lines")
	}
	if lines < 0 {
		return 0, errors.New("ipamd.parseTailLines lines must be non-negative")
	}
	return lines, nil
}

func openLogTail(path string, lines int64) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrapf(err, "ipamd.openLogTail open %s", errors.Safe(path))
	}
	offset, err := tailStartOffset(file, lines)
	if err == nil {
		_, err = file.Seek(offset, io.SeekStart)
	}
	if err != nil {
		return nil, errors.CombineErrors(err, errors.Wrap(file.Close(), "ipamd.openLogTail close log"))
	}
	return file, nil
}

// tailStartOffset is adapted from Kubernetes' CRI log reader.
func tailStartOffset(file io.ReadSeeker, lines int64) (int64, error) {
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, errors.Wrap(err, "ipamd.tailStartOffset seek end")
	}
	if lines == 0 {
		return end, nil
	}

	var offset, count int64
	buffer := make([]byte, tailBlockSize)
	for end > 0 && count <= lines {
		offset = end - tailBlockSize
		if offset < 0 {
			offset = 0
			buffer = make([]byte, end)
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return 0, errors.Wrap(err, "ipamd.tailStartOffset seek block")
		}
		if _, err := io.ReadFull(file, buffer); err != nil {
			return 0, errors.Wrap(err, "ipamd.tailStartOffset read block")
		}
		count += int64(bytes.Count(buffer, []byte{'\n'}))
		end = offset
	}
	for count > lines {
		index := bytes.IndexByte(buffer, '\n') + 1
		buffer = buffer[index:]
		offset += int64(index)
		count--
	}
	return offset, nil
}

func followLogTail(
	ctx context.Context,
	writer io.Writer,
	flusher http.Flusher,
	file *os.File,
	watcher *fsnotify.Watcher,
	path string,
) (returnErr error) {
	defer func() {
		returnErr = errors.CombineErrors(returnErr, errors.Wrap(file.Close(), "ipamd.followLogTail close log"))
		returnErr = errors.CombineErrors(returnErr, errors.Wrap(watcher.Close(), "ipamd.followLogTail close watcher"))
	}()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err == nil {
			written, err := writer.Write(line)
			if err != nil {
				return errors.Wrap(err, "ipamd.followLogTail write")
			}
			if written != len(line) {
				return errors.Wrap(io.ErrShortWrite, "ipamd.followLogTail write")
			}
			flusher.Flush()
			continue
		}
		if !errors.Is(err, io.EOF) {
			return errors.Wrap(err, "ipamd.followLogTail read")
		}
		if len(line) > 0 {
			if _, err := file.Seek(-int64(len(line)), io.SeekCurrent); err != nil {
				return errors.Wrap(err, "ipamd.followLogTail reset partial line")
			}
			reader.Reset(file)
		}

		recreated, err := waitLogChange(ctx, watcher, path)
		if err != nil || ctx.Err() != nil {
			return err
		}
		if !recreated {
			continue
		}

		replacement, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return errors.Wrapf(err, "ipamd.followLogTail reopen %s", errors.Safe(path))
		}
		closeErr := file.Close()
		file = replacement
		reader.Reset(file)
		if closeErr != nil {
			return errors.Wrap(closeErr, "ipamd.followLogTail close rotated log")
		}
	}
}

func waitLogChange(ctx context.Context, watcher *fsnotify.Watcher, path string) (bool, error) {
	path = filepath.Clean(path)
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case event, ok := <-watcher.Events:
			if !ok {
				return false, errors.New("ipamd.waitLogChange watcher closed")
			}
			if filepath.Clean(event.Name) != path {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				return true, nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Rename|fsnotify.Remove|fsnotify.Chmod) != 0 {
				return false, nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return false, errors.New("ipamd.waitLogChange watcher errors closed")
			}
			return false, errors.Wrap(err, "ipamd.waitLogChange watch")
		case <-time.After(tailForceCheckTime):
			return false, nil
		}
	}
}
