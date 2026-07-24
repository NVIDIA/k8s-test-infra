#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Host driver masquerade helpers. Sourced by:
#   - deployments/nvml-mock/scripts/setup.sh    (install / converge)
#   - deployments/nvml-mock/scripts/cleanup.sh  (preStop teardown)
#
# The masquerade installs nvidia-smi and the mock libraries at the node's
# standard paths so consumers need zero configuration. Every write is tracked
# in a typed manifest so mode transitions and uninstalls remove EXACTLY what
# was installed and NOTHING else. The manifest is the load-bearing safety
# mechanism -- a stale path-only record on a real driver node would be a
# catastrophic bug -- so entries record enough metadata to prove ownership at
# cleanup time.
#
# Manifest schema (whitespace-separated fields, one entry per line):
#
#   file       <host-path>  <sha256>            regular file, hashed at install
#   symlink    <host-path>  <target>            symlink pointing at <target>
#   device     <host-path>  <major>:<minor>     char device (major/minor pair)
#   ldconfig   <host-path>                      ld.so.conf.d snippet
#
# Any other line prefix is a hard error: cleanup refuses to interpret it and
# preserves the manifest so a human can inspect it. Blank lines and lines
# starting with `#` are ignored (comments are permitted for readability).

# hdrl_manifest_path HOST_DIR
# Echo the manifest file path for a given host state directory.
hdrl_manifest_path() {
  echo "$1/host-driver-manifest.txt"
}

# hdrl_hash PATH
# Echo the sha256 of PATH (hex, no filename), or empty if the file is missing.
hdrl_hash() {
  if [ -f "$1" ]; then
    sha256sum "$1" | awk '{print $1}'
  fi
}

# hdrl_devnum MAJOR MINOR
# Echo the "MAJOR:MINOR" canonical form used for device entries.
hdrl_devnum() {
  printf '%s:%s\n' "$1" "$2"
}

# hdrl_verify_entry HOSTROOT TYPE PATH DETAILS
# Verify that a manifest entry still matches the actual host state.
# Prints an error and returns non-zero when the entry disagrees; returns 0
# when the entry matches OR when the recorded path no longer exists (a
# crash-before-write partial install is safe to skip).
hdrl_verify_entry() {
  _hdrl_hostroot=$1
  _hdrl_type=$2
  _hdrl_path=$3
  _hdrl_details=$4
  _hdrl_full="$_hdrl_hostroot$_hdrl_path"
  case "$_hdrl_type" in
    file)
      if [ ! -e "$_hdrl_full" ]; then
        return 0
      fi
      if [ -L "$_hdrl_full" ] || [ ! -f "$_hdrl_full" ]; then
        echo "ERROR: manifest entry file $_hdrl_path is not a regular file on host" >&2
        return 1
      fi
      _hdrl_got=$(hdrl_hash "$_hdrl_full")
      if [ "$_hdrl_got" != "$_hdrl_details" ]; then
        echo "ERROR: manifest entry file $_hdrl_path has been modified (hash mismatch)" >&2
        return 1
      fi
      ;;
    symlink)
      if [ ! -L "$_hdrl_full" ] && [ ! -e "$_hdrl_full" ]; then
        return 0
      fi
      if [ ! -L "$_hdrl_full" ]; then
        echo "ERROR: manifest entry symlink $_hdrl_path is not a symlink on host" >&2
        return 1
      fi
      _hdrl_got=$(readlink "$_hdrl_full")
      if [ "$_hdrl_got" != "$_hdrl_details" ]; then
        echo "ERROR: manifest entry symlink $_hdrl_path points at $_hdrl_got, expected $_hdrl_details" >&2
        return 1
      fi
      ;;
    device)
      if [ ! -e "$_hdrl_full" ]; then
        return 0
      fi
      if [ ! -c "$_hdrl_full" ]; then
        echo "ERROR: manifest entry device $_hdrl_path is not a char device on host" >&2
        return 1
      fi
      # The GNU stat format prints major/minor as decimal; busybox stat is not
      # guaranteed to be present, so a portable fallback via ls parses the
      # comma-separated major, minor fields.
      _hdrl_got=$(ls -l "$_hdrl_full" 2>/dev/null | awk '{gsub(",","",$5); print $5":"$6}')
      if [ "$_hdrl_got" != "$_hdrl_details" ]; then
        echo "ERROR: manifest entry device $_hdrl_path has $_hdrl_got, expected $_hdrl_details" >&2
        return 1
      fi
      ;;
    ldconfig)
      # ldconfig snippets are static and their content is fixed to
      # `/usr/lib64\n`; treat their presence as sufficient.
      return 0
      ;;
    *)
      echo "ERROR: unknown manifest entry type '$_hdrl_type' for $_hdrl_path" >&2
      return 1
      ;;
  esac
  return 0
}

# hdrl_walk_manifest HOSTROOT MANIFEST CALLBACK
# Iterate the manifest and invoke `CALLBACK TYPE PATH DETAILS` for every
# non-blank, non-comment entry, in file order. Blank and comment lines are
# ignored; a malformed line returns non-zero. The callback's exit code is
# preserved: a non-zero return aborts the walk and propagates the code.
hdrl_walk_manifest() {
  _hdrl_hostroot=$1
  _hdrl_manifest=$2
  _hdrl_cb=$3
  # shellcheck disable=SC2094 # reading the manifest we walk is intentional
  while IFS= read -r _hdrl_line || [ -n "$_hdrl_line" ]; do
    case "$_hdrl_line" in
      ''|'#'*) continue ;;
    esac
    _hdrl_type=$(echo "$_hdrl_line" | awk '{print $1}')
    _hdrl_path=$(echo "$_hdrl_line" | awk '{print $2}')
    _hdrl_details=$(echo "$_hdrl_line" | awk '{for (i=3; i<=NF; i++) printf "%s%s", $i, (i<NF ? OFS : ORS)}')
    if [ -z "$_hdrl_type" ] || [ -z "$_hdrl_path" ]; then
      echo "ERROR: malformed manifest line: $_hdrl_line" >&2
      return 1
    fi
    "$_hdrl_cb" "$_hdrl_type" "$_hdrl_path" "$_hdrl_details" || return $?
  done < "$_hdrl_manifest"
}

# hdrl_verify_manifest HOSTROOT MANIFEST
# Two-pass ownership check: verify every recorded entry matches the current
# host state (or is missing) BEFORE any deletion. Returns non-zero without
# side effects when a single entry disagrees, so the caller can preserve the
# manifest and exit loudly instead of silently deleting foreign files.
hdrl_verify_manifest() {
  _hdrl_hostroot=$1
  _hdrl_manifest=$2
  # Wrap hdrl_verify_entry to close over $HOSTROOT for hdrl_walk_manifest.
  _hdrl_check() { hdrl_verify_entry "$_hdrl_hostroot" "$1" "$2" "$3"; }
  hdrl_walk_manifest "$_hdrl_hostroot" "$_hdrl_manifest" _hdrl_check
}

# hdrl_remove_verified_manifest HOSTROOT MANIFEST
# Delete every recorded entry. Callers MUST invoke hdrl_verify_manifest first;
# this helper assumes ownership has already been proven.
hdrl_remove_verified_manifest() {
  _hdrl_hostroot=$1
  _hdrl_manifest=$2
  _hdrl_delete() {
    case "$1" in
      file|symlink|device|ldconfig)
        rm -f "$_hdrl_hostroot$2"
        ;;
    esac
  }
  hdrl_walk_manifest "$_hdrl_hostroot" "$_hdrl_manifest" _hdrl_delete
}

# hdrl_manifest_add MANIFEST TYPE PATH [DETAILS]
# Append a typed entry to the manifest BEFORE creating the file on disk, so a
# crash between record and write leaves a harmless dangling record rather
# than an untracked host file.
hdrl_manifest_add() {
  _hdrl_manifest=$1
  _hdrl_type=$2
  _hdrl_path=$3
  _hdrl_details=$4
  if [ -n "$_hdrl_details" ]; then
    printf '%s %s %s\n' "$_hdrl_type" "$_hdrl_path" "$_hdrl_details" >> "$_hdrl_manifest"
  else
    printf '%s %s\n' "$_hdrl_type" "$_hdrl_path" >> "$_hdrl_manifest"
  fi
}
