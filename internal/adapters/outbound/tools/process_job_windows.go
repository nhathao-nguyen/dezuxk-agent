//go:build windows

package tools

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	modkernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modkernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modkernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = modkernel32.NewProc("TerminateJobObject")
	procOpenProcess              = modkernel32.NewProc("OpenProcess")
	procCloseHandle              = modkernel32.NewProc("CloseHandle")
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x2000
	processSetQuota                        = 0x0100
	processTerminate                       = 0x0001
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryLimit uintptr
	PeakJobMemoryLimit    uintptr
}

type processJobGroup struct {
	jobHandle syscall.Handle
}

// setupProcessGroup tạo Windows Job Object và áp dụng cờ JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
func setupProcessGroup(cmd *exec.Cmd) (*processJobGroup, error) {
	handle, _, err := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		return nil, err
	}

	jobHandle := syscall.Handle(handle)

	var info jobObjectExtendedLimitInformation
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose

	ret, _, err := procSetInformationJobObject.Call(
		uintptr(jobHandle),
		jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
	)
	if ret == 0 {
		_ = syscall.CloseHandle(jobHandle)
		return nil, err
	}

	return &processJobGroup{jobHandle: jobHandle}, nil
}

// attachProcess gán tiến trình vừa Start vào Job Object
func (g *processJobGroup) attachProcess(cmd *exec.Cmd) error {
	if g == nil || g.jobHandle == 0 || cmd.Process == nil {
		return nil
	}

	// Mở process handle với quyền PROCESS_SET_QUOTA | PROCESS_TERMINATE
	pHandle, _, err := procOpenProcess.Call(
		uintptr(processSetQuota|processTerminate),
		0,
		uintptr(cmd.Process.Pid),
	)
	if pHandle == 0 {
		return err
	}
	defer procCloseHandle.Call(pHandle)

	ret, _, err := procAssignProcessToJobObject.Call(
		uintptr(g.jobHandle),
		pHandle,
	)
	if ret == 0 {
		return err
	}
	return nil
}

// dispose đóng Job Handle hoặc buộc hủy toàn bộ cây tiến trình
func (g *processJobGroup) dispose(forceKill bool) {
	if g == nil || g.jobHandle == 0 {
		return
	}
	if forceKill {
		_, _, _ = procTerminateJobObject.Call(uintptr(g.jobHandle), 1)
	}
	_, _, _ = procCloseHandle.Call(uintptr(g.jobHandle))
	g.jobHandle = 0
}
