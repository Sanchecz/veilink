param([string]$Version = 'v0.1.0-rc.1')
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^v\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$') { throw 'Version must look like vX.Y.Z[-suffix]' }

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$dist = Join-Path $repo 'dist'
$stage = Join-Path $repo '.release-stage'
$sbomTool = Join-Path $stage 'cyclonedx-gomod.exe'
if (-not $dist.StartsWith($repo, [StringComparison]::OrdinalIgnoreCase) -or -not $stage.StartsWith($repo, [StringComparison]::OrdinalIgnoreCase)) { throw 'Release paths escaped repository' }
Remove-Item -LiteralPath $dist,$stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $dist,$stage | Out-Null

$commit = 'none'
if (Test-Path -LiteralPath (Join-Path $repo '.git')) {
  $candidate = (& git -C $repo rev-parse --short=12 HEAD 2>$null)
  if ($LASTEXITCODE -eq 0 -and $candidate) { $commit = $candidate }
}
$buildDate = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
$ldflags = "-s -w -X main.version=$Version -X main.commit=$commit -X main.date=$buildDate"

function Build-Target([string]$Os, [string]$Arch, [string]$Ext) {
  $name = "veilink-$Version-$Os-$Arch"
  $dir = Join-Path $stage $name
  New-Item -ItemType Directory -Force -Path $dir,(Join-Path $dir 'configs'),(Join-Path $dir 'docs') | Out-Null
  $oldOs,$oldArch,$oldCgo = $env:GOOS,$env:GOARCH,$env:CGO_ENABLED
  try {
    $env:GOOS=$Os; $env:GOARCH=$Arch; $env:CGO_ENABLED='0'
    & go build -buildvcs=false -trimpath -ldflags $ldflags -o (Join-Path $dir ("veilink"+$Ext)) ./cmd/veilink
    if ($LASTEXITCODE -ne 0) { throw "build failed for $Os/$Arch" }
  } finally { $env:GOOS=$oldOs; $env:GOARCH=$oldArch; $env:CGO_ENABLED=$oldCgo }
  Copy-Item LICENSE,SECURITY.md,CHANGELOG.md,THIRD_PARTY_NOTICES.md,README.md -Destination $dir
  Copy-Item (Join-Path $repo 'configs\*') -Destination (Join-Path $dir 'configs') -Recurse
  Copy-Item (Join-Path $repo 'docs\*') -Destination (Join-Path $dir 'docs') -Recurse
  if ($Os -eq 'linux') { Copy-Item (Join-Path $repo 'deploy') -Destination $dir -Recurse }
  if ($Os -eq 'windows') {
    $zip = Join-Path $stage 'wintun-0.14.1.zip'
    if (-not (Test-Path -LiteralPath $zip)) { & curl.exe --fail --location --retry 3 --output $zip 'https://www.wintun.net/builds/wintun-0.14.1.zip'; if ($LASTEXITCODE -ne 0) { throw 'Wintun download failed' } }
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    if ($hash -ne '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51') { throw "Wintun checksum mismatch: $hash" }
    tar.exe -xf $zip -C $stage
    Copy-Item (Join-Path $stage 'wintun\bin\amd64\wintun.dll') -Destination $dir
    Copy-Item (Join-Path $stage 'wintun\LICENSE.txt') -Destination (Join-Path $dir 'WINTUN-LICENSE.txt')
  }
  if ($Os -eq 'windows') { Compress-Archive -Path $dir -DestinationPath (Join-Path $dist ($name+'.zip')) } else { tar.exe -C $stage -czf (Join-Path $dist ($name+'.tar.gz')) $name }
}

function New-Sbom([string]$Os, [string]$Arch) {
  $output = Join-Path $dist "sbom-$Os-$Arch.cdx.json"
  $oldOs,$oldArch,$oldCgo = $env:GOOS,$env:GOARCH,$env:CGO_ENABLED
  try {
    $env:GOOS=$Os; $env:GOARCH=$Arch; $env:CGO_ENABLED='0'
    & $sbomTool app -json -output $output -licenses -main cmd/veilink .
    if ($LASTEXITCODE -ne 0) { throw "SBOM generation failed for $Os/$Arch" }
  } finally { $env:GOOS=$oldOs; $env:GOARCH=$oldArch; $env:CGO_ENABLED=$oldCgo }
  $document = Get-Content -LiteralPath $output -Raw | ConvertFrom-Json
  $document.metadata.component | Add-Member -NotePropertyName version -NotePropertyValue $Version -Force
  $json = $document | ConvertTo-Json -Depth 100
  [IO.File]::WriteAllText($output,$json+[Environment]::NewLine,[Text.UTF8Encoding]::new($false))
}

Push-Location $repo
try {
  & go test -count=1 ./...; if ($LASTEXITCODE -ne 0) { throw 'tests failed' }
  & go vet ./...; if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
  & go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...; if ($LASTEXITCODE -ne 0) { throw 'staticcheck failed' }
  & go run github.com/securego/gosec/v2/cmd/gosec@v2.28.0 -quiet ./...; if ($LASTEXITCODE -ne 0) { throw 'gosec failed' }
  & go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...; if ($LASTEXITCODE -ne 0) { throw 'govulncheck failed' }
  $oldGoBin = $env:GOBIN
  try { $env:GOBIN=$stage; & go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.11.0; if ($LASTEXITCODE -ne 0) { throw 'SBOM tool build failed' } }
  finally { $env:GOBIN=$oldGoBin }
  Build-Target linux amd64 ''
  Build-Target linux arm64 ''
  Build-Target windows amd64 '.exe'
  New-Sbom linux amd64
  New-Sbom linux arm64
  New-Sbom windows amd64
  $lines = Get-ChildItem -LiteralPath $dist -File | Where-Object Name -ne 'SHA256SUMS' | Sort-Object Name | ForEach-Object { '{0}  {1}' -f (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant(),$_.Name }
  [IO.File]::WriteAllLines((Join-Path $dist 'SHA256SUMS'),$lines,[Text.UTF8Encoding]::new($false))
} finally { Pop-Location; Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue }
Write-Host "Release artifacts written to $dist"
