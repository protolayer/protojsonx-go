#!/usr/bin/env bash
#
# Runs the protobuf conformance test suite against the conformance testee in
# internal/cmd/conformance.
#
# Usage: scripts/conformance.sh [protojsonx|protojson]
#
# The argument selects the ProtoJSON implementation (default: protojsonx). Use
# protojson to compare with upstream. Any failure fails the run.
#
# The runner is a prebuilt linux/amd64 binary. On other platforms, the script
# runs it in a linux/amd64 Ubuntu container with Docker.

set -euo pipefail

readonly RUNNER_VERSION="31.1"
readonly RUNNER_ZIP="conformance_test_runner-${RUNNER_VERSION}-linux-x86_64.zip"
readonly RUNNER_URL="https://github.com/bufbuild/protobuf-conformance/releases/download/v${RUNNER_VERSION}/${RUNNER_ZIP}"
readonly RUNNER_SHA256="f55d56f1db31cd0aca9af9877fe55e80f9cb507cb343df223ed1dbcb992dd80c"
# The runner needs glibc 2.38 or later.
readonly CONTAINER_IMAGE="ubuntu:24.04@sha256:2198e4c65076e9cb0b3d695219df667c0e3b0072da64ca3c2648aca17dcecaf5"

main() {
  local impl="${1:-protojsonx}"
  local root_dir
  root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  local cache_dir="${root_dir}/.cache/conformance"
  local bin_dir="${cache_dir}/bin"
  local out_dir="${cache_dir}/out"

  mkdir -p "${bin_dir}" "${out_dir}"
  download_runner "${cache_dir}" "${bin_dir}"

  (
    cd "${root_dir}"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
      go build -o "${bin_dir}/testee" ./internal/cmd/conformance
  )

  if [[ "$(uname -s)" == "Linux" && "$(uname -m)" == "x86_64" ]]; then
    (
      cd "${bin_dir}"
      PROTOJSONX_CONFORMANCE_IMPL="${impl}" \
        ./conformance_test_runner \
        --enforce_recommended \
        --maximum_edition 2023 \
        --output_dir "${out_dir}" \
        ./testee
    )
  else
    docker run --rm --platform linux/amd64 \
      --env "PROTOJSONX_CONFORMANCE_IMPL=${impl}" \
      --volume "${bin_dir}:/conformance/bin:ro" \
      --volume "${out_dir}:/conformance/out" \
      --workdir /conformance/bin \
      "${CONTAINER_IMAGE}" \
      ./conformance_test_runner \
      --enforce_recommended \
      --maximum_edition 2023 \
      --output_dir /conformance/out \
      ./testee
  fi
}

download_runner() {
  local cache_dir="${1}"
  local bin_dir="${2}"
  local zip_path="${cache_dir}/${RUNNER_ZIP}"

  if [[ ! -f "${zip_path}" ]]; then
    curl --fail --silent --show-error --location --output "${zip_path}.tmp" "${RUNNER_URL}"
    mv "${zip_path}.tmp" "${zip_path}"
  fi
  local actual_sha256
  actual_sha256="$(sha256 "${zip_path}")"
  if [[ "${actual_sha256}" != "${RUNNER_SHA256}" ]]; then
    echo "error: checksum mismatch for ${zip_path}: got ${actual_sha256}, want ${RUNNER_SHA256}" >&2
    rm -f "${zip_path}"
    exit 1
  fi
  if [[ ! -x "${bin_dir}/conformance_test_runner" ]]; then
    unzip -q -o -j "${zip_path}" bin/conformance_test_runner -d "${bin_dir}"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${1}" | cut -d ' ' -f 1
  else
    shasum -a 256 "${1}" | cut -d ' ' -f 1
  fi
}

main "$@"
