#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUTPUT_DIR=${1:-"${ROOT_DIR}/dist"}
VERSION=${CLOUD_SKILLS_VERSION:-0.4.0}

mkdir -p "${OUTPUT_DIR}"
if find "${OUTPUT_DIR}" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
  echo "refusing to write into non-empty output directory: ${OUTPUT_DIR}" >&2
  exit 3
fi

WORK_DIR=$(mktemp -d)
trap 'rm -rf "${WORK_DIR}"' EXIT

TARGETS=(darwin/arm64 darwin/amd64 linux/arm64 linux/amd64)
for target in "${TARGETS[@]}"; do
  GOOS=${target%/*}
  GOARCH=${target#*/}
  ARCHIVE="cloud-skills-mcp_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
  PACKAGE_DIR="${WORK_DIR}/cloud-skills-mcp_${VERSION}_${GOOS}_${GOARCH}"
  mkdir -p "${PACKAGE_DIR}"
  (
    cd "${ROOT_DIR}"
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
      go build -trimpath -o "${PACKAGE_DIR}/cloud-skills-mcp" ./cmd/cloud-skills-mcp
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
      go build -trimpath -o "${PACKAGE_DIR}/tencent-cloud-mcp" ./cmd/tencent-cloud-mcp
  )
  cp "${ROOT_DIR}/README.md" "${ROOT_DIR}/LICENSE" "${PACKAGE_DIR}/"
  for skill in aws azure google-cloud alicloud tencent-cloud baiducloud; do
    cp -R "${ROOT_DIR}/${skill}" "${PACKAGE_DIR}/${skill}"
  done
  tar -C "${WORK_DIR}" -czf "${OUTPUT_DIR}/${ARCHIVE}" "$(basename "${PACKAGE_DIR}")"
done

(
  cd "${OUTPUT_DIR}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > checksums.txt
  else
    shasum -a 256 ./*.tar.gz > checksums.txt
  fi
)

echo "release artifacts written to ${OUTPUT_DIR}"
