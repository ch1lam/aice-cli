<#
Install AICE on Windows into a user-writable directory so `aice update` can
later replace the binary in place. The directory is added to the per-user PATH
and the current shell's PATH.

Usage:
  iwr -useb https://raw.githubusercontent.com/ch1lam/aice-cli/main/scripts/install.ps1 | iex
  $env:INSTALL_DIR = "$HOME\aice"; iwr -useb <url> | iex
  $env:AICE_VERSION = 'v1.2.3'; iwr -useb <url> | iex
#>

# A child scope keeps preferences and temporary variables out of the user's
# interactive session when the script is piped to Invoke-Expression.
& {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'

	function Invoke-AiceDownload {
		param([string]$Uri, [string]$OutFile)
		for ($attempt = 1; $attempt -le 3; $attempt++) {
			try {
				$options = @{ UseBasicParsing = $true; Uri = $Uri; TimeoutSec = 180 }
				if ($OutFile) { $options.OutFile = $OutFile }
				return Invoke-WebRequest @options
			} catch {
				$status = if ($_.Exception.Response) { [int]$_.Exception.Response.StatusCode } else { 0 }
				if ($status -eq 404) { throw "release or asset not found: $Uri" }
				if ($attempt -eq 3 -or ($status -ge 400 -and $status -lt 500 -and $status -notin @(408, 429))) {
					throw "could not download $Uri : $_"
				}
				Start-Sleep -Seconds $attempt
			}
		}
	}

	function Add-AicePathEntry {
		param([string]$CurrentPath, [string]$InstallDir)
		foreach ($entry in ($CurrentPath -split ';')) {
			$entry = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"'))
			if ([string]::IsNullOrWhiteSpace($entry)) { continue }
			# Ignore malformed entries, and leave the original PATH text intact.
			try { $entry = [IO.Path]::GetFullPath($entry).TrimEnd('\', '/') } catch { continue }
			if ([string]::Equals($entry, $InstallDir.TrimEnd('\', '/'), [StringComparison]::OrdinalIgnoreCase)) {
				return $CurrentPath
			}
		}
		if ([string]::IsNullOrEmpty($CurrentPath)) { return $InstallDir }
		return "$CurrentPath;$InstallDir"
	}

	function Install-AiceBinary {
		param([string]$InstallDir, [string]$Version)
		$repo = 'ch1lam/aice-cli'
		$bundle = 'aice_windows_amd64.zip'
		$InstallDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($InstallDir)
		if ($Version) {
			if (-not $Version.StartsWith('v')) { $Version = "v$Version" }
		} else {
			$response = Invoke-AiceDownload -Uri "https://github.com/$repo/releases/latest"
			# Windows PowerShell 5.1 uses HttpWebResponse; PowerShell 7 uses HttpResponseMessage.
			$latest = $response.BaseResponse.ResponseUri
			if (-not $latest) { $latest = $response.BaseResponse.RequestMessage.RequestUri }
			$prefix = "https://github.com/$repo/releases/tag/"
			if (-not $latest -or -not $latest.AbsoluteUri.StartsWith($prefix)) {
				throw "unexpected latest release URL: $latest"
			}
			$Version = $latest.AbsoluteUri.Substring($prefix.Length)
			if (-not $Version) { throw 'latest release has no tag' }
		}
		$base = "https://github.com/$repo/releases/download/$Version"
		Write-Host "aice: installing $Version"
		$tmp = Join-Path ([IO.Path]::GetTempPath()) ("aice-install-" + [Guid]::NewGuid().ToString('N'))
		$staged = $null
		New-Item -ItemType Directory -Path $tmp | Out-Null
		try {
			Write-Host "aice: downloading $bundle ..."
			Invoke-AiceDownload -Uri "$base/$bundle" -OutFile (Join-Path $tmp $bundle) | Out-Null
			Invoke-AiceDownload -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') | Out-Null
			$hashes = @(Get-Content -LiteralPath (Join-Path $tmp 'checksums.txt') | ForEach-Object {
				$parts = $_.Trim() -split '\s+'
				if ($parts.Count -eq 2 -and $parts[1] -eq $bundle) { $parts[0] }
			})
			if ($hashes.Count -ne 1) { throw "checksums.txt must contain exactly one entry for $bundle" }
			$got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $tmp $bundle)).Hash
			if ($hashes[0] -ne $got) { throw "checksum mismatch for $bundle" }

			Expand-Archive -LiteralPath (Join-Path $tmp $bundle) -DestinationPath (Join-Path $tmp 'extract')
			$source = Join-Path $tmp 'extract\aice.exe'
			if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'archive has no aice.exe file' }
			New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
			$target = Join-Path $InstallDir 'aice.exe'
			$staged = Join-Path $InstallDir ('.aice-install-' + [Guid]::NewGuid().ToString('N'))
			Write-Host "aice: installing to $InstallDir ..."
			try {
				Copy-Item -LiteralPath $source -Destination $staged
				if ([IO.File]::Exists($target)) {
					# Replace only after the complete new binary is on the same filesystem.
					[IO.File]::Replace($staged, $target, [System.Management.Automation.Language.NullString]::Value)
				} else {
					[IO.File]::Move($staged, $target)
				}
			} catch {
				throw "could not replace $target; check directory permissions and close any running aice.exe, then retry: $_"
			}
			return $InstallDir
		} finally {
			if ($staged) { Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue }
			Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
		}
	}

	if (($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') -and ($env:PROCESSOR_ARCHITEW6432 -ne 'AMD64')) {
		throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE (only amd64 is published)"
	}
	$installDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $HOME '.local\bin' }
	$installDir = Install-AiceBinary -InstallDir $installDir -Version $env:AICE_VERSION
	$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
	$newPath = Add-AicePathEntry -CurrentPath $userPath -InstallDir $installDir
	if ($newPath -cne $userPath) {
		[Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
		Write-Host "aice: added $installDir to your user PATH"
	}
	$env:Path = Add-AicePathEntry -CurrentPath $env:Path -InstallDir $installDir
	Write-Host "aice: installed $installDir\aice.exe"
	Write-Host 'aice: run `aice --version` to verify, and `aice update` to upgrade later'
}
