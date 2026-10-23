//go:build windows

package guest

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const DefaultRoot = `C:\ProgramData\vm-test\work`
const installPath = `C:\ProgramData\vm-test\vm-test-guest.exe`

var oobeComplete = windows.NewLazySystemDLL("kernel32.dll").NewProc("OOBEComplete")

//go:embed setup-windows.ps1
var setupWindows string

func Prepare(root string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("run preparation from an elevated administrator terminal")
	}
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\r\n\"") {
		return fmt.Errorf("invalid work directory")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "vm-test-prepare-*.ps1")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(setupWindows)
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	c := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", f.Name(), "-Binary", exe, "-Root", root)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func FirstBoot(root string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("firstboot requires an elevated token")
	}
	if err := os.WriteFile(filepath.Join(root, "prepared"), []byte(wire.Version), 0600); err != nil {
		return err
	}
	return nil
}

func readiness(root string) (bool, bool) {
	var heartbeat struct {
		Time       time.Time `json:"time"`
		Privileged bool      `json:"privileged"`
	}
	service := readJSON(filepath.Join(root, "service.json"), &heartbeat) == nil && heartbeat.Privileged && time.Since(heartbeat.Time) < 10*time.Second
	_, err := os.Stat(filepath.Join(root, "prepared"))
	if err != nil || oobeComplete.Find() != nil {
		return service, false
	}
	// The specialize hook runs before OOBE. Its temporary desktop can still
	// log off and terminate commands, even though our service is already up.
	// OOBEComplete does not require an interactive user or automatic login:
	// https://learn.microsoft.com/windows/win32/api/oobenotification/nf-oobenotification-oobecomplete
	var complete int32
	ok, _, _ := oobeComplete.Call(uintptr(unsafe.Pointer(&complete)))
	return service, ok != 0 && complete != 0
}

// Guest Control need not carry an elevated interactive token. A service
// installed explicitly by the administrator launches the supervised command.
func submit(ctx context.Context, root, request string) error {
	privileged, prepared := readiness(root)
	if !privileged || !prepared {
		return fmt.Errorf("prepared guest command service is unavailable")
	}
	var spec wire.CommandSpec
	if err := readJSON(request, &spec); err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if err := atomic(filepath.Join(root, "inbox", spec.ID+".json"), spec); err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(root, spec.ID, "status.json")); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

type commandService struct{ root string }

func Service(root string) error { return svc.Run("VMTestGuest", commandService{root: root}) }
func (s commandService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := os.MkdirAll(filepath.Join(s.root, "inbox"), 0700); err != nil {
		return false, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	var wg sync.WaitGroup
	defer wg.Wait()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				return false, 0
			}
		case <-ticker.C:
			_ = atomic(filepath.Join(s.root, "service.json"), map[string]any{"time": time.Now().UTC(), "privileged": windows.GetCurrentProcessToken().IsElevated()})
			items, err := os.ReadDir(filepath.Join(s.root, "inbox"))
			if err != nil {
				continue
			}
			for _, item := range items {
				id := strings.TrimSuffix(item.Name(), ".json")
				if item.IsDir() || !wire.IDPattern.MatchString(id) || item.Name() != id+".json" {
					continue
				}
				file := filepath.Join(s.root, "inbox", item.Name())
				var spec wire.CommandSpec
				if readJSON(file, &spec) != nil || spec.ID != id || spec.Validate() != nil {
					continue
				}
				claimed := file + ".claimed"
				if os.Rename(file, claimed) != nil {
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer os.Remove(claimed)
					// Use the logged-in administrator's elevated linked token when
					// available, allowing GUI programs on that desktop. Without a
					// login the LocalSystem service still executes CLI commands.
					if err := serviceCommand(ctx, s.root, claimed); err != nil {
						dir := filepath.Join(s.root, id)
						if _, err := os.Stat(filepath.Join(dir, "status.json")); os.IsNotExist(err) {
							_ = os.MkdirAll(dir, 0700)
							_ = os.WriteFile(filepath.Join(dir, "stdout"), nil, 0600)
							_ = os.WriteFile(filepath.Join(dir, "stderr"), nil, 0600)
							_ = atomic(filepath.Join(dir, "status.json"), wire.CommandStatus{State: "failed", Error: "Guest command service could not launch supervisor"})
						}
					}
				}()
			}
		}
	}
}

func serviceCommand(ctx context.Context, root, request string) error {
	token, err := desktopToken()
	if err != nil {
		return exec.CommandContext(ctx, installPath, "execute", "--root", root, "--request", request).Run()
	}
	defer token.Close()
	env, err := token.Environ(false)
	if err != nil {
		return err
	}
	var block []uint16
	for _, v := range env {
		block = append(block, utf16.Encode([]rune(v))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	app, _ := windows.UTF16PtrFromString(installPath)
	args := []string{installPath, "execute", "--root", root, "--request", request}
	for i, arg := range args {
		args[i] = syscall.EscapeArg(arg)
	}
	line, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return err
	}
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	pi := windows.ProcessInformation{}
	if err = windows.CreateProcessAsUser(token, app, line, nil, nil, false, windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, &block[0], nil, &si, &pi); err != nil {
		return err
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)
	for {
		result, err := windows.WaitForSingleObject(pi.Process, 100)
		if err != nil {
			return err
		}
		if result == windows.WAIT_OBJECT_0 {
			var exit uint32
			if err = windows.GetExitCodeProcess(pi.Process, &exit); err != nil {
				return err
			}
			if exit != 0 {
				return fmt.Errorf("command supervisor exited unsuccessfully")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			_ = windows.TerminateProcess(pi.Process, 1)
			return ctx.Err()
		default:
		}
	}
}

func desktopToken() (windows.Token, error) {
	var token windows.Token
	if err := windows.WTSQueryUserToken(windows.WTSGetActiveConsoleSessionId(), &token); err != nil {
		return 0, err
	}
	if token.IsElevated() {
		return token, nil
	}
	linked, err := token.GetLinkedToken()
	token.Close()
	if err != nil {
		return 0, err
	}
	if !linked.IsElevated() {
		linked.Close()
		return 0, fmt.Errorf("desktop user is not an administrator")
	}
	return linked, nil
}
