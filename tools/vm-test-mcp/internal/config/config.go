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
	"time"

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
type Duration time.Duration

func (d Duration) Value() time.Duration { return time.Duration(d) }
func (d Duration) Seconds() int         { return int(time.Duration(d) / time.Second) }
func (d *Duration) UnmarshalYAML(data []byte) error {
	var value string
	if err := yaml.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("duration must include a unit, for example 30s or 10m: %w", err)
	}
	*d = Duration(parsed)
	return nil
}
func (d Duration) MarshalYAML() (any, error) { return d.Value().String(), nil }

type Tasks struct {
	DefaultTimeout Duration `yaml:"default_timeout"`
	MaxDuration    Duration `yaml:"max_duration"`
	CleanupTimeout Duration `yaml:"cleanup_timeout"`
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
	Executable       string              `yaml:"executable"`
	LibraryDir       string              `yaml:"library_dir"`
	APIHelper        string              `yaml:"api_helper"`
	ISOBuilder       string              `yaml:"iso_builder"`
	StartupTimeout   Duration            `yaml:"startup_timeout"`
	ShutdownTimeout  Duration            `yaml:"shutdown_timeout"`
	UnavailableAfter Duration            `yaml:"unavailable_after"`
	Router           Router              `yaml:"router"`
	Network          Network             `yaml:"network"`
	Templates        map[string]Template `yaml:"templates"`
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
type Storage struct {
	SuccessDays    int      `yaml:"success_days" json:"success_days"`
	FailureDays    int      `yaml:"failure_days" json:"failure_days"`
	SummaryDays    int      `yaml:"summary_days" json:"summary_days"`
	EvidenceBytes  int64    `yaml:"evidence_bytes" json:"evidence_bytes"`
	LowWaterBytes  int64    `yaml:"low_water_bytes" json:"low_water_bytes"`
	InputBytes     int64    `yaml:"input_bytes" json:"input_bytes"`
	UploadIdle     Duration `yaml:"upload_idle" json:"upload_idle"`
	MaxUploadBytes int64    `yaml:"max_upload_bytes" json:"max_upload_bytes"`
}
type Nix = Worker
type Config struct {
	Service    Service    `yaml:"service"`
	Tasks      Tasks      `yaml:"tasks"`
	VirtualBox VirtualBox `yaml:"virtualbox"`
	Storage    Storage    `yaml:"storage"`
	Nix        Nix        `yaml:"nix"`
}

func (c Config) VirtualBoxEnabled() bool {
	return c.VirtualBox.Router.ISO != "" || len(c.VirtualBox.Templates) != 0
}

func Defaults() Config {
	return Config{
		Service: Service{Listen: "127.0.0.1:8765"},
		Tasks:   Tasks{DefaultTimeout: Duration(time.Hour), MaxDuration: Duration(24 * time.Hour), CleanupTimeout: Duration(5 * time.Minute)},
		VirtualBox: VirtualBox{StartupTimeout: Duration(10 * time.Minute), ShutdownTimeout: Duration(time.Minute), UnavailableAfter: Duration(30 * time.Second),
			Network: Network{LAN: "10.77.0.0/24", WAN: "10.0.2.0/24", DNS: []string{"1.1.1.1", "9.9.9.9"}, Listen: "127.0.0.1", Sources: []string{"127.0.0.1/32"}, PortMin: 20000, PortMax: 29999, LeaseSeconds: 15}},
		Nix: Worker{ControllerUID: -1, Nix: "nix"}, Storage: Storage{SuccessDays: 1, FailureDays: 7, SummaryDays: 30, EvidenceBytes: 20 << 30, LowWaterBytes: 16 << 30, InputBytes: 16 << 30, UploadIdle: Duration(time.Hour), MaxUploadBytes: 8 << 30},
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
	if c.Tasks.DefaultTimeout < Duration(time.Second) || c.Tasks.MaxDuration < c.Tasks.DefaultTimeout || c.Tasks.MaxDuration > Duration(24*time.Hour) || c.Tasks.CleanupTimeout < Duration(time.Second) || c.Tasks.CleanupTimeout > Duration(time.Hour) {
		return bad("deadline limits")
	}
	if c.Storage.SuccessDays < 1 || c.Storage.FailureDays < c.Storage.SuccessDays || c.Storage.SummaryDays < c.Storage.FailureDays || c.Storage.MaxUploadBytes < 1 || c.Storage.MaxUploadBytes > 8<<30 || c.Storage.InputBytes < c.Storage.MaxUploadBytes || c.Storage.LowWaterBytes < 1 || c.Storage.EvidenceBytes <= c.Storage.LowWaterBytes || c.Storage.UploadIdle < Duration(time.Second) {
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
	defaults := Defaults().VirtualBox
	if c.VirtualBox.StartupTimeout == 0 {
		c.VirtualBox.StartupTimeout = defaults.StartupTimeout
	}
	if c.VirtualBox.ShutdownTimeout == 0 {
		c.VirtualBox.ShutdownTimeout = defaults.ShutdownTimeout
	}
	if c.VirtualBox.UnavailableAfter == 0 {
		c.VirtualBox.UnavailableAfter = defaults.UnavailableAfter
	}
	for _, value := range []Duration{c.VirtualBox.StartupTimeout, c.VirtualBox.ShutdownTimeout, c.VirtualBox.UnavailableAfter} {
		if value < Duration(time.Second) || value > Duration(time.Hour) {
			return bad("node timeouts must be between 1s and 1h")
		}
	}
	if c.VirtualBoxEnabled() {
		for _, p := range []string{c.VirtualBox.Executable, c.VirtualBox.LibraryDir, c.VirtualBox.APIHelper, c.VirtualBox.ISOBuilder} {
			if p != "" && !absolute(p) {
				return bad("VirtualBox path overrides must be absolute")
			}
		}
		if !absolute(c.VirtualBox.Router.ISO) {
			return bad("virtualbox.router.iso must be an absolute Live ISO path")
		}
	}
	if c.Nix.Socket != "" && !absolute(c.Nix.Socket) {
		return bad("worker socket")
	}
	lan, e := netip.ParsePrefix(c.VirtualBox.Network.LAN)
	if e != nil || !PrivateLAN(lan) {
		return bad("LAN must be a canonical RFC1918 IPv4 subnet /8 through /29")
	}
	wan, e := netip.ParsePrefix(c.VirtualBox.Network.WAN)
	if e != nil || !wan.Addr().Is4() || wan != wan.Masked() || wan.Bits() > 28 || lan.Overlaps(wan) {
		return bad("WAN subnet")
	}
	if a, e := netip.ParseAddr(c.VirtualBox.Network.Listen); e != nil || !a.Is4() {
		return bad("endpoint IPv4 listener")
	}
	if len(c.VirtualBox.Network.Sources) == 0 {
		return bad("endpoint source CIDRs are required")
	}
	for _, s := range append(append([]string{}, c.VirtualBox.Network.Sources...), c.VirtualBox.Network.Protected...) {
		p, e := netip.ParsePrefix(s)
		if e != nil || !p.Addr().Is4() || p != p.Masked() {
			return bad("invalid IPv4 CIDR")
		}
	}
	for _, s := range append(append([]string{}, c.VirtualBox.Network.DNS...), c.VirtualBox.Network.PublicHostAddresses...) {
		a, e := netip.ParseAddr(s)
		if e != nil || !a.Is4() {
			return bad("invalid IPv4 address")
		}
	}
	if len(c.VirtualBox.Network.DNS) == 0 || len(c.VirtualBox.Network.DNS) > 8 || c.VirtualBox.Network.PortMin < 1024 || c.VirtualBox.Network.PortMax > 65535 || c.VirtualBox.Network.PortMin > c.VirtualBox.Network.PortMax || c.VirtualBox.Network.LeaseSeconds < 5 || c.VirtualBox.Network.LeaseSeconds > 60 {
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
	KeepFailed    bool   `yaml:"keep_failed"`
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
