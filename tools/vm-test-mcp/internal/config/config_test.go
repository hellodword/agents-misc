package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurationConfigurationAndAnnotatedExample(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("state_dir: %s\nservice:\n  token: fixture\n  public_url: http://localhost:8765\ntasks:\n  default_timeout: 2h\n  max_duration: 12h\n  cleanup_timeout: 45s\nvirtualbox:\n  startup_parallelism: 3\n  startup_timeout: 8m\n  shutdown_timeout: 90s\n  unavailable_after: 40s\n  router:\n    iso: /fixture/router.iso\n  network:\n    lan: 10.99.0.0/24\nstorage:\n  failure_days: 7\n", t.TempDir())
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tasks.DefaultTimeout.Value() != 2*time.Hour || c.Tasks.CleanupTimeout.Value() != 45*time.Second || c.VirtualBox.StartupTimeout.Value() != 8*time.Minute || c.VirtualBox.StartupParallelism != 3 || c.VirtualBox.Network.LAN != "10.99.0.0/24" || c.Storage.FailureDays != 7 {
		t.Fatal("duration or nested configuration differs")
	}
	example, err := Load("../../config.example.yaml")
	if err != nil || example.VirtualBox.StartupParallelism != 1 {
		t.Fatal("example startup default", example.VirtualBox.StartupParallelism, err)
	}
}

func TestStrictConfig(t *testing.T) {
	for _, body := range []string{"service:\n  token: a\n  token: b\n", "service:\n  typo: a\n", "tasks:\n  default_timeout: nope\n", "service: {}\n---\nservice: {}\n"} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(p); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	c := Defaults()
	c.StateDir = t.TempDir()
	c.Service.PublicURL = "http://localhost:8765"
	if c.Validate() == nil {
		t.Fatal("empty token accepted")
	}
	c.Service.Token = "synthetic"
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.VirtualBox.Network.LAN = "10.0.2.0/24"
	if c.Validate() == nil {
		t.Fatal("WAN overlap accepted")
	}
}

func TestRouterWithoutTemplatesValidatesISOAndPrograms(t *testing.T) {
	c := Defaults()
	c.Service.Token = "synthetic"
	c.StateDir = t.TempDir()
	c.Service.PublicURL = "http://localhost:8765"
	c.VirtualBox.Router.ISO = "/fixture/router.iso"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.VirtualBox.Router.ISO = "relative/router.iso"
	if c.Validate() == nil {
		t.Fatal("relative router ISO accepted without templates")
	}
	c.VirtualBox.Router.ISO = "/fixture/router.iso"
	c.VirtualBox.ISOBuilder = "relative/xorriso"
	if c.Validate() == nil {
		t.Fatal("relative helper override accepted without templates")
	}
}

func TestSharedConfigurationAndPermissions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := fmt.Sprintf("state_dir: %s\nservice:\n  token: synthetic\n  public_url: http://127.0.0.1:8765\nnix:\n  socket: %s/worker.sock\n  state_dir: %s\n  executable: /fixture/nix\n  controller_uid: %d\n", dir, dir, dir, os.Getuid())
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	w, err := LoadWorker(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Nix.Socket != w.Socket || w.CacheURL != c.Service.PublicURL || len(c.Service.Hosts) != 0 || len(c.Service.Origins) != 0 {
		t.Fatal("shared configuration or defaults differ")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0644 {
		t.Fatal("configuration permissions changed")
	}
	if err = os.WriteFile(p, []byte(body+"  unknown_field: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadWorker(p); err == nil {
		t.Fatal("worker accepted unknown shared field")
	}
}

func TestExecutableLibraryDiscovery(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	lib := filepath.Join(dir, "lib", "virtualbox")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lib, 0755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "VBoxManage")
	if err := os.WriteFile(exe, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "VBoxXPCOMC.so"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "VBoxManage")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(link))
	resolved, err := Program("", "VBoxManage")
	if err != nil || resolved != link {
		t.Fatal(resolved, err)
	}
	resolved, err = LibraryDir("", resolved)
	if err != nil || resolved != lib {
		t.Fatal(resolved, err)
	}
	if resolved, err = LibraryDir("/administrator/override", exe); err != nil || resolved != "/administrator/override" {
		t.Fatal(resolved, err)
	}
}

func TestTemplateDefaultsAndUnboundedHTTPHosts(t *testing.T) {
	c := Defaults()
	c.Service.Token = "fixture"
	c.StateDir = t.TempDir()
	c.Service.PublicURL = "http://example.test"
	c.VirtualBox.Router.ISO = "/fixture/router.iso"
	c.VirtualBox.Templates = map[string]Template{"linux": {Kind: "linux", VM: "named-vm", Snapshot: "11111111-1111-4111-8111-111111111111", Username: "root", Password: "fixture"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	v := c.VirtualBox.Templates["linux"]
	if v.Supervisor != "/opt/vm-test/vm-test-guest" || v.WorkDir != "/var/lib/vm-test/work" || v.CPUs != 2 || *v.InputInterval != 1 || len(c.Service.Hosts) != 0 {
		t.Fatal(v)
	}
	v.Username = "ordinary-user"
	c.VirtualBox.Templates["linux"] = v
	if c.Validate() == nil {
		t.Fatal("non-root Linux template accepted")
	}
}

func TestRoleDirectories(t *testing.T) {
	root := t.TempDir()
	for _, override := range []string{"", root, filepath.Join(root, "separate")} {
		c := Defaults()
		c.StateDir, c.Nix.StateDir = root, override
		if err := c.validateStateDirs(); err != nil {
			t.Fatal(err)
		}
		if c.ControllerDir() != filepath.Join(root, "controller") {
			t.Fatal(c.ControllerDir())
		}
		expectedRoot := override
		if expectedRoot == "" {
			expectedRoot = root
		}
		if c.WorkerDir() != filepath.Join(expectedRoot, "nix-worker") {
			t.Fatal(c.WorkerDir())
		}
	}
	c := Defaults()
	c.StateDir, c.Nix.StateDir = root, filepath.Join(root, "controller")
	if c.validateStateDirs() == nil {
		t.Fatal("nested role directory accepted")
	}
	c.StateDir, c.Nix.StateDir = filepath.Join(root, "nix-worker"), root
	if c.validateStateDirs() == nil {
		t.Fatal("reverse nesting accepted")
	}
}
