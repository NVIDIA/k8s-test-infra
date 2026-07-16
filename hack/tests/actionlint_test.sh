#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
INSTALLER="${ROOT_DIR}/hack/actionlint.sh"
TEST_TMP=$(mktemp -d)
trap 'rm -rf "${TEST_TMP}"' EXIT

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local file=$1
	local expected=$2
	grep -Fqx -- "${expected}" "${file}" || fail "${file} does not contain exact line: ${expected}"
}

write_common_fakes() {
	local bin_dir=$1
	mkdir -p "${bin_dir}"

	cat >"${bin_dir}/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) printf '%s\n' "${TEST_OS:?}" ;;
  -m) printf '%s\n' "${TEST_ARCH:?}" ;;
  *) exit 2 ;;
esac
EOF

	cat >"${bin_dir}/curl" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" >"${TEST_LOG:?}/curl.args"
output=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    output=$1
  fi
  shift
done
[ -n "${output}" ] || exit 2
: >"${output}"
EOF

	cat >"${bin_dir}/tar" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" >"${TEST_LOG:?}/tar.args"
destination=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-C" ]; then
    shift
    destination=$1
  fi
  shift
done
[ -n "${destination}" ] || exit 2
cat >"${destination}/actionlint" <<'ACTIONLINT'
#!/bin/sh
if [ "${1:-}" = "-version" ]; then
  printf '%s\n' '1.7.12'
  exit 0
fi
printf '%s\n' "$@" >"${TEST_LOG:?}/actionlint.args"
ACTIONLINT
chmod +x "${destination}/actionlint"
EOF

	chmod +x "${bin_dir}/uname" "${bin_dir}/curl" "${bin_dir}/tar"
}

write_sha256sum_fake() {
	local bin_dir=$1
	cat >"${bin_dir}/sha256sum" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" >"${TEST_LOG:?}/checksum.args"
printf '%s  %s\n' "${TEST_DIGEST:?}" "$1"
EOF
	chmod +x "${bin_dir}/sha256sum"
}

write_shasum_fake() {
	local bin_dir=$1
	cat >"${bin_dir}/shasum" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" >"${TEST_LOG:?}/checksum.args"
archive=$3
printf '%s  %s\n' "${TEST_DIGEST:?}" "${archive}"
EOF
	chmod +x "${bin_dir}/shasum"
}

run_install() {
	local case_dir=$1
	shift
	env \
		PATH="${case_dir}/bin:/usr/bin:/bin" \
		ACTIONLINT_CACHE_DIR="${case_dir}/cache" \
		TEST_LOG="${case_dir}/log" \
		"$@" \
		/bin/bash "${INSTALLER}"
}

assert_platform() {
	local name=$1
	local os=$2
	local arch=$3
	local archive=$4
	local digest=$5
	local case_dir="${TEST_TMP}/${name}"
	mkdir -p "${case_dir}/log"
	write_common_fakes "${case_dir}/bin"
	write_sha256sum_fake "${case_dir}/bin"

	run_install "${case_dir}" TEST_OS="${os}" TEST_ARCH="${arch}" TEST_DIGEST="${digest}"

	local expected_url="https://github.com/rhysd/actionlint/releases/download/v1.7.12/${archive}"
	local curl_log="${case_dir}/log/curl.args"
	[[ $(wc -l <"${curl_log}") -eq 7 ]] || fail "unexpected curl argument count for ${name}"
	[[ $(sed -n '1p' "${curl_log}") == --fail ]] || fail "curl does not fail on HTTP errors for ${name}"
	[[ $(sed -n '2p' "${curl_log}") == --silent ]] || fail "curl is not silent for ${name}"
	[[ $(sed -n '3p' "${curl_log}") == --show-error ]] || fail "curl does not show errors for ${name}"
	[[ $(sed -n '4p' "${curl_log}") == --location ]] || fail "curl does not follow redirects for ${name}"
	[[ $(sed -n '5p' "${curl_log}") == --output ]] || fail "curl does not download to a file for ${name}"
	local downloaded_archive
	downloaded_archive=$(sed -n '6p' "${curl_log}")
	[[ ${downloaded_archive} == "${case_dir}/cache/"* ]] || fail "archive was not downloaded inside the cache for ${name}"
	[[ $(sed -n '7p' "${curl_log}") == "${expected_url}" ]] || fail "unexpected release URL for ${name}"
	assert_file_contains "${case_dir}/log/checksum.args" "${downloaded_archive}"
	assert_file_contains "${case_dir}/log/tar.args" actionlint
	[[ -s "${case_dir}/log/actionlint.args" ]] || fail "actionlint was not executed for ${name}"
}

[[ -f "${INSTALLER}" ]] || fail "installer is absent: ${INSTALLER}"

assert_platform linux-amd64 Linux x86_64 \
	actionlint_1.7.12_linux_amd64.tar.gz \
	8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8
assert_platform linux-arm64 Linux aarch64 \
	actionlint_1.7.12_linux_arm64.tar.gz \
	325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6
assert_platform darwin-arm64 Darwin arm64 \
	actionlint_1.7.12_darwin_arm64.tar.gz \
	aba9ced2dee8d27fecca3dc7feb1a7f9a52caefa1eb46f3271ea66b6e0e6953f

mismatch_dir="${TEST_TMP}/mismatch"
mkdir -p "${mismatch_dir}/log"
write_common_fakes "${mismatch_dir}/bin"
write_sha256sum_fake "${mismatch_dir}/bin"
if run_install "${mismatch_dir}" \
	TEST_OS=Linux TEST_ARCH=x86_64 \
	TEST_DIGEST=0000000000000000000000000000000000000000000000000000000000000000 \
	>"${mismatch_dir}/stdout" 2>"${mismatch_dir}/stderr"; then
	fail "mismatched digest succeeded"
fi
[[ ! -e "${mismatch_dir}/log/tar.args" ]] || fail "mismatched archive was extracted"
[[ ! -e "${mismatch_dir}/log/actionlint.args" ]] || fail "mismatched archive was executed"

reuse_dir="${TEST_TMP}/reuse"
mkdir -p "${reuse_dir}/bin" "${reuse_dir}/cache" "${reuse_dir}/log"
write_common_fakes "${reuse_dir}/bin"
cat >"${reuse_dir}/cache/actionlint" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "-version" ]; then
  printf '%s\n' '1.7.12' 'installed by downloading from release page' 'built with go compiler'
else
  printf '%s\n' "$@" >"${TEST_LOG:?}/actionlint.args"
fi
EOF
chmod +x "${reuse_dir}/cache/actionlint"
run_install "${reuse_dir}" TEST_OS=Unsupported TEST_ARCH=unsupported TEST_DIGEST=unused
[[ ! -e "${reuse_dir}/log/curl.args" ]] || fail "valid cached binary was downloaded again"
[[ -s "${reuse_dir}/log/actionlint.args" ]] || fail "valid cached binary was not executed"

unsupported_dir="${TEST_TMP}/unsupported"
mkdir -p "${unsupported_dir}/log"
write_common_fakes "${unsupported_dir}/bin"
if run_install "${unsupported_dir}" TEST_OS=Plan9 TEST_ARCH=mips TEST_DIGEST=unused \
	>"${unsupported_dir}/stdout" 2>"${unsupported_dir}/stderr"; then
	fail "unsupported platform succeeded"
fi
grep -Fq 'unsupported platform: Plan9/mips' "${unsupported_dir}/stderr" || fail "unsupported platform error was unclear"
[[ ! -e "${unsupported_dir}/log/curl.args" ]] || fail "unsupported platform attempted a download"

fallback_dir="${TEST_TMP}/shasum-fallback"
mkdir -p "${fallback_dir}/log"
write_common_fakes "${fallback_dir}/bin"
write_shasum_fake "${fallback_dir}/bin"
fallback_digest=aba9ced2dee8d27fecca3dc7feb1a7f9a52caefa1eb46f3271ea66b6e0e6953f
run_install "${fallback_dir}" TEST_OS=Darwin TEST_ARCH=arm64 TEST_DIGEST="${fallback_digest}"
fallback_log="${fallback_dir}/log/checksum.args"
[[ $(sed -n '1p' "${fallback_log}") == -a && $(sed -n '2p' "${fallback_log}") == 256 ]] || fail "shasum fallback did not use -a 256"
[[ $(sed -n '3p' "${fallback_log}") == "${fallback_dir}/cache/"* ]] || fail "shasum did not verify the cached archive"

printf 'PASS: actionlint installer contracts\n'
