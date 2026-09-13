param([string]$OutputDirectory = (Join-Path $PSScriptRoot 'dist'))
$ErrorActionPreference = 'Stop'
$sourceRoot = [IO.Path]::GetFullPath($PSScriptRoot)
$outputRoot = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
$common = @('run', '--rm', '-v', "${sourceRoot}:/src", '-v', 'cline-channel-go-cache:/root/.cache/go-build', '-w', '/src', '-e', 'GOFLAGS=-mod=vendor')

& docker @common golang:1.24-bookworm go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Go static checks failed' }
& docker @common golang:1.24-bookworm go test -race ./...
if ($LASTEXITCODE -ne 0) { throw 'Go regression tests failed' }
& docker @common -v "${outputRoot}:/out" golang:1.24-bookworm go build -trimpath -buildmode=c-shared -o /out/cline-channel.so .
if ($LASTEXITCODE -ne 0) { throw 'Linux plugin build failed' }

$pluginPath = Join-Path $outputRoot 'cline-channel.so'
$signature = [IO.File]::ReadAllBytes($pluginPath)[0..3]
if (($signature -join ',') -ne '127,69,76,70') { throw 'Build output is not a Linux ELF library' }
Get-FileHash -LiteralPath $pluginPath -Algorithm SHA256
