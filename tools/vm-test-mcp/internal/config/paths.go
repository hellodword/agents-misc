package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Program resolves only administrator configuration, the installed package's
// bin directory and PATH. Request parameters never enter executable discovery.
func Program(override, name string) (string, error) {
	if override != "" {
		return override, nil
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			candidate := filepath.Join(filepath.Dir(real), name)
			if p, err := exec.LookPath(candidate); err == nil {
				return p, nil
			}
		}
	}
	return exec.LookPath(name)
}

func LibraryDir(override, executable string) (string, error) {
	if override != "" {
		return override, nil
	}
	exe, err := Program(executable, "VBoxManage")
	if err != nil {
		return "", err
	}
	return libraryBeside(exe)
}

func libraryBeside(exe string) (string, error) {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(real)
	for _, candidate := range []string{dir, filepath.Join(dir, "..", "lib", "virtualbox"), filepath.Join(dir, "..", "lib", "VirtualBox"), filepath.Join(dir, "..", "lib")} {
		candidate = filepath.Clean(candidate)
		if st, err := os.Stat(filepath.Join(candidate, "VBoxXPCOMC.so")); err == nil && st.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("VirtualBox runtime library not found beside executable; set virtualbox.library_dir")
}

func GuestBinary(kind string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	name := "vm-test-guest-" + kind + "-amd64"
	if kind == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), "..", "share", "vm-test-mcp", "guest", name)
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("guest supervisor is not a regular file")
	}
	return filepath.Clean(p), nil
}
