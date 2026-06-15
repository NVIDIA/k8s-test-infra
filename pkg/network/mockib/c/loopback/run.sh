#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Host-side loopback test for the RDMA verbs data-path shim (libibmockrdma.so).
# Runs inside the image's pinned debian:bookworm-slim with the real rdma-core /
# perftest, builds the production shim via the package Makefile, and proves the
# data path two ways:
#
#   1. datapath_test : a focused, deterministic check that real BYTES move
#      A->B (WRITE), A<-B (READ), and A->B (SEND) through the JSON relay.
#   2. ib_write_bw    : STOCK perftest 4.5 (default ibv_wr_* path, NO flags)
#      runs server+client over loopback and reports non-zero bandwidth -- the
#      bar set by docs/magi/spike/FINDINGS.md -- with the relay observing
#      verbs_op frames crossing it.
#
# The reported bandwidth is a functional artifact of a JSON relay, NOT an
# InfiniBand measurement.
#
# Intended to be invoked by docker (see the header of this file's caller):
#   docker run --rm -v "$REPO:/src" -w /src debian:bookworm-slim \
#     sh pkg/network/mockib/c/loopback/run.sh
set -e

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends \
  build-essential libibverbs-dev rdma-core ibverbs-providers perftest >/dev/null
echo "deps installed"

HERE=pkg/network/mockib/c/loopback
MK=pkg/network/mockib
WORK=$(mktemp -d)
SOCK="$WORK/relay.sock"

# 1) Build the PRODUCTION shim via the package Makefile (exercises the wiring).
make -C "$MK" libibmockrdma.so >/dev/null
SHIM="$MK/libibmockrdma.so"
test -e "$SHIM" || { echo "FAIL: shim not built"; exit 1; }
echo "built $(readlink -f "$SHIM")"

# 2) Build the test fixtures.
cc -O2 -Wall -o "$WORK/relayd" "$HERE/relayd.c" -lpthread
cc -O2 -Wall -o "$WORK/datapath_test" "$HERE/datapath_test.c" -libverbs
cc -shared -fPIC -O2 -o "$WORK/libtestenum.so" "$HERE/enum_shim.c"
echo "built fixtures"

# 3) Write a mock sysfs fixture so the shim surfaces a REAL per-port LID/GID
#    (proves B1: fill_port / ibv_query_gid read sysfs, not lid=1 / zero gid).
IBROOT="$WORK/ibroot"
PORTDIR="$IBROOT/sys/class/infiniband/mlx5_0/ports/1"
mkdir -p "$PORTDIR/gids"
printf '0x0102\n' > "$PORTDIR/lid"
printf 'fe80:0000:0000:0000:0001:0002:0003:0004\n' > "$PORTDIR/gids/0"

# 4) Start the relay daemon.
"$WORK/relayd" "$SOCK" 2>"$WORK/relay.log" &
RELAY=$!
trap 'kill $RELAY 2>/dev/null || true' EXIT
sleep 1

export MOCK_IB_RDMA=1
export MOCK_IB_PING_SOCKET="$SOCK"
export MOCK_IB_ROOT="$IBROOT"

# ---- Test 1: focused data-path byte-movement proof ----
echo "================ datapath_test ================"
set +e
LD_PRELOAD="$(readlink -f "$SHIM")" "$WORK/datapath_test"
DT=$?
set -e
if [ $DT -ne 0 ]; then echo "FAIL: datapath_test exit=$DT"; exit 1; fi
echo "datapath_test OK"

# ---- Test 2: stock ib_write_bw over loopback ----
PORT=18599
SIZE=1024
ITERS=200
PRELOAD="$(readlink -f "$SHIM"):$WORK/libtestenum.so"
echo "================ ib_write_bw (stock, ibv_wr_* path) ================"
LD_PRELOAD="$PRELOAD" ib_write_bw -d mlx5_0 -i 1 -p $PORT -s $SIZE -n $ITERS -F \
  >"$WORK/server.out" 2>&1 &
SRV=$!
sleep 2
set +e
LD_PRELOAD="$PRELOAD" timeout 30 ib_write_bw -d mlx5_0 -i 1 -p $PORT -s $SIZE \
  -n $ITERS -F 127.0.0.1 >"$WORK/client.out" 2>&1
CRC=$?
set -e
sleep 1
kill $SRV 2>/dev/null || true
wait $SRV 2>/dev/null || true

echo "---------------- client.out ----------------"
cat "$WORK/client.out"
echo "---------------- relay.log ----------------"
cat "$WORK/relay.log"

if [ $CRC -ne 0 ]; then echo "FAIL: ib_write_bw client exit=$CRC"; exit 1; fi

# Assert a non-zero BW average was reported (column 4 of the data row).
BW=$(awk '/^ *[0-9]+ +[0-9]+ +[0-9.]+ +[0-9.]+/{print $4; exit}' "$WORK/client.out")
echo "BW average = ${BW:-none} MB/sec"
case "$BW" in
  ""|0|0.00) echo "FAIL: no non-zero bandwidth reported"; exit 1 ;;
esac

# The bandwidth number is a weak signal on its own (completions are generated
# locally). Assert the relay actually forwarded verbs_op frames across the
# fabric, so a regression that stops crossing the relay cannot pass.
if ! grep -q "forwarded .* verbs_op frames" "$WORK/relay.log"; then
  echo "FAIL: relay observed no verbs_op traversal"; exit 1
fi

echo "================ LOOPBACK TEST PASSED ================"
