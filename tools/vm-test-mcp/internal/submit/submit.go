package submit

import (
	"context"
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

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Options struct{ Nix, Test, SmokeProject, SmokeFile, RequestID, CacheURL, CacheID, WorkDir, SigningKey string }
type Metadata struct {
	Derivation string   `json:"derivation"`
	System     string   `json:"system"`
	ForceAccel bool     `json:"force_accel"`
	Nodes      []string `json:"nodes"`
	Containers []string `json:"containers"`
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

func Expression(id string) string {
	quoted, _ := json.Marshal("-" + id)
	return `t: let fresh = t.overrideTestDerivation (old: { name = old.name + ` + string(quoted) + `; requiredSystemFeatures = builtins.attrNames (builtins.listToAttrs (map (name: { inherit name; value = true; }) ((old.requiredSystemFeatures or []) ++ [ "kvm" "nixos-test" ]))); }); in { derivation = fresh.drvPath; system = t.system; force_accel = t.config.qemu.forceAccel or false; nodes = builtins.attrNames (t.config.nodes or {}); containers = builtins.attrNames (t.config.containers or {}); }`
}
func Prepare(ctx context.Context, o Options) (map[string]any, error) {
	if !requestIDPattern.MatchString(o.RequestID) || !wire.IDPattern.MatchString(o.CacheID) || (o.Test == "") == (o.SmokeProject == "") || strings.HasPrefix(o.Test, "-") {
		return nil, fmt.Errorf("invalid request ID, cache ID or test attribute")
	}
	identity := o.Test
	if o.SmokeProject != "" {
		if !filepath.IsAbs(o.SmokeProject) || !filepath.IsAbs(o.SmokeFile) {
			return nil, fmt.Errorf("smoke project and module must be absolute paths")
		}
		identity = "smoke:" + o.SmokeProject
	}
	u, e := url.Parse(o.CacheURL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" || !strings.HasPrefix(u.Path, "/files/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("use the cache URL from transfer_create")
	}
	if !filepath.IsAbs(o.WorkDir) {
		return nil, fmt.Errorf("work-dir must be an absolute task-owned directory")
	}
	if e = os.MkdirAll(o.WorkDir, 0700); e != nil {
		return nil, e
	}
	metaFile := filepath.Join(o.WorkDir, "submission.json")
	var meta Metadata
	// A retry reuses the saved evaluated derivation, even if the project changed.
	if old, e := os.ReadFile(metaFile); e == nil {
		var saved struct {
			RequestID string   `json:"request_id"`
			Test      string   `json:"test"`
			Metadata  Metadata `json:"metadata"`
		}
		if e = wire.Decode(old, &saved); e != nil {
			return nil, e
		}
		if saved.RequestID != o.RequestID || saved.Test != identity {
			return nil, fmt.Errorf("work directory belongs to a different submission")
		}
		meta = saved.Metadata
	} else if os.IsNotExist(e) {
		args := []string{"eval", "--no-update-lock-file", "--no-write-lock-file", "--json"}
		if o.SmokeProject != "" {
			// Resolve with the CLI so Git projects retain Git source filtering.
			// getFlake on a bare absolute path otherwise copies ignored files too.
			resolve := exec.CommandContext(ctx, o.Nix, "flake", "metadata", "--no-update-lock-file", "--no-write-lock-file", "--json", o.SmokeProject)
			resolve.Stderr = os.Stderr
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
			expression := `let project = builtins.getFlake ` + strings.ReplaceAll(string(project), "${", `\${`) + `; pkgs = import project.inputs.nixpkgs { system = builtins.currentSystem; }; test = pkgs.testers.runNixOSTest (import (builtins.toPath ` + strings.ReplaceAll(string(module), "${", `\${`) + `)); in (` + Expression(o.RequestID) + `) test`
			args = append(args, "--impure", "--expr", expression)
		} else {
			args = append(args, "--apply", Expression(o.RequestID), o.Test)
		}
		cmd := exec.CommandContext(ctx, o.Nix, args...)
		cmd.Stderr = os.Stderr
		raw, e := cmd.Output()
		if e != nil {
			return nil, e
		}
		if e = wire.Decode(raw, &meta); e != nil {
			return nil, e
		}
		if !meta.ForceAccel || len(meta.Nodes) == 0 || len(meta.Containers) != 0 || !wire.StorePattern.MatchString(meta.Derivation) {
			return nil, fmt.Errorf("select a QEMU test with nodes, qemu.forceAccel=true and no nspawn containers")
		}
		data := wire.JSON(map[string]any{"request_id": o.RequestID, "test": identity, "metadata": meta})
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
	cache := filepath.Join(o.WorkDir, "cache")
	destination := url.URL{Scheme: "file", Path: cache}
	q := destination.Query()
	q.Set("compression", "xz")
	if o.SigningKey != "" {
		if !filepath.IsAbs(o.SigningKey) {
			return nil, fmt.Errorf("signing-key must be absolute")
		}
		q.Set("secret-key", o.SigningKey)
	}
	destination.RawQuery = q.Encode()
	cmd := exec.CommandContext(ctx, o.Nix, "copy", "--derivation", "--to", destination.String(), meta.Derivation)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return nil, e
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("upload redirects are not allowed") }}
	e = filepath.WalkDir(cache, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cache contains a symlink")
		}
		name, e := filepath.Rel(cache, p)
		if e != nil {
			return e
		}
		if name == "nix-cache-info" {
			return nil
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		defer f.Close()
		request, e := http.NewRequestWithContext(ctx, "PUT", strings.TrimRight(o.CacheURL, "/")+"/"+filepath.ToSlash(name), f)
		if e != nil {
			return e
		}
		st, e := f.Stat()
		if e != nil {
			return e
		}
		request.ContentLength = st.Size()
		response, e := client.Do(request)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		io.Copy(io.Discard, io.LimitReader(response.Body, 65536))
		if response.StatusCode != 200 {
			return fmt.Errorf("cache upload returned HTTP %d", response.StatusCode)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return map[string]any{"request_id": o.RequestID, "cache_id": o.CacheID, "derivation": meta.Derivation, "system": meta.System}, nil
}
