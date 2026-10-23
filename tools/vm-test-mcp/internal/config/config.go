package config

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Service struct {
	Listen    string   `yaml:"listen"`
	PublicURL string   `yaml:"public_url"`
	Token     string   `yaml:"token"`
	StateDir  string   `yaml:"state_dir"`
	Hosts     []string `yaml:"hosts"`
	Origins   []string `yaml:"origins"`
}
type Scheduler struct {
	DefaultSeconds int `yaml:"default_seconds"`
	MaxSeconds     int `yaml:"max_seconds"`
}
type Media struct {
	Camera       bool `yaml:"camera" json:"camera,omitempty"`
	AudioInput   bool `yaml:"audio_input" json:"audio_input,omitempty"`
	AudioOutput  bool `yaml:"audio_output" json:"audio_output,omitempty"`
	Accelerate3D bool `yaml:"accelerate_3d" json:"accelerate_3d,omitempty"`
}

func (m Media) Allows(v Media) bool {
	return (!v.Camera || m.Camera) && (!v.AudioInput || m.AudioInput) && (!v.AudioOutput || m.AudioOutput) && (!v.Accelerate3D || m.Accelerate3D)
}

type Template struct {
	Kind          string            `yaml:"kind" json:"kind"`
	VM            string            `yaml:"vm" json:"-"`
	Snapshot      string            `yaml:"snapshot" json:"-"`
	Username      string            `yaml:"username" json:"-"`
	Password      string            `yaml:"password" json:"-"`
	Domain        string            `yaml:"domain" json:"-"`
	Supervisor    string            `yaml:"-" json:"-"`
	WorkDir       string            `yaml:"work_dir" json:"-"`
	StartupEnv    map[string]string `yaml:"startup_env" json:"-"`
	InputInterval *float64          `yaml:"input_interval_seconds" json:"input_interval_seconds"`
	CPUs          int               `yaml:"cpus" json:"cpus"`
	MemoryMiB     int               `yaml:"memory_mib" json:"memory_mib"`
	MaxCPUs       int               `yaml:"max_cpus" json:"max_cpus"`
	MaxMemoryMiB  int               `yaml:"max_memory_mib" json:"max_memory_mib"`
	Media         Media             `yaml:"media" json:"media"`
	Cameras       map[string]string `yaml:"cameras" json:"-"`
}
type VirtualBox struct {
	Executable      string              `yaml:"executable"`
	LibraryDir      string              `yaml:"library_dir"`
	InputHelper     string              `yaml:"input_helper"`
	ISOBuilder      string              `yaml:"iso_builder"`
	ReadySeconds    int                 `yaml:"ready_seconds"`
	ShutdownSeconds int                 `yaml:"shutdown_seconds"`
	Templates       map[string]Template `yaml:"templates"`
}
type Router struct {
	ISO string `yaml:"iso"`
}
type Network struct {
	LAN                 string   `yaml:"lan"`
	WAN                 string   `yaml:"wan"`
	DNS                 []string `yaml:"dns"`
	Protected           []string `yaml:"protected_cidrs"`
	PublicHostAddresses []string `yaml:"public_host_addresses"`
	Listen              string   `yaml:"listen"`
	Sources             []string `yaml:"source_cidrs"`
	PortMin             int      `yaml:"port_min"`
	PortMax             int      `yaml:"port_max"`
	LeaseSeconds        int      `yaml:"lease_seconds"`
}
type Retention struct {
	Days           int   `yaml:"days"`
	MaxUploadBytes int64 `yaml:"max_upload_bytes"`
}
type Nix = Worker
type Config struct {
	Service    Service    `yaml:"service"`
	Scheduler  Scheduler  `yaml:"scheduler"`
	VirtualBox VirtualBox `yaml:"virtualbox"`
	Router     Router     `yaml:"router"`
	Network    Network    `yaml:"network"`
	Retention  Retention  `yaml:"retention"`
	Nix        Nix        `yaml:"nix"`
}

func (c Config) VirtualBoxEnabled() bool {
	return c.Router.ISO != "" || len(c.VirtualBox.Templates) != 0
}

func Defaults() Config {
	return Config{
		Service: Service{Listen: "127.0.0.1:8765"}, Scheduler: Scheduler{DefaultSeconds: 3600, MaxSeconds: 86400},
		VirtualBox: VirtualBox{ReadySeconds: 600, ShutdownSeconds: 600},
		Nix:        Worker{ControllerUID: -1, Nix: "nix"},
		Network:    Network{LAN: "10.77.0.0/24", WAN: "10.0.2.0/24", DNS: []string{"1.1.1.1", "9.9.9.9"}, Listen: "127.0.0.1", Sources: []string{"127.0.0.1/32"}, PortMin: 20000, PortMax: 29999, LeaseSeconds: 15},
		Retention:  Retention{MaxUploadBytes: 8 << 30},
	}
}
func decode(file string, v any) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, wire.MaxJSON+1))
	if err != nil {
		return err
	}
	if len(b) > wire.MaxJSON {
		return fmt.Errorf("configuration exceeds limit")
	}
	d := yaml.NewDecoder(bytes.NewReader(b), yaml.Strict())
	if err = d.Decode(v); err != nil {
		return fmt.Errorf("invalid YAML configuration: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one YAML document")
	}
	return nil
}
func Load(file string) (Config, error) {
	c := Defaults()
	if err := decode(file, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func absolute(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsRune(p, 0)
}
func origin(s string) bool {
	u, e := url.Parse(s)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}
func (c *Config) Validate() error {
	bad := func(s string) error { return fmt.Errorf("invalid configuration: %s", s) }
	if c.Service.Token == "" || strings.ContainsAny(c.Service.Token, "\r\n") || !absolute(c.Service.StateDir) {
		return bad("service token and absolute private state_dir are required")
	}
	host, port, e := net.SplitHostPort(c.Service.Listen)
	number, portError := strconv.Atoi(port)
	if e != nil || portError != nil || number < 1 || number > 65535 || net.ParseIP(host).To4() == nil {
		return bad("listen must contain a literal IPv4 address and port")
	}
	if !origin(c.Service.PublicURL) {
		return bad("public_url must be an HTTP origin")
	}
	for _, h := range c.Service.Hosts {
		if h == "" || strings.ContainsAny(h, "/\r\n@ ") {
			return bad("invalid Host allowlist")
		}
	}
	for _, o := range c.Service.Origins {
		if !origin(o) {
			return bad("invalid Origin allowlist")
		}
	}
	if c.Scheduler.DefaultSeconds < 1 || c.Scheduler.MaxSeconds < c.Scheduler.DefaultSeconds || c.Scheduler.MaxSeconds > 86400*30 {
		return bad("deadline limits")
	}
	if c.Retention.Days < 0 || c.Retention.MaxUploadBytes < 1 {
		return bad("retention limits")
	}
	for name, t := range c.VirtualBox.Templates {
		if t.Supervisor == "" {
			if t.Kind == "windows" {
				t.Supervisor = `C:\ProgramData\vm-test\vm-test-guest.exe`
			} else {
				t.Supervisor = "/opt/vm-test/vm-test-guest"
			}
		}
		if t.WorkDir == "" {
			if t.Kind == "windows" {
				t.WorkDir = `C:\ProgramData\vm-test\work`
			} else {
				t.WorkDir = "/var/lib/vm-test/work"
			}
		}
		if t.CPUs == 0 {
			t.CPUs = 2
		}
		if t.MemoryMiB == 0 {
			if t.Kind == "windows" {
				t.MemoryMiB = 4096
			} else {
				t.MemoryMiB = 2048
			}
		}
		if t.MaxCPUs == 0 {
			t.MaxCPUs = 8
		}
		if t.MaxMemoryMiB == 0 {
			t.MaxMemoryMiB = 16384
		}
		if t.InputInterval == nil {
			v := 1.0
			t.InputInterval = &v
		}
		if !wire.NamePattern.MatchString(name) || (t.Kind != "windows" && t.Kind != "linux") || !wire.ValidText(t.VM, 256) || strings.TrimSpace(t.VM) == "" || !wire.UUIDPattern.MatchString(t.Snapshot) || t.Username == "" || t.Password == "" || !wire.ValidText(t.Domain, 256) {
			return bad("template identity or guest settings")
		}
		if t.Kind == "linux" && (t.Username != "root" || t.Domain != "") {
			return bad("Linux templates require root credentials")
		}
		if math.IsNaN(*t.InputInterval) || math.IsInf(*t.InputInterval, 0) || *t.InputInterval < 0 || *t.InputInterval > 60 {
			return bad("input interval must be 0..60 seconds")
		}
		for k, v := range t.StartupEnv {
			if k == "" || strings.ContainsAny(k, "=\x00\r\n") || !wire.ValidText(v, 4096) {
				return bad("startup environment")
			}
		}
		if t.CPUs < 1 || t.CPUs > t.MaxCPUs || t.MemoryMiB < 128 || t.MemoryMiB > t.MaxMemoryMiB {
			return bad("template resource bounds")
		}
		if !wire.ValidText(t.WorkDir, 32768) || (t.Kind == "linux" && !absolute(t.WorkDir)) || (t.Kind == "windows" && (len(t.WorkDir) < 3 || t.WorkDir[1] != ':' || (t.WorkDir[2] != '\\' && t.WorkDir[2] != '/'))) {
			return bad("absolute guest work directory is required")
		}
		for k, v := range t.Cameras {
			if !wire.NamePattern.MatchString(k) || v == "" || !wire.ValidText(v, 4096) {
				return bad("camera mapping")
			}
		}
		c.VirtualBox.Templates[name] = t
	}
	if c.VirtualBox.ReadySeconds == 0 {
		c.VirtualBox.ReadySeconds = 600
	}
	if c.VirtualBox.ShutdownSeconds == 0 {
		c.VirtualBox.ShutdownSeconds = 600
	}
	if c.VirtualBox.ReadySeconds < 1 || c.VirtualBox.ReadySeconds > 3600 || c.VirtualBox.ShutdownSeconds < 1 || c.VirtualBox.ShutdownSeconds > 3600 {
		return bad("VirtualBox timeouts must be 1..3600 seconds")
	}
	if c.VirtualBoxEnabled() {
		for _, p := range []string{c.VirtualBox.Executable, c.VirtualBox.LibraryDir, c.VirtualBox.InputHelper, c.VirtualBox.ISOBuilder} {
			if p != "" && !absolute(p) {
				return bad("VirtualBox path overrides must be absolute")
			}
		}
		if !absolute(c.Router.ISO) {
			return bad("router.iso must be an absolute Live ISO path")
		}
	}
	if c.Nix.Socket != "" && !absolute(c.Nix.Socket) {
		return bad("worker socket")
	}
	lan, e := netip.ParsePrefix(c.Network.LAN)
	if e != nil || !PrivateLAN(lan) {
		return bad("LAN must be a canonical RFC1918 IPv4 subnet /8 through /29")
	}
	wan, e := netip.ParsePrefix(c.Network.WAN)
	if e != nil || !wan.Addr().Is4() || wan != wan.Masked() || wan.Bits() > 28 || lan.Overlaps(wan) {
		return bad("WAN subnet")
	}
	if a, e := netip.ParseAddr(c.Network.Listen); e != nil || !a.Is4() {
		return bad("endpoint IPv4 listener")
	}
	if len(c.Network.Sources) == 0 {
		return bad("endpoint source CIDRs are required")
	}
	for _, s := range append(append([]string{}, c.Network.Sources...), c.Network.Protected...) {
		p, e := netip.ParsePrefix(s)
		if e != nil || !p.Addr().Is4() || p != p.Masked() {
			return bad("invalid IPv4 CIDR")
		}
	}
	for _, s := range append(append([]string{}, c.Network.DNS...), c.Network.PublicHostAddresses...) {
		a, e := netip.ParseAddr(s)
		if e != nil || !a.Is4() {
			return bad("invalid IPv4 address")
		}
	}
	if len(c.Network.DNS) == 0 || len(c.Network.DNS) > 8 || c.Network.PortMin < 1024 || c.Network.PortMax > 65535 || c.Network.PortMin > c.Network.PortMax || c.Network.LeaseSeconds < 5 || c.Network.LeaseSeconds > 60 {
		return bad("network limits")
	}
	return nil
}
func PrivateLAN(p netip.Prefix) bool {
	if !p.Addr().Is4() || p != p.Masked() || p.Bits() > 29 {
		return false
	}
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		q := netip.MustParsePrefix(s)
		if q.Contains(p.Addr()) && p.Bits() >= q.Bits() {
			return true
		}
	}
	return false
}

type Worker struct {
	StateDir      string `yaml:"state_dir"`
	Socket        string `yaml:"socket"`
	ControllerUID int    `yaml:"controller_uid"`
	Nix           string `yaml:"executable"`
	CacheURL      string `yaml:"cache_url"`
}

func LoadWorker(file string) (Worker, error) {
	shared := Defaults()
	if e := decode(file, &shared); e != nil {
		return shared.Nix, e
	}
	c := shared.Nix
	if c.CacheURL == "" {
		c.CacheURL = shared.Service.PublicURL
	}
	if c.Nix == "nix" {
		if p, e := exec.LookPath(c.Nix); e == nil {
			c.Nix = p
		}
	}
	if !absolute(c.StateDir) {
		return c, fmt.Errorf("nix.state_dir must be a canonical absolute path")
	}
	if !absolute(c.Socket) {
		return c, fmt.Errorf("nix.socket must be a canonical absolute path")
	}
	if !absolute(c.Nix) {
		return c, fmt.Errorf("nix.executable must resolve to a canonical absolute path; ensure nix is in PATH or configure an absolute executable")
	}
	if c.ControllerUID < 0 {
		return c, fmt.Errorf("nix.controller_uid must specify the controller account's nonnegative UID")
	}
	if !origin(c.CacheURL) {
		return c, fmt.Errorf("nix.cache_url (or service.public_url) must be an HTTP(S) origin")
	}
	if c.StateDir == shared.Service.StateDir {
		return c, fmt.Errorf("controller and worker require separate state directories")
	}
	return c, nil
}
