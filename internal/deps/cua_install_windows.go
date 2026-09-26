package deps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func publishCuaNative(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// MoveFile does not replace an existing destination, including an empty dir.
	return windows.MoveFile(source, destination)
}

func verifyCuaNative(ctx context.Context, directory string) error {
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		return errors.New("Windows SystemRoot is unavailable for Cua signature verification")
	}
	powershell := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	for _, name := range []string{"cua-driver.exe", "cua-cursor-theme.exe", "cua-driver-uia.exe"} {
		literal := "'" + strings.ReplaceAll(filepath.Join(directory, name), "'", "''") + "'"
		// Fixed code plus an escaped literal path, never shell/command interpolation.
		script := `$ErrorActionPreference='Stop'; $s=Get-AuthenticodeSignature -LiteralPath ` + literal + `; if ($s.Status -ne 'Valid' -or $null -eq $s.TimeStamperCertificate -or $s.SignerCertificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName,$false) -cne 'Cua AI, Inc.') { exit 1 }; 'verified'`
		output, err := cuaVerifyCommand(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
		if err != nil || strings.TrimSpace(output) != "verified" {
			return fmt.Errorf("Cua Authenticode signature, timestamp or signing identity rejected for %s: %w", name, errors.Join(err, errors.New("signature verification failed")))
		}
	}
	version, err := cuaVerifyCommand(ctx, filepath.Join(directory, "cua-driver.exe"), "--version")
	if err != nil {
		return fmt.Errorf("Cua native version probe failed: %w", err)
	}
	if strings.TrimSpace(version) != "cua-driver "+CuaDriverVersion {
		return errors.New("Cua native version does not match the pinned release")
	}
	return nil
}
