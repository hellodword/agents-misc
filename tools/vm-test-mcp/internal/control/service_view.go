package control

import (
	"context"
	"sort"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type BackendView struct {
	Available     bool        `json:"available"`
	WaitingReason string      `json:"waiting_reason"`
	Identity      any         `json:"identity,omitempty"`
	Error         *wire.Error `json:"error,omitempty"`
}
type AdmissionView struct {
	Accepting      bool `json:"accepting"`
	StorageBlocked bool `json:"storage_blocked"`
}
type ServiceLimits struct {
	StartupParallelism      int   `json:"startup_parallelism"`
	StartupSeconds          int   `json:"startup_seconds"`
	ShutdownSeconds         int   `json:"shutdown_seconds"`
	ProbeIntervalSeconds    int   `json:"probe_interval_seconds"`
	ProbeTimeoutSeconds     int   `json:"probe_timeout_seconds"`
	UnavailableAfterSeconds int   `json:"unavailable_after_seconds"`
	MaxActiveEndpoints      int   `json:"max_active_endpoints_per_lab"`
	EvidenceBytes           int64 `json:"evidence_bytes"`
	EvidenceLowWaterBytes   int64 `json:"evidence_low_water_bytes"`
	SuccessDays             int   `json:"success_days"`
	FailureDays             int   `json:"failure_days"`
	SummaryDays             int   `json:"summary_days"`
	WaitSeconds             int   `json:"wait_seconds"`
	ToolTimeoutSeconds      int   `json:"tool_timeout_sec"`
	HeartbeatSeconds        int   `json:"heartbeat_seconds"`
	DefaultSeconds          int   `json:"default_seconds"`
	MaxSeconds              int   `json:"max_seconds"`
	CleanupSeconds          int   `json:"cleanup_seconds"`
	EvidenceSeconds         int   `json:"evidence_seconds"`
	DiagnosticSeconds       int   `json:"diagnostic_seconds"`
	InputBytes              int64 `json:"input_bytes"`
	MaxUploadBytes          int64 `json:"max_upload_bytes"`
	MaxManifestObjects      int   `json:"max_manifest_objects"`
	MaxManifestBytes        int64 `json:"max_manifest_bytes"`
	UploadIdleSeconds       int   `json:"upload_idle_seconds"`
	UploadConcurrency       int   `json:"upload_concurrency"`
	GlobalUploadConcurrency int   `json:"global_upload_concurrency"`
	MaxNodes                int   `json:"max_nodes"`
	NodeExecDefaultSeconds  int   `json:"node_exec_default_seconds"`
	NodeExecMaxSeconds      int   `json:"node_exec_max_seconds"`
	StdinPacketBytes        int   `json:"stdin_packet_bytes"`
	StdinPackets            int   `json:"stdin_packets"`
	StdinBytes              int   `json:"stdin_bytes"`
	OutputBytesPerStream    int64 `json:"output_bytes_per_stream"`
}
type ServiceView struct {
	EndpointAddress   string                 `json:"endpoint_address"`
	Protocol          wire.Protocol          `json:"protocol"`
	Build             wire.BuildIdentity     `json:"build"`
	Backends          map[string]BackendView `json:"backends"`
	Admission         AdmissionView          `json:"admission"`
	ActiveExecutionID string                 `json:"active_execution_id"`
	Limits            ServiceLimits          `json:"limits"`
}

// The caller holds s.mu; backend availability is an observation outside it.
func (s *Server) serviceView(ctx context.Context) ServiceView {
	backends := map[string]BackendView{}
	s.mu.Unlock()
	for _, kind := range []string{"lab", "nixos"} {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		reason, err := s.backend.Available(c, kind)
		cancel()
		view := BackendView{Available: err == nil && reason == "", WaitingReason: reason}
		if provider, ok := s.backend.(interface{ BackendIdentity(string) any }); ok {
			view.Identity = provider.BackendIdentity(kind)
		}
		if err != nil {
			view.Error = wire.Safe(err)
		}
		backends[kind] = view
	}
	s.mu.Lock()
	return ServiceView{
		EndpointAddress: s.cfg.VirtualBox.Network.Listen,
		Protocol:        wire.CurrentProtocol(), Build: wire.Identity(), Backends: backends,
		Admission: AdmissionView{Accepting: !s.closed && !s.storageBlocked, StorageBlocked: s.storageBlocked}, ActiveExecutionID: s.active,
		Limits: ServiceLimits{WaitSeconds: int(WaitWindow / time.Second), ToolTimeoutSeconds: 660, HeartbeatSeconds: int(TransportHeartbeat / time.Second),
			StartupParallelism: s.cfg.VirtualBox.StartupParallelism, StartupSeconds: s.cfg.VirtualBox.StartupTimeout.Seconds(), ShutdownSeconds: s.cfg.VirtualBox.ShutdownTimeout.Seconds(),
			ProbeIntervalSeconds: int(probeInterval / time.Second), ProbeTimeoutSeconds: int(probeTimeout / time.Second), UnavailableAfterSeconds: s.cfg.VirtualBox.UnavailableAfter.Seconds(), MaxActiveEndpoints: 128,
			EvidenceBytes: s.cfg.Storage.EvidenceBytes, EvidenceLowWaterBytes: s.cfg.Storage.LowWaterBytes, SuccessDays: s.cfg.Storage.SuccessDays, FailureDays: s.cfg.Storage.FailureDays, SummaryDays: s.cfg.Storage.SummaryDays,
			DefaultSeconds: s.cfg.Tasks.DefaultTimeout.Seconds(), MaxSeconds: s.cfg.Tasks.MaxDuration.Seconds(), CleanupSeconds: s.cfg.Tasks.CleanupTimeout.Seconds(),
			EvidenceSeconds: s.cfg.Tasks.EvidenceTimeout.Seconds(), DiagnosticSeconds: int(diagnosticBudget / time.Second), InputBytes: s.cfg.Storage.InputBytes,
			MaxUploadBytes: s.cfg.Storage.MaxUploadBytes, MaxManifestObjects: 100000, MaxManifestBytes: wire.MaxManifestBytes, UploadIdleSeconds: s.cfg.Storage.UploadIdle.Seconds(),
			UploadConcurrency: uploadConcurrency, GlobalUploadConcurrency: globalUploadConcurrency, MaxNodes: 64, NodeExecDefaultSeconds: 10, NodeExecMaxSeconds: 30,
			StdinPacketBytes: wire.InputPacketLimit, StdinPackets: wire.InputPacketCount, StdinBytes: wire.CommandInputLimit, OutputBytesPerStream: wire.CommandOutputLimit},
	}
}

type TemplateListRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type TemplateView struct {
	Name             string       `json:"name"`
	Kind             string       `json:"kind"`
	CPUs             int          `json:"cpus"`
	MemoryMiB        int          `json:"memory_mib"`
	MaxCPUs          int          `json:"max_cpus"`
	MaxMemoryMiB     int          `json:"max_memory_mib"`
	InputInterval    *float64     `json:"input_interval_seconds"`
	Media            config.Media `json:"media"`
	Cameras          []string     `json:"cameras"`
	ConfiguredSHA256 string       `json:"configured_sha256"`
}
type TemplatePage struct {
	Items      []TemplateView `json:"items"`
	NextCursor string         `json:"next_cursor"`
}

func (s *Server) templateList(a TemplateListRequest) TemplatePage {
	names := make([]string, 0, len(s.cfg.VirtualBox.Templates))
	for name := range s.cfg.VirtualBox.Templates {
		if name > a.Cursor {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	limit := a.Limit
	if limit == 0 {
		limit = 50
	}
	page := TemplatePage{Items: []TemplateView{}, NextCursor: a.Cursor}
	for _, name := range names[:min(limit, len(names))] {
		t := s.cfg.VirtualBox.Templates[name]
		cameras := []string{}
		for camera := range t.Cameras {
			cameras = append(cameras, camera)
		}
		sort.Strings(cameras)
		page.Items = append(page.Items, TemplateView{Name: name, Kind: t.Kind, CPUs: t.CPUs, MemoryMiB: t.MemoryMiB, MaxCPUs: t.MaxCPUs, MaxMemoryMiB: t.MaxMemoryMiB, InputInterval: t.InputInterval, Media: t.Media, Cameras: cameras, ConfiguredSHA256: configuredTemplateIdentity(t)})
		page.NextCursor = name
	}
	return page
}
