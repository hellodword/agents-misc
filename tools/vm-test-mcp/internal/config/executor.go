package config

import (
	"fmt"
	"time"
)

// Executor is fixed policy loaded from the shared configuration at an explicit
// administrator launch. Worker RPCs contain identities, never host settings.
type Executor struct {
	StateDir       string   `yaml:"state_dir"`
	Socket         string   `yaml:"socket"`
	WorkerUID      int      `yaml:"worker_uid"`
	Nix            string   `yaml:"executable"`
	StopGrace      Duration `yaml:"stop_grace"`
	ReleaseTimeout Duration `yaml:"release_timeout"`
}

func executorDefaults() Executor {
	return Executor{StateDir: "/var/lib/vm-test-nix-executor", Socket: "/run/vm-test-nix-executor/control.sock", WorkerUID: -1, StopGrace: Duration(30 * time.Second), ReleaseTimeout: Duration(5 * time.Minute)}
}

func LoadExecutor(file string) (Executor, error) {
	shared := Defaults()
	if err := decode(file, &shared); err != nil {
		return shared.Executor, err
	}
	c := shared.Executor
	if !absolute(c.StateDir) || !absolute(c.Socket) || !absolute(c.Nix) || c.WorkerUID <= 0 {
		return c, fmt.Errorf("executor requires absolute state_dir, socket, executable and a non-root worker_uid")
	}
	if c.StopGrace < 0 || c.ReleaseTimeout <= c.StopGrace || c.ReleaseTimeout > Duration(5*time.Minute) {
		return c, fmt.Errorf("executor release_timeout must exceed stop_grace and be at most 5m")
	}
	return c, nil
}
