package forkexec

import (
	"syscall"
	"unsafe" // required for go:linkname.

	"golang.org/x/sys/unix"
)

// Start will fork, load seccomp and execve and being traced by ptrace
// Return pid and potential error
// The runtime OS thread must be locked before calling this function
// if ptrace is set to true
func (r *Runner) Start() (int, error) {
	// prepare work dir
	workdir, err := syscallStringFromString(r.WorkDir)
	if err != nil {
		return 0, err
	}

	// prepare hostname
	hostname, err := syscallStringFromString(r.HostName)
	if err != nil {
		return 0, err
	}

	// prepare domainname
	domainname, err := syscallStringFromString(r.DomainName)
	if err != nil {
		return 0, err
	}

	// prepare pivot_root param
	pivotRoot, err := syscallStringFromString(r.PivotRoot)
	if err != nil {
		return 0, err
	}

	// socketpair p used to notify child the uid / gid mapping have been setup
	// socketpair p is also used to sync with parent before final execve
	// p[0] is used by parent and p[1] is used by child
	p, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}

	// fork in child
	pid, err1 := forkAndExecInChild(r, r.ExecPath, workdir, hostname, domainname, pivotRoot, p)

	// restore all signals
	afterFork()
	syscall.ForkLock.Unlock()

	return syncWithChild(r, p, int(pid), err1)
}

func syncWithChild(r *Runner, p [2]int, pid int, err1 syscall.Errno) (int, error) {
	var (
		r1          uintptr
		err2        syscall.Errno
		err         error
		unshareUser = r.CloneFlags&unix.CLONE_NEWUSER == unix.CLONE_NEWUSER
		childErr    ChildError
	)

	// sync with child
	unix.Close(p[1])

	// clone syscall failed
	if err1 != 0 {
		unix.Close(p[0])
		childErr.Location = LocClone
		childErr.Err = err1
		return 0, childErr
	}

	// synchronize with child for uid / gid map
	if unshareUser {
		if err = writeIDMaps(r, int(pid)); err != nil {
			err2 = err.(syscall.Errno)
		}
		syscall.RawSyscall(syscall.SYS_WRITE, uintptr(p[0]), uintptr(unsafe.Pointer(&err2)), uintptr(unsafe.Sizeof(err2)))
	}

	n, err := readChildErr(p[0], &childErr)
	// child returned error code
	if (n != int(unsafe.Sizeof(err2)) && n != int(unsafe.Sizeof(childErr))) || childErr.Err != 0 || err != nil {
		childErr.Err = handlePipeError(r1, childErr.Err)
		goto fail
	}

	// if syncfunc return error, then fail child immediately
	if r.SyncFunc != nil {
		if err = r.SyncFunc(int(pid)); err != nil {
			goto fail
		}
	}
	// otherwise, ack child (err1 == 0)
	syscall.RawSyscall(syscall.SYS_WRITE, uintptr(p[0]), uintptr(unsafe.Pointer(&err1)), uintptr(unsafe.Sizeof(err1)))

	// if stopped before execve by signal SIGSTOP or PTRACE_ME, then do not wait until execve
	if r.Ptrace || r.StopBeforeSeccomp {
		// let's wait it in another goroutine to avoid SIGPIPE
		go func() {
			readChildErr(p[0], &childErr)
			unix.Close(p[0])
		}()
		return int(pid), nil
	}

	// if read anything mean child failed after sync (close_on_exec so it should not block)
	n, err = readChildErr(p[0], &childErr)
	unix.Close(p[0])
	if n != 0 || err != nil {
		childErr.Err = handlePipeError(r1, childErr.Err)
		goto failAfterClose
	}
	return int(pid), nil

fail:
	unix.Close(p[0])

failAfterClose:
	handleChildFailed(int(pid))
	if childErr.Err == 0 {
		return 0, err
	}
	return 0, childErr
}

func readChildErr(fd int, childErr *ChildError) (n int, err error) {
	for {
		n, err = readlen(fd, (*byte)(unsafe.Pointer(childErr)), int(unsafe.Sizeof(*childErr)))
		if err != syscall.EINTR {
			break
		}
	}
	return
}

// https://cs.opensource.google/go/go/+/refs/tags/go1.18.1:src/syscall/zsyscall_linux_amd64.go;l=944
func readlen(fd int, p *byte, np int) (n int, err error) {
	r0, _, e1 := syscall.Syscall(syscall.SYS_READ, uintptr(fd), uintptr(unsafe.Pointer(p)), uintptr(np))
	n = int(r0)
	if e1 != 0 {
		err = syscall.Errno(e1)
	}
	return
}

// check pipe error
func handlePipeError(r1 uintptr, errno syscall.Errno) syscall.Errno {
	if r1 >= unsafe.Sizeof(errno) {
		return syscall.Errno(errno)
	}
	return syscall.EPIPE
}

func handleChildFailed(pid int) {
	var wstatus syscall.WaitStatus
	// make sure not blocked
	syscall.Kill(pid, syscall.SIGKILL)
	// child failed; wait for it to exit, to make sure the zombies don't accumulate
	_, err := syscall.Wait4(pid, &wstatus, 0, nil)
	for err == syscall.EINTR {
		_, err = syscall.Wait4(pid, &wstatus, 0, nil)
	}
}
