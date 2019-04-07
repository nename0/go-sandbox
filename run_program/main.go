package main

import (
	"flag"
	"fmt"
	"log"
	"syscall"

	tracer "github.com/criyle/go-judger/tracer"
	libseccomp "github.com/seccomp/libseccomp-golang"
)

func handle(ctx *tracer.Context) tracer.TraceAction {
	syscallNo := ctx.SyscallNo()
	syscallName, err := libseccomp.ScmpSyscall(syscallNo).GetName()
	log.Println("syscall: ", syscallNo, syscallName, err)
	switch syscallName {
	case "open":
		fileptr := ctx.Arg0()
		file := ctx.GetString(fileptr)
		log.Println("open: ", file)
		if file == "1" {
			ctx.SetReturnValue(-int(syscall.EPERM))
			return tracer.TraceBan
		}
	case "access":
		fileptr := ctx.Arg0()
		file := ctx.GetString(fileptr)
		log.Println("access: ", file)
	case "execve":
		fileptr := ctx.Arg0()
		file := ctx.GetString(fileptr)
		log.Println("execve: ", file)
	}
	return tracer.TraceAllow
}

type arrayFlags []string

func (f *arrayFlags) String() string {
	return fmt.Sprint([]string(*f))
}

func (f *arrayFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// TODO: syscall handle, file access checker
func main() {
	var (
		addReadable, addWritable       arrayFlags
		addRawReadable, addRawWritable arrayFlags
		allowProc                      bool
	)

	t := tracer.NewTracer()
	flag.Uint64Var(&t.TimeLimit, "tl", 1, "Set time limit (in second)")
	flag.Uint64Var(&t.RealTimeLimit, "rtl", 1, "Set real time limit (in second)")
	flag.Uint64Var(&t.MemoryLimit, "ml", 256, "Set memory limit (in mb)")
	flag.Uint64Var(&t.OutputLimit, "ol", 64, "Set output limit (in mb)")
	flag.Uint64Var(&t.StackLimit, "sl", 1024, "Set stack limit (in mb)")
	flag.StringVar(&t.InputFileName, "in", "", "Set input file name")
	flag.StringVar(&t.OutputFileName, "out", "", "Set output file name")
	flag.StringVar(&t.ErrorFileName, "err", "", "Set error file name")
	flag.StringVar(&t.WorkPath, "work-path", "", "Set the work path of the program")
	_ = flag.String("type", "", "Set the program type (for some program such as python)")
	_ = flag.String("res", "", "Set the file name for output the result")
	flag.Var(&addReadable, "add-readable", "Add a readable file")
	flag.Var(&addWritable, "add-writable", "Add a writable file")
	flag.BoolVar(&t.Unsafe, "unsafe", false, "Don't check dangerous syscalls")
	flag.BoolVar(&t.ShowDetails, "show-trace-details", false, "Show trace details")
	flag.BoolVar(&allowProc, "allow-proc", false, "Allow fork, exec... etc.")
	flag.Var(&addRawReadable, "add-readable-raw", "Add a readable file (don't transform to its real path)")
	flag.Var(&addRawWritable, "add-writable-raw", "Add a writable file (don't transform to its real path)")

	flag.Parse()

	t.Args = flag.Args()
	t.TraceHandle = handle
	//t.Debug = true
	rt, err := t.StartTrace()
	log.Println(rt, err)
}