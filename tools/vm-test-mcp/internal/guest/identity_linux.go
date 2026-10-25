//go:build linux

package guest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

var bootIdentity = sync.OnceValues(func() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data)), err
})

func AllowProcessObservation(account string) error {
	if account != "" {
		return fmt.Errorf("control-user applies only to Windows service processes")
	}
	return nil
}

func processIdentity(pid int) (wire.ProcessIdentity, bool, error) {
	var id wire.ProcessIdentity
	if pid < 1 {
		return id, false, fmt.Errorf("invalid process ID")
	}
	boot, err := bootIdentity()
	if err != nil {
		return id, false, err
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return id, false, err
	}
	// The comm field may contain spaces and parentheses. Fields after its final
	// closing parenthesis begin at state (field 3); starttime is field 22.
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return id, false, fmt.Errorf("invalid process identity")
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) < 20 {
		return id, false, fmt.Errorf("short process identity")
	}
	if _, err = strconv.ParseUint(fields[19], 10, 64); err != nil {
		return id, false, err
	}
	h := sha256.Sum256([]byte(boot + "/" + fields[19]))
	id = wire.ProcessIdentity{PID: pid, Start: hex.EncodeToString(h[:])}
	return id, fields[0] != "Z" && fields[0] != "X", nil
}
