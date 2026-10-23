package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve against the command environment, not the long-lived service's PATH.
// A test can install optional tooling and supply PATH without restarting a VM.
func commandExecutable(name, cwd string, env map[string]string) (string, error) {
	value := func(key string) string {
		for k, v := range env {
			if k == key || runtime.GOOS == "windows" && strings.EqualFold(k, key) {
				return v
			}
		}
		return os.Getenv(key)
	}
	exts := []string{""}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		ext := value("PATHEXT")
		if ext == "" {
			ext = ".COM;.EXE;.BAT;.CMD"
		}
		exts = strings.Split(ext, ";")
	}
	dirs := filepath.SplitList(value("PATH"))
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		dirs = []string{""}
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		for _, ext := range exts {
			candidate := p + ext
			if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode()&0111 != 0) {
				return filepath.Abs(candidate)
			}
		}
	}
	return "", fmt.Errorf("executable is unavailable in command PATH")
}
