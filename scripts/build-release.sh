#!/usr/bin/env bash
set -euo pipefail

version="${1:-v0.1.0-rc.1}"
case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "version must look like vX.Y.Z[-suffix]" >&2; exit 2;; esac

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dist="$repo/dist"
stage="$repo/.release-stage"
trap 'rm -rf -- "$stage"' EXIT
rm -rf -- "$dist" "$stage"
mkdir -p "$dist" "$stage"
sbom_tool="$stage/cyclonedx-gomod"

commit="$(git -C "$repo" rev-parse --short=12 HEAD 2>/dev/null || printf none)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.date=$build_date"

build_target() {
	os="$1"; arch="$2"; ext="$3"; name="veilink-$version-$os-$arch"
	dir="$stage/$name"
	mkdir -p "$dir/configs" "$dir/deploy" "$dir/docs"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -buildvcs=false -trimpath -ldflags "$ldflags" -o "$dir/veilink$ext" ./cmd/veilink
	cp LICENSE SECURITY.md CHANGELOG.md THIRD_PARTY_NOTICES.md README.md "$dir/"
	cp -R configs/. "$dir/configs/"
	cp -R docs/. "$dir/docs/"
	if [[ "$os" == linux ]]; then cp -R deploy/. "$dir/deploy/"; fi
	if [[ "$os" == windows ]]; then
		wintun_zip="$stage/wintun-0.14.1.zip"
		if [[ ! -f "$wintun_zip" ]]; then curl --fail --location --retry 3 -o "$wintun_zip" https://www.wintun.net/builds/wintun-0.14.1.zip; fi
		echo "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51  $wintun_zip" | sha256sum -c -
		unzip -p "$wintun_zip" wintun/bin/amd64/wintun.dll > "$dir/wintun.dll"
		unzip -p "$wintun_zip" wintun/LICENSE.txt > "$dir/WINTUN-LICENSE.txt"
	fi
	if [[ "$os" == windows ]]; then (cd "$stage" && zip -qr "$dist/$name.zip" "$name"); else tar -C "$stage" -czf "$dist/$name.tar.gz" "$name"; fi
}

generate_sbom() {
	os="$1"; arch="$2"; output="$dist/sbom-$os-$arch.cdx.json"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$sbom_tool" app -json -output "$output" -licenses -main cmd/veilink .
	python3 - "$output" "$version" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
document = json.loads(path.read_text(encoding="utf-8"))
document["metadata"]["component"]["version"] = sys.argv[2]
path.write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
PY
}

cd "$repo"
go test -count=1 ./...
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.28.0 -quiet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
GOBIN="$stage" go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.11.0
build_target linux amd64 ""
build_target linux arm64 ""
build_target windows amd64 .exe
generate_sbom linux amd64
generate_sbom linux arm64
generate_sbom windows amd64
(cd "$dist" && sha256sum veilink-* sbom-*.cdx.json > SHA256SUMS)
echo "Release artifacts written to $dist"
