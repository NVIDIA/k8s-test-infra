#!/usr/bin/env bash

set -euo pipefail

readonly ACTIONLINT_VERSION=1.7.12
readonly CACHE_DIR=${ACTIONLINT_CACHE_DIR:-.cache/actionlint/${ACTIONLINT_VERSION}}
readonly ACTIONLINT_BIN="${CACHE_DIR}/actionlint"

run_actionlint() {
	shopt -s nullglob
	local workflows=(.github/workflows/*.yml .github/workflows/*.yaml)
	"${ACTIONLINT_BIN}" "${workflows[@]}"
}

actionlint_version() {
	local output
	output=$("${ACTIONLINT_BIN}" -version)
	printf '%s\n' "${output%%$'\n'*}"
}

if [[ -x "${ACTIONLINT_BIN}" ]] && [[ $(actionlint_version) == "${ACTIONLINT_VERSION}" ]]; then
	run_actionlint
	exit
fi

os=$(uname -s)
arch=$(uname -m)
case "${os}/${arch}" in
	Linux/x86_64|Linux/amd64)
		platform=linux_amd64
		digest=8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8
		;;
	Linux/aarch64|Linux/arm64)
		platform=linux_arm64
		digest=325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6
		;;
	Darwin/arm64)
		platform=darwin_arm64
		digest=aba9ced2dee8d27fecca3dc7feb1a7f9a52caefa1eb46f3271ea66b6e0e6953f
		;;
	*)
		printf 'unsupported platform: %s/%s\n' "${os}" "${arch}" >&2
		exit 1
		;;
esac

readonly archive_name="actionlint_${ACTIONLINT_VERSION}_${platform}.tar.gz"
readonly release_url="https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/${archive_name}"

mkdir -p "${CACHE_DIR}"
archive=$(mktemp "${CACHE_DIR}/${archive_name}.XXXXXX")
trap 'rm -f "${archive}"' EXIT

curl --fail --silent --show-error --location --output "${archive}" "${release_url}"

if command -v sha256sum >/dev/null 2>&1; then
	actual_digest=$(sha256sum "${archive}")
elif command -v shasum >/dev/null 2>&1; then
	actual_digest=$(shasum -a 256 "${archive}")
else
	printf 'cannot verify actionlint: sha256sum or shasum is required\n' >&2
	exit 1
fi
actual_digest=${actual_digest%% *}
if [[ "${actual_digest}" != "${digest}" ]]; then
	printf 'actionlint archive checksum mismatch: expected %s, got %s\n' "${digest}" "${actual_digest}" >&2
	exit 1
fi

tar -xzf "${archive}" -C "${CACHE_DIR}" actionlint
chmod +x "${ACTIONLINT_BIN}"

if [[ $(actionlint_version) != "${ACTIONLINT_VERSION}" ]]; then
	printf 'installed actionlint did not report version %s\n' "${ACTIONLINT_VERSION}" >&2
	exit 1
fi

run_actionlint
