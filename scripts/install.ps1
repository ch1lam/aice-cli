<#
Install AICE on Windows into a user-writable directory so `aice update` can
later replace the binary in place. The directory is added to the per-user PATH
so the binary is available from new shells.

Usage:
  iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
  $env:INSTALL_DIR = "$HOME\aice"; iwr -useb <url> | iex   # override install directory
#>

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'ch1lam/aice-cli'
$binary = 'aice.exe'
$installDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $HOME '.local\bin' }
$base = "https://github.com/$repo/releases/latest/download"

if (($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') -and ($env:PROCESSOR_ARCHITEW6432 -ne 'AMD64')) {
	throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE (only amd64 is published)"
}

$bundle = 'aice_windows_amd64.zip'
$tmp = Join-Path $env:TEMP ("aice-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
	Write-Host "aice: downloading $bundle ..."
	Invoke-WebRequest -UseBasicParsing -Uri "$base/$bundle" -OutFile (Join-Path $tmp $bundle)
	Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

	$want = Get-Content (Join-Path $tmp 'checksums.txt') |
		ForEach-Object {
			$parts = $_ -split '\s+'
			if ($parts.Count -ge 2 -and $parts[1] -eq $bundle) { $parts[0] }
		} | Select-Object -First 1
	if (-not $want) { throw "checksums.txt has no entry for $bundle" }

	$got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $bundle)).Hash.ToLower()
	if ($want.ToLower() -ne $got) { throw "checksum mismatch for $bundle" }

	Write-Host "aice: installing to $installDir ..."
	New-Item -ItemType Directory -Path $installDir -Force | Out-Null
	Expand-Archive -Path (Join-Path $tmp $bundle) -DestinationPath (Join-Path $tmp 'extract')
	$target = Join-Path $installDir $binary
	try {
		Copy-Item -Force (Join-Path $tmp "extract\$binary") $target
	} catch {
		throw "aice: could not replace $target - close any running aice.exe and retry: $_"
	}

	$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
	$alreadyInPath = -not [string]::IsNullOrEmpty($userPath) -and
		$userPath.IndexOf($installDir, [System.StringComparison]::OrdinalIgnoreCase) -ge 0
	if (-not $alreadyInPath) {
		$newPath = if ([string]::IsNullOrEmpty($userPath)) { $installDir } else { "$userPath;$installDir" }
		[Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
		Write-Host "aice: added $installDir to your user PATH (new shells only)"
	}

	Write-Host "aice: installed $installDir\$binary"
	Write-Host 'aice: run `aice --version` to verify, and `aice update` to upgrade later'
}
finally {
	Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
