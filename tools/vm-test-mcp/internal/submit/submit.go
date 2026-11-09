package submit

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Options struct {
	Nix, Test, SmokeProject, SmokeFile, RequestID, CacheURL, CacheID, WorkDir, SigningKey, CacheDir string
	CacheBytes                                                                                      int64
	Progress                                                                                        io.Writer
}
type Metadata struct {
	RunID      string   `json:"test_run_id,omitempty"`
	Derivation string   `json:"derivation"`
	System     string   `json:"system"`
	ForceAccel bool     `json:"force_accel"`
	Nodes      []string `json:"nodes"`
	Containers []string `json:"containers"`
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

//go:embed adapter.nix
var adapterNix string

//go:embed sitecustomize.py
var driverObserver string

func Expression(id string) string {
	return `t: (` + adapterNix + `) t ` + nixString(id) + ` ` + nixString(driverObserver)
}

func nixString(value string) string {
	return `"` + strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, "${", `\${`,
		"\n", `\n`, "\r", `\r`, "\t", `\t`,
	).Replace(value) + `"`
}
func Prepare(ctx context.Context, o Options) (map[string]any, error) {
	if !requestIDPattern.MatchString(o.RequestID) || !wire.IDPattern.MatchString(o.CacheID) || (o.Test == "") == (o.SmokeProject == "") || strings.HasPrefix(o.Test, "-") {
		return nil, usage("invalid request ID, cache ID or test attribute")
	}
	identity := o.Test
	if o.SmokeProject != "" {
		if !filepath.IsAbs(o.SmokeProject) || !filepath.IsAbs(o.SmokeFile) {
			return nil, usage("smoke project and module must be absolute paths")
		}
		identity = "smoke:" + o.SmokeProject
	}
	u, e := url.Parse(o.CacheURL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" || !strings.HasPrefix(u.Path, "/files/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, usage("use the cache URL from upload_create")
	}
	if !filepath.IsAbs(o.WorkDir) {
		return nil, usage("work-dir must be an absolute task-owned directory")
	}
	if o.SigningKey != "" && !filepath.IsAbs(o.SigningKey) {
		return nil, usage("signing-key must be absolute")
	}
	if o.CacheDir != "" && !filepath.IsAbs(o.CacheDir) {
		return nil, usage("cache-dir must be absolute")
	}
	root, e := safefs.Private(o.WorkDir, true)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	log, e := root.OpenFile("prepare.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	defer log.Close()
	if o.Progress == nil {
		o.Progress = io.Discard
	}
	started := time.Now()
	executionID := wire.ID()
	metaFile := filepath.Join(o.WorkDir, "submission.json")
	var meta Metadata
	// A retry reuses the saved evaluated derivation, even if the project changed.
	if old, e := os.ReadFile(metaFile); e == nil {
		var saved struct {
			RequestID   string   `json:"request_id"`
			ExecutionID string   `json:"execution_id"`
			Test        string   `json:"test"`
			Metadata    Metadata `json:"metadata"`
		}
		if e = wire.Decode(old, &saved); e != nil {
			return nil, e
		}
		if saved.RequestID != o.RequestID || saved.Test != identity || !wire.IDPattern.MatchString(saved.ExecutionID) {
			return nil, usage("work directory belongs to a different submission")
		}
		meta = saved.Metadata
		executionID = saved.ExecutionID
	} else if os.IsNotExist(e) {
		args := []string{"eval", "--no-update-lock-file", "--no-write-lock-file", "--json"}
		if o.SmokeProject != "" {
			// Resolve with the CLI so Git projects retain Git source filtering.
			// getFlake on a bare absolute path otherwise copies ignored files too.
			resolve := exec.CommandContext(ctx, o.Nix, "flake", "metadata", "--no-update-lock-file", "--no-write-lock-file", "--json", o.SmokeProject)
			resolve.Stderr = log
			raw, err := resolve.Output()
			if err != nil {
				return nil, fmt.Errorf("resolve smoke project: %w", err)
			}
			var source struct {
				URL string `json:"resolvedUrl"`
			}
			if err := json.Unmarshal(raw, &source); err != nil || source.URL == "" {
				return nil, fmt.Errorf("Nix did not return a resolved project URL")
			}
			// The consuming project's lock chooses nixpkgs; no global registry or
			// new flake output is needed for the packaged acceptance module.
			project, _ := json.Marshal(source.URL)
			module, _ := json.Marshal(o.SmokeFile)
			expression := `let project = builtins.getFlake ` + strings.ReplaceAll(string(project), "${", `\${`) + `; pkgs = import project.inputs.nixpkgs { system = builtins.currentSystem; }; test = pkgs.testers.runNixOSTest (import (builtins.toPath ` + strings.ReplaceAll(string(module), "${", `\${`) + `)); in (` + Expression(executionID) + `) test`
			args = append(args, "--impure", "--expr", expression)
		} else {
			args = append(args, "--apply", Expression(executionID), o.Test)
		}
		cmd := exec.CommandContext(ctx, o.Nix, args...)
		cmd.Stderr = log
		raw, e := cmd.Output()
		if e != nil {
			return nil, e
		}
		if e = wire.Decode(raw, &meta); e != nil {
			return nil, e
		}
		if !meta.ForceAccel || len(meta.Nodes) == 0 || len(meta.Containers) != 0 || !wire.StorePattern.MatchString(meta.Derivation) {
			return nil, usage("select a QEMU test with nodes, qemu.forceAccel=true and no nspawn containers")
		}
		keep := exec.CommandContext(ctx, o.Nix, "build", "--offline", "--out-link", filepath.Join(o.WorkDir, "derivation"), meta.Derivation)
		keep.Stdout, keep.Stderr = log, log
		if e = keep.Run(); e != nil {
			return nil, fmt.Errorf("retain evaluated derivation: %w", e)
		}
		data := wire.JSON(map[string]any{"request_id": o.RequestID, "execution_id": executionID, "test": identity, "metadata": meta})
		temporary := metaFile + "." + wire.ID() + ".partial"
		defer os.Remove(temporary)
		f, e := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		f.Close()
		if e != nil {
			return nil, e
		}
		if e = os.Link(temporary, metaFile); e != nil {
			return nil, e
		}
	} else {
		return nil, e
	}
	if !meta.ForceAccel || len(meta.Nodes) == 0 || len(meta.Containers) != 0 || !wire.StorePattern.MatchString(meta.Derivation) || !strings.HasSuffix(meta.Derivation, ".drv") {
		return nil, fmt.Errorf("invalid saved test metadata")
	}
	meta.RunID = executionID
	cache, e := openExportCache(ctx, o)
	if e != nil {
		return nil, e
	}
	defer cache.Close()
	defer func() {
		if err := cache.retain(map[string]bool{}); err != nil {
			fmt.Fprintf(o.Progress, "Local cache retention: %v\n", err)
		}
	}()
	fmt.Fprintln(o.Progress, "Exporting saved derivation closure…")
	manifest, e := cache.export(ctx, o, meta, log)
	if e != nil {
		return nil, e
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("upload redirects are not allowed") }}
	if e = uploadManifest(ctx, client, o, cache, manifest); e != nil {
		return nil, e
	}
	fmt.Fprintf(o.Progress, "Input ready: %d objects; elapsed %s; log %s\n", len(manifest.Objects), time.Since(started).Round(time.Second), filepath.Join(o.WorkDir, "prepare.log"))
	return map[string]any{"request_id": o.RequestID, "upload_id": o.CacheID}, nil

}

// Keep the object and server rejection in errors without disclosing the scoped
// capability URL. Successful uploads are idempotent when resuming a saved run.
func uploadCacheObject(ctx context.Context, client *http.Client, base, name, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	request, err := http.NewRequestWithContext(ctx, "PUT", strings.TrimRight(base, "/")+"/"+name, f)
	if err != nil {
		return fmt.Errorf("invalid cache upload URL")
	}
	st, err := f.Stat()
	if err != nil {
		return err
	}
	request.ContentLength = st.Size()
	response, err := client.Do(request)
	if err != nil {
		// net/url.Error includes the credential-bearing URL in Error().
		return fmt.Errorf("cache object %q upload: %w", name, wire.Safe(err))
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	if response.StatusCode == http.StatusOK {
		return nil
	}
	var envelope struct {
		Error *wire.Error `json:"error"`
	}
	if readErr == nil && len(body) <= 65536 && wire.Decode(body, &envelope) == nil && envelope.Error != nil &&
		requestIDPattern.MatchString(envelope.Error.Code) && envelope.Error.Message != "" && wire.ValidText(envelope.Error.Message, 1024) {
		failure := wire.Safe(envelope.Error)
		if failure.Details == nil {
			failure.Details = map[string]any{}
		}
		failure.Details["boundary"], failure.Details["object"], failure.Details["http_status"] = "cache_upload", name, response.StatusCode
		return fmt.Errorf("cache object %q upload returned HTTP %d: %w", name, response.StatusCode, failure)
	}
	return fmt.Errorf("cache object %q upload returned HTTP %d without a valid error response", name, response.StatusCode)
}
