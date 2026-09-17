package deps

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindNativeBashWindows(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "aice", "bin")
	windows := filepath.Join(root, "Windows")
	local := filepath.Join(root, "Local")
	programs := filepath.Join(root, "Program Files")
	wsl := filepath.Join(windows, "System32", "bash.exe")
	alias := filepath.Join(local, "Microsoft", "WindowsApps", "bash.exe")
	git := filepath.Join(root, "Custom Git", "cmd", "git.exe")
	native := filepath.Join(root, "Custom Git", "bin", "bash.exe")
	cached := filepath.Join(bin, "git", "bin", "bash.exe")
	standard := filepath.Join(programs, "Git", "bin", "bash.exe")
	userGit := filepath.Join(local, "Programs", "Git", "bin", "bash.exe")
	msys := filepath.Join(root, "msys64", "usr", "bin", "bash.exe")
	for _, test := range []struct {
		name  string
		path  []string
		files []string
		git   string
		want  string
	}{
		{name: "WSL only", path: []string{filepath.Dir(wsl)}, files: []string{wsl}},
		{name: "WindowsApps only", path: []string{filepath.Dir(alias)}, files: []string{alias}},
		{name: "native after WSL and alias", path: []string{filepath.Dir(wsl), filepath.Dir(alias), filepath.Dir(native)}, files: []string{wsl, alias, native}, want: native},
		{name: "cached before PATH", path: []string{filepath.Dir(native)}, files: []string{cached, native}, want: cached},
		{name: "managed Git before other installs", path: []string{filepath.Dir(msys)}, files: []string{cached, standard, msys, filepath.Join(bin, "bash.exe")}, want: cached},
		{name: "standard Git before PATH Bash", path: []string{filepath.Dir(msys)}, files: []string{standard, msys}, want: standard},
		{name: "Git from cmd before PATH Bash", path: []string{filepath.Dir(msys), filepath.Dir(git)}, files: []string{native, msys}, git: git, want: native},
		{name: "per user Git before PATH Bash", path: []string{filepath.Dir(msys)}, files: []string{userGit, msys}, want: userGit},
		{name: "other native Bash fallback", path: []string{filepath.Dir(msys)}, files: []string{msys}, want: msys},
		{name: "Git cmd entry", path: []string{filepath.Dir(wsl), filepath.Dir(git)}, files: []string{wsl, native}, git: git, want: native},
		{name: "standard install outside PATH", files: []string{standard}, want: standard},
		{name: "per user install", files: []string{userGit}, want: userGit},
		{name: "quoted PATH", path: []string{`"` + filepath.Dir(native) + `"`}, files: []string{native}, want: native},
		{name: "relative PATH ignored", path: []string{".", "relative"}, files: []string{"bash.exe", filepath.Join("relative", "bash.exe")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{
				"PATH": strings.Join(test.path, ";"), "SystemRoot": windows,
				"LocalAppData": local, "ProgramFiles": programs,
			}
			opts := Options{Goos: "windows", BinDir: bin, Getenv: func(key string) string { return env[key] }}
			opts.LookPath = func(name string) (string, error) {
				if name == wsl || name == alias {
					t.Fatalf("resolver attempted to use WSL launcher %q", name)
				}
				if name == "git" && test.git != "" {
					return test.git, nil
				}
				for _, file := range test.files {
					if name == file {
						return file, nil
					}
				}
				return "", exec.ErrNotFound
			}
			got, err := FindNativeBash(opts)
			if test.want == "" {
				if err == nil || !strings.Contains(err.Error(), "winget install Git.Git") {
					t.Fatalf("FindNativeBash() = %q, %v, want Git Bash installation guidance", got, err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("FindNativeBash() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestFindNativeBashUnixUsesPATH(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			for _, lookupErr := range []error{nil, exec.ErrNotFound} {
				got, err := FindNativeBash(Options{Goos: goos, LookPath: func(name string) (string, error) {
					if name != "bash" {
						t.Fatalf("LookPath(%q), want bash", name)
					}
					return "/bin/bash", lookupErr
				}})
				if got != "/bin/bash" || !errors.Is(err, lookupErr) {
					t.Fatalf("FindNativeBash() = %q, %v", got, err)
				}
			}
		})
	}
}

func TestIsWSLBash(t *testing.T) {
	t.Parallel()
	getenv := func(key string) string {
		switch key {
		case "SystemRoot":
			return `D:\WinNT`
		case "WINDIR":
			return `D:\WinNT`
		case "LocalAppData":
			return `C:\Users\Test\AppData\Local`
		default:
			return ""
		}
	}
	for _, test := range []struct {
		name, path string
		want       bool
	}{
		{"system32 case insensitive", `d:\WINNT\SYSTEM32\BASH.EXE`, true},
		{"sysnative", `D:/WinNT/Sysnative/bash.exe`, true},
		{"syswow64", `D:\WinNT\SysWOW64\bash.exe`, true},
		{"clean path", `D:/WinNT/System32/../System32/bash.exe`, true},
		{"default system root", `C:\Windows\System32\bash.exe`, true},
		{"app alias", `C:\Users\Test\AppData\Local\Microsoft\WindowsApps\bash.exe`, true},
		{"package alias", `C:\Users\Test\AppData\Local\Microsoft\WindowsApps\Microsoft.WSL_123\bash.exe`, true},
		{"Git Bash", `C:\Program Files\Git\bin\bash.exe`, false},
		{"MSYS2 Bash", `C:\msys64\usr\bin\bash.exe`, false},
		{"similar directory name", `C:\Users\Test\AppData\Local\Microsoft\WindowsApps-tools\bash.exe`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isWSLBash(test.path, getenv); got != test.want {
				t.Fatalf("isWSLBash(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

func TestWSLBashPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	windows := filepath.Join(root, "Windows")
	local := filepath.Join(root, "Local")
	wsl := filepath.Join(windows, "System32", "bash.exe")
	alias := filepath.Join(local, "Microsoft", "WindowsApps", "bash.exe")
	native := filepath.Join(root, "Git", "bin", "bash.exe")
	opts := Options{
		Goos: "windows", BinDir: filepath.Join(root, "bin"),
		Getenv: func(key string) string {
			switch key {
			case "SystemRoot":
				return windows
			case "LocalAppData":
				return local
			case "PATH":
				return strings.Join([]string{filepath.Dir(native), filepath.Dir(wsl), filepath.Dir(wsl), `"` + filepath.Dir(alias) + `"`, "relative"}, ";")
			default:
				return ""
			}
		},
		LookPath: func(name string) (string, error) {
			if name == wsl || name == alias {
				return name, nil
			}
			t.Fatalf("unexpected candidate %q", name)
			return "", exec.ErrNotFound
		},
	}
	paths := WSLBashPaths(opts)
	if len(paths) != 2 || paths[0] != wsl || paths[1] != alias {
		t.Fatalf("paths = %v", paths)
	}
	opts.Goos = "linux"
	if paths := WSLBashPaths(opts); len(paths) != 0 {
		t.Fatalf("non-Windows paths = %v", paths)
	}
}
