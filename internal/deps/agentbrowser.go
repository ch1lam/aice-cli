package deps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AgentBrowserInstalled checks the private pinned installation, never PATH.
func AgentBrowserInstalled(binDir string) bool {
	version, err := os.ReadFile(filepath.Join(binDir, "agent-browser.version"))
	if err != nil || strings.TrimSpace(string(version)) != AgentBrowserVersion {
		return false
	}
	info, err := os.Stat(filepath.Join(binDir, "agent-browser"))
	return err == nil && info.Mode().IsRegular()
}

func installAgentBrowser(ctx context.Context, opts Options) error {
	asset, ok := agentBrowserAsset[opts.Goos+"/"+opts.Goarch]
	if !ok {
		return fmt.Errorf("unsupported browser platform %s/%s", opts.Goos, opts.Goarch)
	}
	if err := os.MkdirAll(opts.BinDir, 0755); err != nil {
		return err
	}
	unlock, err := lockBrowserInstall(ctx, opts)
	if err != nil {
		return err
	}
	defer unlock()
	if AgentBrowserInstalled(opts.BinDir) {
		return nil
	}
	fmt.Fprintf(opts.Log, "aice: installing browser helper agent-browser %s (~13 MB) into %s ...\n", AgentBrowserVersion, opts.BinDir)
	archive, err := download(ctx, opts, agentBrowserDownloadURL(opts.BaseURL, asset), agentBrowserSHA256[opts.Goos+"/"+opts.Goarch], "")
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	stage, err := os.MkdirTemp(opts.BinDir, ".agent-browser-")
	if err != nil {
		return err
	}
	defer func() {
		// Failed rollback backups must remain available for manual recovery.
		backups, _ := filepath.Glob(filepath.Join(stage, "old-*"))
		if len(backups) == 0 {
			os.RemoveAll(stage)
		}
	}()
	binary := filepath.Join(stage, "agent-browser")
	// Stage on the destination filesystem before the atomic rename.
	if err := moveFile(archive, binary); err != nil {
		return err
	}
	if err := os.Chmod(binary, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "version"), []byte(AgentBrowserVersion+"\n"), 0644); err != nil {
		return err
	}
	// Keep old files until every replacement succeeds, including the version marker.
	replacements := []browserReplacement{
		{from: binary, to: filepath.Join(opts.BinDir, "agent-browser")},
		{from: filepath.Join(stage, "version"), to: filepath.Join(opts.BinDir, "agent-browser.version")},
	}
	if err := replaceBrowserFiles(stage, replacements); err != nil {
		return err
	}
	// Earlier versions extracted CLI skill documents; the MCP tools replace them.
	if err := os.RemoveAll(filepath.Join(opts.BinDir, "agent-browser-skills")); err != nil {
		fmt.Fprintf(opts.Log, "aice: warning: remove old browser skills: %v\n", err)
	}
	os.RemoveAll(stage)
	fmt.Fprintf(opts.Log, "aice: installed agent-browser %s\n", AgentBrowserVersion)
	return nil
}

type browserReplacement struct {
	from, to, backup string
	published        bool
}

func replaceBrowserFiles(stage string, files []browserReplacement) (err error) {
	defer func() {
		if err == nil {
			return
		}
		for i := len(files) - 1; i >= 0; i-- {
			f := files[i]
			if f.published {
				err = errors.Join(err, os.RemoveAll(f.to))
			}
			if f.backup != "" {
				err = errors.Join(err, os.Rename(f.backup, f.to))
			}
		}
	}()
	for i := range files {
		f := &files[i]
		if _, statErr := os.Lstat(f.to); statErr == nil {
			backup := filepath.Join(stage, fmt.Sprintf("old-%d", i))
			if err = os.Rename(f.to, backup); err != nil {
				return err
			}
			f.backup = backup
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if err = os.Rename(f.from, f.to); err != nil {
			return err
		}
		f.published = true
	}
	return nil
}

func lockBrowserInstall(ctx context.Context, opts Options) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	lock := filepath.Join(opts.BinDir, "agent-browser.lock")
	announced := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := os.Mkdir(lock, 0700)
		if err == nil {
			if err := os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				os.RemoveAll(lock)
				return nil, err
			}
			return func() { os.RemoveAll(lock) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		reclaimed, err := reclaimBrowserInstallLock(lock)
		if err != nil {
			return nil, err
		}
		if reclaimed {
			continue
		}
		if !announced {
			fmt.Fprintln(opts.Log, "aice: another aice is installing agent-browser; waiting ...")
			announced = true
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
