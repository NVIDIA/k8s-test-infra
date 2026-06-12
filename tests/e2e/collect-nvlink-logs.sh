#!/usr/bin/env bash
# Run every `nvidia-smi nvlink` query/display command one after another,
# logging command + output to a file.
# Usage: collect-nvlink-logs.sh [logfile]
#   NVIDIA_SMI=<path>   override the nvidia-smi binary
#   INCLUDE_RESET=1     also run the state-changing reset flags (-r, -re) last
SMI="${NVIDIA_SMI:-nvidia-smi}"
LOG="${1:-nvlink.log}"
: >"$LOG"

for args in \
  "--version" \
  "nvlink -h" \
  "nvlink -info" \
  "nvlink -s" \
  "nvlink -c" \
  "nvlink -p" \
  "nvlink -R" \
  "nvlink -e" \
  "nvlink -ec" \
  "nvlink -gc" \
  "nvlink -g 0" \
  "nvlink -gt d" \
  "nvlink -gt r" \
  "nvlink -gLowPwrInfo" \
  "nvlink -gBwMode" \
  "nvlink -gLWidth" \
  "nvlink -cBridge"; do
  echo "### $SMI $args" >>"$LOG"
  $SMI $args >>"$LOG" 2>&1
  echo "" >>"$LOG"
done

if [ "${INCLUDE_RESET:-0}" = "1" ]; then
  for args in "nvlink -r 0" "nvlink -re" "nvlink -e"; do
    echo "### $SMI $args" >>"$LOG"
    $SMI $args >>"$LOG" 2>&1
    echo "" >>"$LOG"
  done
fi

echo "wrote $LOG"
