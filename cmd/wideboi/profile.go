package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/pprof"
)

// profilePath turns an env prefix into a per-process file:
// "/tmp/wb" -> "/tmp/wb.server.cpu.12345.pprof". Several clients attach
// at once, so the pid keeps their profiles apart. The kind ("cpu" or
// "mem") keeps a shared prefix for both env vars from writing both
// profiles to one file, where the heap profile would truncate the CPU one.
func profilePath(prefix, component, kind string, pid int) string {
	return fmt.Sprintf("%s.%s.%s.%d.pprof", prefix, component, kind, pid)
}

// startProfiles starts a CPU profile if WIDEBOI_CPUPROFILE is set and
// returns a stop func that also writes a heap profile if
// WIDEBOI_MEMPROFILE is set. Errors are logged, never fatal: profiling
// must not stop a session from starting.
//
// The stop func runs from a defer, and a signal's teardown re-raises
// past defers, so profiles are written on a normal exit only
// (kill-session, detach, quit).
func startProfiles(component string) func() {
	pid := os.Getpid()

	var cpuFile *os.File
	if prefix := os.Getenv("WIDEBOI_CPUPROFILE"); prefix != "" {
		path := profilePath(prefix, component, "cpu", pid)
		f, err := os.Create(path)
		if err != nil {
			slog.Error("cannot create CPU profile", "path", path, "err", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			slog.Error("cannot start CPU profile", "path", path, "err", err)
			f.Close()
		} else {
			cpuFile = f
			slog.Info("CPU profiling", "path", path)
		}
	}

	return func() {
		if cpuFile != nil {
			pprof.StopCPUProfile()
			if err := cpuFile.Close(); err != nil {
				slog.Error("cannot close CPU profile", "path", cpuFile.Name(), "err", err)
			}
		}
		if prefix := os.Getenv("WIDEBOI_MEMPROFILE"); prefix != "" {
			writeMemProfile(profilePath(prefix, component, "mem", pid))
		}
	}
}

// writeMemProfile writes the "allocs" profile, which carries cumulative
// allocations (alloc_space) as well as the live heap. The GC first
// brings the live-heap numbers up to date.
func writeMemProfile(path string) {
	f, err := os.Create(path)
	if err != nil {
		slog.Error("cannot create memory profile", "path", path, "err", err)
		return
	}
	runtime.GC()
	if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
		slog.Error("cannot write memory profile", "path", path, "err", err)
	}
	if err := f.Close(); err != nil {
		slog.Error("cannot close memory profile", "path", path, "err", err)
	}
}
