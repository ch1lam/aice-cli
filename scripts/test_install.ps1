# Offline Windows acceptance tests; no network access or user PATH writes.
$ErrorActionPreference = 'Stop'
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile(
	(Join-Path $PSScriptRoot 'install.ps1'), [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
# Load the real function definitions without running the interactive entry point.
$ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $true) |
	ForEach-Object { Invoke-Expression $_.Extent.Text }

function Assert-Equal($Actual, $Expected, [string]$Label) {
	if ($Actual -cne $Expected) { throw "$Label : expected [$Expected], got [$Actual]" }
}

$root = Join-Path ([IO.Path]::GetTempPath()) ('aice-installer-test-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root | Out-Null
$originalTemp = $env:TEMP
$originalTmp = $env:TMP
$originalTmpDir = $env:TMPDIR
try {
	$env:TEMP = Join-Path $root 'temp'
	$env:TMP = $env:TEMP
	$env:TMPDIR = $env:TEMP
	New-Item -ItemType Directory -Path $env:TEMP | Out-Null
	$fixture = Join-Path $root 'aice.exe'
	[IO.File]::WriteAllText($fixture, 'new binary')
	$script:fixtureBundle = Join-Path $root 'aice_windows_amd64.zip'
	Compress-Archive -LiteralPath $fixture -DestinationPath $script:fixtureBundle
	$digest = (Get-FileHash -LiteralPath $script:fixtureBundle -Algorithm SHA256).Hash
	$script:requests = @()
	$script:mode = ''
	$script:responseStyle = '7'
	$script:failuresLeft = 0

	function Start-Sleep { param($Seconds) }
	function Invoke-WebRequest {
		param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile, [int]$TimeoutSec)
		$script:requests += $Uri
		if ($script:mode -eq 'download-failure' -or $script:failuresLeft -gt 0) {
			$script:failuresLeft--
			throw 'simulated network failure'
		}
		if ($Uri.EndsWith('/releases/latest')) {
			$latest = [uri]'https://github.com/ch1lam/aice-cli/releases/tag/v9.8.7'
			if ($script:responseStyle -eq '5') { return @{ BaseResponse = @{ ResponseUri = $latest } } }
			return @{ BaseResponse = @{ RequestMessage = @{ RequestUri = $latest } } }
		}
		if ($Uri.EndsWith('/checksums.txt')) {
			$hash = if ($script:mode -eq 'checksum-failure') { '0' * 64 } else { $digest }
			[IO.File]::WriteAllText($OutFile, "$hash  aice_windows_amd64.zip`n")
		} else {
			[IO.File]::Copy($script:fixtureBundle, $OutFile, $true)
		}
	}
	function Copy-Item {
		param([string]$LiteralPath, [string]$Destination)
		if ($script:mode -eq 'copy-failure') {
			[IO.File]::WriteAllText($Destination, 'partial')
			throw 'simulated disk write failure'
		}
		Microsoft.PowerShell.Management\Copy-Item -LiteralPath $LiteralPath -Destination $Destination
	}

	$dir = Join-Path $root 'install dir'
	Assert-Equal (Add-AicePathEntry "$dir-old" $dir) "$dir-old;$dir" 'PATH substring collision'
	Assert-Equal (Add-AicePathEntry "C:\other;$($dir.ToUpper())\" $dir) "C:\other;$($dir.ToUpper())\" 'PATH case and slash'
	Assert-Equal (Add-AicePathEntry '' $dir) $dir 'empty PATH'
	Assert-Equal (Add-AicePathEntry 'C:\other' $dir) "C:\other;$dir" 'append PATH'

	$cases = @('fresh', 'replace', 'latest5', 'latest7', 'retry', 'checksum-failure', 'download-failure', 'copy-failure')
	if ($env:OS -eq 'Windows_NT') { $cases += 'locked-target' }
	foreach ($case in $cases) {
		$script:mode = $case
		$script:requests = @()
		$script:failuresLeft = if ($case -eq 'retry') { 1 } else { 0 }
		$script:responseStyle = if ($case -eq 'latest5') { '5' } else { '7' }
		$dest = Join-Path $root "$case dir [literal]"
		New-Item -ItemType Directory -Path $dest | Out-Null
		$target = Join-Path $dest 'aice.exe'
		if ($case -ne 'fresh') { [IO.File]::WriteAllText($target, 'old binary') }
		$locked = $null
		if ($case -eq 'locked-target') {
			$locked = [IO.File]::Open($target, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
		}
		$failureExpected = $case -in @('checksum-failure', 'download-failure', 'copy-failure', 'locked-target')
		$caught = $null
		Push-Location $root
		try {
			$version = if ($case.StartsWith('latest')) { '' } else { '1.2.3' }
			# Relative input exercises normalization against PowerShell's current location.
			$result = Install-AiceBinary -InstallDir "./$case dir [literal]" -Version $version
		} catch { $caught = $_ } finally {
			Pop-Location
			if ($locked) { $locked.Dispose() }
		}
		Assert-Equal ([bool]$caught) $failureExpected "$case failure state ($caught)"
		$expected = if ($failureExpected) { 'old binary' } else { 'new binary' }
		Assert-Equal ([IO.File]::ReadAllText($target)) $expected "$case binary content"
		Assert-Equal @(Get-ChildItem -LiteralPath $env:TEMP).Count 0 "$case download cleanup"
		Assert-Equal @(Get-ChildItem -LiteralPath $dest -Force | Where-Object Name -Like '.aice-install-*').Count 0 "$case staging cleanup"
		if (-not $failureExpected) { Assert-Equal $result $dest "$case absolute path" }
		if ($case.StartsWith('latest')) {
			Assert-Equal $script:requests.Count 3 "$case resolve once"
			foreach ($request in $script:requests[1..2]) {
				if (-not $request.Contains('/download/v9.8.7/')) { throw "unlocked release URL: $request" }
			}
		} elseif ($case -eq 'retry' -or $case -eq 'download-failure') {
			Assert-Equal $script:requests.Count 3 "$case request count"
		} elseif (-not $script:requests[0].Contains('/download/v1.2.3/')) {
			throw 'pinned version did not gain v prefix'
		}
		Write-Host "PASS $case"
	}
	Write-Host 'PASS PATH matching'
} finally {
	$env:TEMP = $originalTemp
	$env:TMP = $originalTmp
	$env:TMPDIR = $originalTmpDir
	Remove-Item -LiteralPath $root -Recurse -Force
}
