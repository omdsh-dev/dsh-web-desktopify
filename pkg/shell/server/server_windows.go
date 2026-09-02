//go:build windows

package server

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Name 是壳同目录下 SEA 后端可执行文件名。Windows 上必须带 .exe：
// exec.Command 对直接路径不会像 LookPath 那样自动补扩展名。
const Name = "dsh-server.exe"

// 创建子进程标志。CREATE_NEW_PROCESS_GROUP 让后端自成进程组（与壳进程组
// 隔离，壳退出信号不会误伤）；CREATE_NO_WINDOW 防止 SEA 是控制台程序时
// 弹出黑窗（壳以 GUI 子系统运行）。
const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

// command 构造 spawn dsh-server 的命令。Windows 无用户 shell rc 概念，直接
// exec；Job Object 关联由 attachToJob 在 Start 成功后完成。
func command(exeDir, profile, port, dshHome string) *exec.Cmd {
	server := filepath.Join(exeDir, Name)
	cmd := exec.Command(server, "--profile", profile, "--port", port)
	cmd.Env = os.Environ()
	if dshHome != "" {
		cmd.Env = append(cmd.Env, "DSH_HOME="+dshHome)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
	log.Printf("server: exec: %s", cmd.String())
	return cmd
}

// attachToJob 把 dsh-server 放入 Job Object（KILL_ON_JOB_CLOSE）：壳退出
// （含异常退出、句柄随进程关闭）时作业树整体终止，后端及其子进程一并清理，
// 不留孤儿 node。句柄保存在 p.job，由 Process.done 释放。
func attachToJob(p *Process) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return err
	}
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(p.cmd.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		return err
	}
	err = windows.AssignProcessToJobObject(job, proc)
	windows.CloseHandle(proc)
	if err != nil {
		windows.CloseHandle(job)
		return err
	}
	p.job = uintptr(job)
	p.cleanup = func() { windows.CloseHandle(job) }
	return nil
}

// terminateJob 终止 Job Object 内全部进程（Windows 无 POSIX 信号，SEA node
// 收不到优雅 SIGTERM，直接强杀全树）。
func terminateJob(p *Process) {
	if p.job == 0 || p.cmd.Process == nil {
		return
	}
	_ = windows.TerminateJobObject(windows.Handle(p.job), 1)
}

// requestStop 终止后端：Windows 上即强杀全树（无优雅信号可用）。
func requestStop(p *Process) {
	terminateJob(p)
}

// forceStop 兜底强杀（与 requestStop 同效，幂等）。
func forceStop(p *Process) {
	terminateJob(p)
}
