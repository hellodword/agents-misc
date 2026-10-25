package guest

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func environmentContract() map[string]any {
	contexts := []string{"service"}
	identity := "root"
	variables := []string{"HOME", "USER", "LOGNAME", "PATH"}
	inherit := "system_service_manager"
	if runtime.GOOS == "windows" {
		contexts = append(contexts, "desktop")
		identity = "LocalSystem; desktop explicitly selects the logged-in administrator's elevated token"
		variables = []string{"USERPROFILE", "PATH", "SYSTEMROOT", "TEMP", "TMP"}
		inherit = "selected_token_environment"
	}
	return map[string]any{"contexts": contexts, "default_context": "service", "identity": identity, "default_cwd": "selected account home directory", "default_variables": variables, "inheritance": inherit, "override": "env replaces named variables; cwd is absolute; no login shell", "observed": "command_get.execution records actual identity, cwd and variable names"}
}

func commandEnvironment(spec wire.CommandSpec) (wire.CommandSpec, *wire.CommandExecution, error) {
	u, err := user.Current()
	if err != nil {
		return spec, nil, fmt.Errorf("resolve execution identity: %w", err)
	}
	// LookupId reads the account database on Linux, so a service's missing HOME
	// or inherited USER cannot become the effective account's home or name.
	if runtime.GOOS == "linux" {
		u, err = user.LookupId(u.Uid)
		if err != nil {
			return spec, nil, fmt.Errorf("resolve account defaults: %w", err)
		}
	}
	return resolveEnvironment(spec, runtime.GOOS, u.Username, u.HomeDir, os.Environ())
}

func resolveEnvironment(spec wire.CommandSpec, platform, identity, home string, inherited []string) (wire.CommandSpec, *wire.CommandExecution, error) {
	if spec.Context == "" {
		spec.Context = "service"
	}
	if spec.Context == "desktop" && platform != "windows" {
		return spec, nil, wire.Fail("unsupported_context", "desktop context is only available for Windows; Linux desktop fixtures must select their user/session explicitly")
	}
	normalize := func(k string) string {
		if platform == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	env := map[string]string{}
	for _, entry := range inherited {
		k, v, ok := strings.Cut(entry, "=")
		if ok && k != "" {
			env[normalize(k)] = v
		}
	}
	if platform == "linux" {
		// These override the supervisor's system-service environment; the
		// remaining variables (for example LANG) stay inherited.
		path := env["PATH"]
		if path == "" {
			path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		}
		env = map[string]string{"HOME": home, "USER": identity, "LOGNAME": identity, "PATH": path}
	} else if env["USERPROFILE"] == "" {
		env["USERPROFILE"] = home
	}
	seen := map[string]bool{}
	for k, v := range spec.Env {
		k = normalize(k)
		if seen[k] {
			return spec, nil, wire.Fail("invalid_argument", "Environment keys must be unique ignoring case on Windows")
		}
		seen[k] = true
		env[k] = v
	}
	if spec.Cwd == "" {
		spec.Cwd = home
	}
	// Path syntax must be checked on the guest's actual OS. The resolver's pure
	// cross-platform tests cover environment rules independently of local paths.
	if platform == runtime.GOOS && (!filepath.IsAbs(spec.Cwd) || spec.Cwd == "") {
		return spec, nil, wire.Fail("invalid_argument", "cwd must resolve to an absolute guest directory")
	}
	spec.Env = env
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	inherit := "system_service_manager"
	if platform == "windows" {
		inherit = "selected_token_environment"
	}
	return spec, &wire.CommandExecution{Context: spec.Context, Identity: identity, Cwd: spec.Cwd, Inherited: inherit, EnvironmentKeys: keys}, nil
}
