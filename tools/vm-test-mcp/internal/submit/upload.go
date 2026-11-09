package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func uploadManifest(ctx context.Context, client *http.Client, o Options, cache *exportCache, m wire.UploadManifest) error {
	base := strings.TrimRight(o.CacheURL, "/")
	req, err := http.NewRequestWithContext(ctx, "PUT", base+"/manifest", bytes.NewReader(wire.JSON(m)))
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload manifest: %w", wire.Safe(err))
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("upload manifest returned HTTP %d", response.StatusCode)
	}
	objects := make(map[string]wire.UploadObject, len(m.Objects))
	var totalBytes, missingBytes int64
	for _, object := range m.Objects {
		objects[object.Name] = object
		totalBytes += object.Size
	}
	var missing []wire.UploadObject
	cursor := ""
	for {
		req, err = http.NewRequestWithContext(ctx, "GET", base+"/missing?cursor="+url.QueryEscape(cursor), nil)
		if err != nil {
			return err
		}
		response, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("query missing input objects: %w", wire.Safe(err))
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, wire.MaxJSON+1))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode != http.StatusOK || len(data) > wire.MaxJSON {
			return fmt.Errorf("missing input inventory returned HTTP %d or exceeded its limit", response.StatusCode)
		}
		var page struct {
			Items []wire.UploadObject `json:"items"`
			Next  string              `json:"next_cursor"`
		}
		if err = json.Unmarshal(data, &page); err != nil {
			return err
		}
		if len(page.Items) == 0 {
			break
		}
		for _, obj := range page.Items {
			if expected, ok := objects[obj.Name]; !ok || expected != obj || obj.Name <= cursor {
				return fmt.Errorf("missing input inventory differs from the saved manifest")
			}
			missing = append(missing, obj)
			missingBytes += obj.Size
			cursor = obj.Name
		}
		if page.Next != cursor {
			return fmt.Errorf("missing input cursor did not match its page")
		}
	}
	fmt.Fprintf(o.Progress, "Upload: %d/%d objects, %d/%d bytes missing; concurrency 4\n", len(missing), len(m.Objects), missingBytes, totalBytes)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan wire.UploadObject)
	var wg sync.WaitGroup
	var first error
	var once sync.Once
	var count, bytesSent atomic.Int64
	started := time.Now()
	for range min(4, len(missing)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for obj := range jobs {
				if err := uploadCacheObject(ctx, client, base, obj.Name, filepath.Join(cache.root.Path, "files", obj.Name)); err != nil {
					once.Do(func() { first = err; cancel() })
					return
				}
				count.Add(1)
				bytesSent.Add(obj.Size)
			}
		}()
	}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		defer close(jobs)
		for _, obj := range missing {
			select {
			case jobs <- obj:
			case <-ctx.Done():
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fmt.Fprintf(o.Progress, "Uploaded: %d/%d objects, %d/%d bytes; elapsed %s\n", count.Load(), len(missing), bytesSent.Load(), missingBytes, time.Since(started).Round(time.Second))
		case <-done:
			<-sent
			if first != nil {
				return first
			}
			return ctx.Err()
		}
	}
}
