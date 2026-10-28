//go:build windows

package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/windows"
)

type windowsChild struct {
	process, job windows.Handle
	readers      []*os.File
	streams      *outputStreams
	done         chan struct{}
	stopWait     context.CancelFunc
	exit         int
	err          error
}

// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, from winnt.h: four LARGE_INTEGERs
// followed by four DWORDs. ActiveProcesses confirms the whole job is empty.
type jobAccounting struct {
	TotalUser, TotalKernel, PeriodUser, PeriodKernel                 int64
	PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
}

func launch(ctx context.Context, s wire.CommandSpec, in *os.File, out, errout io.Writer) (Child, error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	p := &windowsChild{job: job}
	ok := false
	defer func() {
		if !ok {
			p.Close()
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return nil, e
	}
	ri := in
	defer windows.SetHandleInformation(windows.Handle(ri.Fd()), windows.HANDLE_FLAG_INHERIT, 0)
	ro, wo, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer wo.Close()
	re, we, e := os.Pipe()
	if e != nil {
		ro.Close()
		return nil, e
	}
	defer we.Close()
	p.readers = []*os.File{ro, re}
	for _, f := range []*os.File{ri, wo, we} {
		if e = windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
			ro.Close()
			re.Close()
			return nil, e
		}
	}
	attrs, e := windows.NewProcThreadAttributeList(1)
	if e != nil {
		return nil, e
	}
	defer attrs.Delete()
	handles := []windows.Handle{windows.Handle(ri.Fd()), windows.Handle(wo.Fd()), windows.Handle(we.Fd())}
	if e = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); e != nil {
		return nil, e
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = handles[0]
	si.StdOutput = handles[1]
	si.StdErr = handles[2]
	si.ProcThreadAttributeList = attrs.List()
	exe, e := commandExecutable(s.Argv[0], s.Cwd, s.Env)
	if e != nil {
		return nil, e
	}
	app, e := windows.UTF16PtrFromString(exe)
	if e != nil {
		return nil, e
	}
	args := []string{}
	for _, a := range s.Argv {
		args = append(args, syscall.EscapeArg(a))
	}
	line, e := windows.UTF16PtrFromString(strings.Join(args, " "))
	if e != nil {
		return nil, e
	}
	env := map[string]string{}
	for k, v := range s.Env {
		env[strings.ToUpper(k)] = v
	}
	keys := []string{}
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var block []uint16
	for _, k := range keys {
		block = append(block, utf16.Encode([]rune(k+"="+env[k]))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	var cwd *uint16
	if s.Cwd != "" {
		cwd, e = windows.UTF16PtrFromString(s.Cwd)
		if e != nil {
			return nil, e
		}
	}
	pi := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e = windows.CreateProcess(app, line, nil, nil, true, flags, &block[0], cwd, &si.StartupInfo, &pi); e != nil {
		ro.Close()
		re.Close()
		return nil, e
	}
	p.process = pi.Process
	defer windows.CloseHandle(pi.Thread)
	// The process cannot execute a single instruction until it belongs to the
	// kill-on-close job. Assignment failure terminates the suspended process.
	if e = windows.AssignProcessToJobObject(job, pi.Process); e != nil {
		windows.TerminateProcess(pi.Process, 1)
		ro.Close()
		re.Close()
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if _, e = windows.ResumeThread(pi.Thread); e != nil {
		windows.TerminateJobObject(job, 1)
		ro.Close()
		re.Close()
		return nil, e
	}
	p.streams = drainStreams(ro, re, out, errout)
	p.done = make(chan struct{})
	waitCtx, stopWait := context.WithCancel(context.Background())
	p.stopWait = stopWait
	// This goroutine owns the process handle until its finite wait returns.
	// Close never closes a handle underneath WaitForSingleObject.
	go func() {
		defer close(p.done)
		defer windows.CloseHandle(p.process)
		p.exit, p.err = waitProcess(waitCtx, p.process)
	}()
	ok = true
	return p, nil
}
func waitProcess(ctx context.Context, handle windows.Handle) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		result, err := windows.WaitForSingleObject(handle, 50)
		if err != nil {
			return -1, err
		}
		if result == windows.WAIT_OBJECT_0 {
			var code uint32
			if err = windows.GetExitCodeProcess(handle, &code); err != nil {
				return -1, err
			}
			return int(code), nil
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			return -1, fmt.Errorf("unexpected process wait result: %d", result)
		}
	}
}
func (p *windowsChild) Wait(ctx context.Context) (processExit, error) {
	select {
	case <-p.done:
		if p.exit >= 0 {
			return exitedWithCode(p.exit), p.err
		}
		return processExit{}, p.err
	case <-ctx.Done():
		return processExit{}, ctx.Err()
	}
}
func (p *windowsChild) Kill(ctx context.Context) error {
	failure := func(delivered bool, err error) error {
		details := map[string]any{"request_delivered": delivered, "cause": wire.Safe(err)}
		var code syscall.Errno
		if errors.As(err, &code) {
			details["system_code"] = uint32(code)
		}
		return &wire.Error{Code: "termination_unconfirmed", Message: "Windows Job termination could not be confirmed", Details: details}
	}
	var job windows.Handle
	current := windows.CurrentProcess()
	if err := windows.DuplicateHandle(current, p.job, current, &job, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return failure(false, err)
	}
	defer windows.CloseHandle(job)
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return failure(false, err)
	}
	for {
		var info jobAccounting
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			return failure(true, err)
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return failure(true, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (p *windowsChild) Drain(ctx context.Context) error { return p.streams.Wait(ctx) }
func (p *windowsChild) Close() error {
	if p.stopWait != nil {
		p.stopWait()
	}
	for _, r := range p.readers {
		r.Close()
	}
	if p.process != 0 && p.stopWait == nil {
		windows.CloseHandle(p.process)
	}
	if p.job != 0 {
		return windows.CloseHandle(p.job)
	}
	return nil
}
func Reboot() error { return exec.Command("shutdown.exe", "/r", "/t", "0").Start() }
