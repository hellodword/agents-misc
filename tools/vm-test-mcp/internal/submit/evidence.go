package submit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type evidenceFile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Local  string `json:"local,omitempty"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}

type evidenceDownload struct {
	State    string         `json:"state"`
	Manifest string         `json:"manifest"`
	Files    []evidenceFile `json:"files"`
}

func collectEvidence(ctx context.Context, session *mcp.ClientSession, root *safefs.Root, id, mode string, progress io.Writer) (evidenceDownload, error) {
	d := evidenceDownload{State: "pending", Manifest: "evidence.json", Files: []evidenceFile{}}
	var cursor int64
	seen := map[string]bool{}
	for {
		var page struct {
			Items []evidenceFile `json:"items"`
			Next  int64          `json:"next_cursor"`
		}
		if err := call(ctx, session, "evidence_list", map[string]any{"execution_id": id, "cursor": cursor, "limit": 200}, &page); err != nil {
			return d, err
		}
		for _, f := range page.Items {
			hash, err := hex.DecodeString(f.SHA256)
			if !wire.IDPattern.MatchString(f.ID) || err != nil || len(hash) != sha256.Size || f.Size < 0 || seen[f.ID] {
				return d, fmt.Errorf("invalid evidence inventory")
			}
			seen[f.ID] = true
			f.State = "listed"
			d.Files = append(d.Files, f)
		}
		if len(page.Items) == 0 {
			break
		}
		if page.Next <= cursor {
			return d, fmt.Errorf("evidence inventory cursor did not advance")
		}
		cursor = page.Next
	}
	save := func() error {
		_, _, err := root.Write(d.Manifest, bytes.NewReader(wire.JSON(d)), 32<<20)
		return err
	}
	if mode == "manifest" {
		d.State = "manifest_only"
		return d, save()
	}
	if err := save(); err != nil {
		return d, err
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("evidence redirects are not allowed") }}
	var first error
	var complete int
	for i := range d.Files {
		f := &d.Files[i]
		f.Local = "artifacts/" + f.ID
		err := downloadEvidence(ctx, session, client, root, f)
		if err != nil {
			f.State, f.Error = "pending", err.Error()
			if first == nil {
				first = err
			}
		} else {
			f.State = "complete"
			complete++
		}
		if err := save(); err != nil {
			return d, err
		}
		if ctx.Err() != nil {
			break
		}
	}
	d.State = "complete"
	if complete != len(d.Files) {
		d.State = "pending"
	}
	fmt.Fprintf(progress, "Evidence: %d/%d files retained; manifest %s\n", complete, len(d.Files), d.Manifest)
	if err := save(); err != nil {
		return d, err
	}
	return d, first
}

func matchesEvidence(root *safefs.Root, name string, item *evidenceFile) bool {
	f, err := root.Regular(name)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, item.Size+1))
	return err == nil && n == item.Size && hex.EncodeToString(h.Sum(nil)) == item.SHA256
}

func downloadEvidence(ctx context.Context, session *mcp.ClientSession, client *http.Client, root *safefs.Root, item *evidenceFile) error {
	if matchesEvidence(root, item.Local, item) {
		return nil
	}
	if err := root.MkdirAll("artifacts", 0700); err != nil {
		return err
	}
	partial := item.Local + ".partial"
	if matchesEvidence(root, partial, item) {
		return root.Publish(partial, item.Local)
	}
	f, err := root.OpenFile(partial, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("evidence destination must be an ordinary file")
	}
	offset := st.Size()
	if offset >= item.Size {
		if err := f.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	var credential struct {
		URL    string `json:"url"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	if err := call(ctx, session, "evidence_get", map[string]any{"evidence_id": item.ID}, &credential); err != nil {
		return err
	}
	u, err := url.Parse(credential.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || !strings.HasPrefix(u.Path, "/files/") || credential.Size != item.Size || credential.SHA256 != item.SHA256 {
		return fmt.Errorf("evidence credential does not match its inventory")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", credential.URL, nil)
	if err != nil {
		return fmt.Errorf("invalid evidence download URL")
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("evidence download interrupted: %w", wire.Safe(err))
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		offset = 0
		if err = f.Truncate(0); err != nil {
			return err
		}
	} else if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, item.Size-1, item.Size) {
		return fmt.Errorf("evidence download returned HTTP %d or an invalid range", response.StatusCode)
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(response.Body, item.Size-offset+1))
	syncErr := f.Sync()
	if copyErr != nil {
		return fmt.Errorf("evidence body interrupted: %w", wire.Safe(copyErr))
	}
	if syncErr != nil {
		return syncErr
	}
	if n != item.Size-offset || !matchesEvidence(root, partial, item) {
		if n == item.Size-offset {
			_ = f.Truncate(0)
		}
		return fmt.Errorf("evidence size or checksum differs from its inventory")
	}
	return root.Publish(partial, item.Local)
}
