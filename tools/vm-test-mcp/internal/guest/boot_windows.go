//go:build windows

package guest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// LastBootUpTime is supplied by the OS, independently of guest service restarts.
// https://learn.microsoft.com/windows/win32/cimwin32prov/win32-operatingsystem
func bootIdentity() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance -ClassName Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().Ticks").Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("OS boot identity is unavailable")
	}
	return value, nil
}
