//go:build windows

package proc

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setup creates the child suspended and without a console window; run puts it in a job object
// before it executes a single instruction, so every process it starts belongs to the job too.
func setup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// run starts cmd inside a job object that kills the whole tree when it is closed: on cancel
// (TerminateJobObject) and when run returns, so no orphaned grandchild outlives the command.
func run(cmd *exec.Cmd) error {
	job, err := newKillOnCloseJob()
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(job) }()
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := adopt(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("configure job object: %w", err)
	}
	return job, nil
}

// adopt assigns the suspended process to job and resumes it.
func adopt(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	if r, _, _ := ntResumeProcess.Call(uintptr(h)); r != 0 {
		return errors.New("resume process: NTSTATUS " + fmt.Sprintf("%#x", r))
	}
	return nil
}
