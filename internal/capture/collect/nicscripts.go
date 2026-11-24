// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package collect

// The scripts below run in the host pod, or with -exec-in in a pod the cluster
// already runs, so they keep to POSIX sh and what BusyBox has. They only read.
// A script whose tool is missing exits 127, so its file is marked FAILED in
// errors.log; a sysfs file that does not exist leaves no line.

// needTool prints the first of its arguments that is on the PATH, or exits
// 127. MFT names its tools mlxconfig, flint and mlxlink; mstflint, which some
// operator images carry, names the same tools mstconfig, mstflint, mstlink.
const needTool = `need() {
  for t; do command -v "$t" >/dev/null 2>&1 && { echo "$t"; return 0; }; done
  echo "none of $* found" >&2
  exit 127
}
`

// nicFunctions loops over the NIC's physical network functions (Ethernet or
// InfiniBand class, not a virtual function), setting b to the bus ID.
const nicFunctions = `for d in /sys/bus/pci/devices/*; do
  case "$(cat $d/vendor 2>/dev/null)$(cat $d/class 2>/dev/null)" in 0x15b30x0200*|0x15b30x0207*) ;; *) continue ;; esac
  [ -e $d/physfn ] && continue
  b=${d##*/}
`

const rdmaSysfsScript = `for h in /sys/class/infiniband/*; do
  [ -e $h ] || continue
  echo "== $(basename $h)"
  for f in node_guid node_desc node_type sys_image_guid fw_ver hca_type board_id; do echo "$f=$(cat $h/$f 2>&1)"; done
  for f in hw_rev; do [ -e $h/$f ] && echo "$f=$(cat $h/$f 2>&1)"; done
  for f in vendor device class numa_node subsystem_vendor subsystem_device; do echo "device/$f=$(cat $h/device/$f 2>&1)"; done
  echo "devpath=$(readlink -f $h/device)"
  echo "driver=$(basename "$(readlink -f $h/device/driver 2>/dev/null)")"
  echo "files=$(ls $h | tr '\n' ',' | sed 's/,$//')"
  for p in $h/ports/*; do
    echo "-- port $(basename $p)"
    for f in state phys_state rate lid sm_lid sm_sl lid_mask_count link_layer cap_mask; do echo "$f=$(cat $p/$f 2>&1)"; done
    echo "pkeys=$(cat $p/pkeys/* 2>/dev/null | grep -v '^0x0000$' | sort -u | tr '\n' ',' | sed 's/,$//')"
    for d in counters hw_counters; do echo "$d=$(ls $p/$d 2>/dev/null | tr '\n' ',' | sed 's/,$//')"; done
    echo "files=$(ls $p | tr '\n' ',' | sed 's/,$//')"
    for g in $p/gids/*; do
      n=$(basename $g)
      t=$(cat $p/gid_attrs/types/$n 2>/dev/null) || continue
      echo "gid[$n]=$(cat $g 2>&1) type=$t ndev=$(cat $p/gid_attrs/ndevs/$n 2>&1)"
    done
  done
done
`

const rdmaCountersScript = `for h in /sys/class/infiniband/*; do
  [ -e $h ] || continue
  p=$h/ports/1
  echo "== $(basename $h) port 1"
  for d in counters hw_counters; do
    for c in $p/$d/*; do [ -f "$c" ] && echo "$d/$(basename $c)=$(cat $c 2>&1)"; done
  done
done
true
`

// pciNICSysfsScript prints every NIC PCI function's sysfs attributes, the
// bridges of the NIC's own PCIe switch included: one "== <bus ID>" block each.
// upstream lists the vendor:device of each bridge from the root port down.
const pciNICSysfsScript = `for d in /sys/bus/pci/devices/*; do
  [ "$(cat $d/vendor 2>/dev/null)" = 0x15b3 ] || continue
  b=${d##*/}
  echo "== $b"
  for f in vendor device subsystem_vendor subsystem_device class revision numa_node local_cpulist \
    current_link_speed current_link_width max_link_speed max_link_width sriov_totalvfs sriov_numvfs \
    mlx5_num_vfs roce_enable modalias reset_method ari_enabled d3cold_allowed power_state; do
    [ -e $d/$f ] && echo "$f=$(cat $d/$f 2>&1)"
  done
  echo "driver=$(basename "$(readlink $d/driver 2>/dev/null)")"
  echo "devpath=$(readlink -f $d)"
  up=""
  p=$(dirname "$(readlink -f $d)")
  while [ -r $p/vendor ]; do up="$(cat $p/vendor):$(cat $p/device)${up:+,$up}"; p=$(dirname $p); done
  echo "upstream=$up"
  [ -e $d/physfn ] && echo "physfn=$(basename "$(readlink $d/physfn)")"
  echo "virtfns=$(ls -d $d/virtfn* 2>/dev/null | wc -l)"
  [ -e $d/iommu_group ] && echo "iommu_group=$(basename "$(readlink $d/iommu_group)")"
  echo "hwmon=$(ls $d/hwmon 2>/dev/null | wc -l)"
  echo "ptp=$(ls $d/ptp 2>/dev/null | wc -l)"
  echo "aux=$(ls -d $d/mlx5_core.* 2>/dev/null | sed 's#.*/mlx5_core\.##; s#\.[0-9]*$##' | sort -u | tr '\n' ',' | sed 's/,$//')"
  echo "irqs=$(grep -c "@pci:$b" /proc/interrupts)"
  echo "files=$(ls $d | tr '\n' ',' | sed 's/,$//')"
done
true
`

// pciVPDScript decodes each NIC physical function's vital product data from
// sysfs and prints it as lspci -vv does, so it can be read without lspci. The
// format is PCI's: tag 0x82 holds the product name, tags 0x90 and 0x91 hold
// keyword records (two-letter key, length, value), and any other tag ends it.
const pciVPDScript = `for d in /sys/bus/pci/devices/*; do
  [ "$(cat $d/vendor 2>/dev/null)" = 0x15b3 ] && [ -r $d/vpd ] && [ ! -e $d/physfn ] || continue
  echo "== ${d##*/}"
  od -An -v -tu1 $d/vpd 2>/dev/null | awk '` + vpdAwk + `'
done
true
`

// vpdAwk reads vital product data as the decimal bytes od -tu1 prints.
const vpdAwk = `
{ for (i = 1; i <= NF; i++) b[n++] = $i }
function str(from, len,   s, k) {
  s = ""
  for (k = from; k < from + len && k < n; k++) if (b[k] >= 32 && b[k] < 127) s = s sprintf("%c", b[k])
  sub(/ +$/, "", s)
  return s
}
function label(k) {
  if (k == "PN") return "Part number"
  if (k == "EC") return "Engineering changes"
  if (k == "SN") return "Serial number"
  if (k == "MN") return "Manufacture ID"
  if (k == "CP") return "Extended capability"
  if (k == "YA") return "Asset tag"
  if (k ~ /^V/) return "Vendor specific"
  if (k ~ /^Y/) return "System specific"
  return "Unknown"
}
END {
  i = 0
  while (i + 2 < n) {
    tag = b[i]; len = b[i+1] + 256 * b[i+2]
    if (tag == 130) printf "Product Name: %s\n", str(i + 3, len)
    else if (tag == 144 || tag == 145) {
      for (j = i + 3; j + 2 < i + 3 + len; j += 3 + b[j+2]) {
        key = sprintf("%c%c", b[j], b[j+1])
        if (key != "RV" && key != "RW") printf "[%s] %s: %s\n", key, label(key), str(j + 3, b[j+2])
      }
    } else break
    i += 3 + len
  }
}
`

// rdmaCharDevicesScript lists the RDMA device files and the sysfs classes that
// describe them: each file's type, device number and mode, the udev links,
// and for each verbs and MAD node its RDMA device, port, device number and
// whether its PCI function links back to it (backlink, from the function's
// own uevent, which container runtimes' hooks read).
const rdmaCharDevicesScript = `D=/dev/infiniband
echo "== $D"
for f in $D/* $D/*/*; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  n=${f#$D/}
  if [ -L "$f" ]; then echo "$n type=link target=$(readlink "$f")"
  elif [ -d "$f" ]; then echo "$n type=dir"
  elif [ -c "$f" ]; then echo "$n type=char dev=$(stat -c %t:%T "$f") mode=$(stat -c %a "$f")"
  else echo "$n type=file mode=$(stat -c %a "$f")"; fi
done
for c in infiniband_verbs infiniband_mad; do
  echo "== /sys/class/$c"
  [ -e /sys/class/$c/abi_version ] && echo "abi_version=$(cat /sys/class/$c/abi_version)"
  for v in /sys/class/$c/*; do
    [ -e $v/ibdev ] || continue
    n=${v##*/}
    ib=$(cat $v/ibdev)
    line="$n ibdev=$ib dev=$(cat $v/dev)"
    [ -e $v/port ] && line="$line port=$(cat $v/port)"
    [ -e $v/abi_version ] && line="$line abi_version=$(cat $v/abi_version)"
    ue="$(readlink -f /sys/class/infiniband/$ib/device)/$c/$n/uevent"
    if [ -r "$ue" ]; then
      line="$line backlink=$(sed -n 's/^MAJOR=//p' "$ue"):$(sed -n 's/^MINOR=//p' "$ue")"
    else
      line="$line backlink=none"
    fi
    echo "$line"
  done
done
true
`

// sysModuleRDMAScript prints the NIC and RDMA kernel modules: whether each is
// loaded (initstate live) or built in (no initstate), its version, and its
// parameter values.
const sysModuleRDMAScript = `for m in mlx5_core mlx5_ib mlx5_fwctl mlx5_dpll mlxfw mlxdevm mlx_compat ib_core ib_uverbs ib_umad ib_ipoib ib_cm iw_cm rdma_cm rdma_ucm nvidia_peermem gdrdrv; do
  [ -d /sys/module/$m ] || continue
  echo "== $m"
  for f in initstate version; do [ -e /sys/module/$m/$f ] && echo "$f=$(cat /sys/module/$m/$f 2>&1)"; done
  for p in /sys/module/$m/parameters/*; do [ -f "$p" ] && echo "parameters/${p##*/}=$(cat "$p" 2>&1)"; done
done
true
`

// netECNQoSScript prints the ECN and QoS settings the NIC driver exposes under
// each interface (/sys/class/net/<if>/ecn and qos).
const netECNQoSScript = `for i in /sys/class/net/*; do
  [ -d $i/ecn ] || [ -d $i/qos ] || continue
  echo "== ${i##*/}"
  for f in $(find $i/ecn $i/qos -type f 2>/dev/null | sort); do echo "${f#$i/}=$(cat $f 2>&1 | tr '\n' ' ' | sed 's/ *$//')"; done
done
true
`

// nicInterfaces loops over the network interfaces on a NIC function, setting
// n to the interface name.
const nicInterfaces = `for i in /sys/class/net/*; do
  [ "$(cat $i/device/vendor 2>/dev/null)" = 0x15b3 ] || continue
  n=${i##*/}
  echo "== $n"
`

const netEthtoolScript = `command -v ethtool >/dev/null 2>&1 || { echo "ethtool not found" >&2; exit 127; }
` + nicInterfaces + `  ethtool $n </dev/null 2>&1
done
true
`

// netEthtoolModuleScript reads each interface's cable or transceiver EEPROM.
const netEthtoolModuleScript = `command -v ethtool >/dev/null 2>&1 || { echo "ethtool not found" >&2; exit 127; }
` + nicInterfaces + `  ethtool -m $n </dev/null 2>&1
done
true
`

const devlinkEswitchScript = `devs=$(devlink dev show </dev/null) || exit 1
for d in $devs; do echo "== $d"; devlink dev eswitch show $d </dev/null 2>&1; done
true
`

// fwMLXConfigScript prints each NIC's firmware configuration: all parameters,
// then the module split, which mlxconfig lists only when asked by index. One
// query per ASIC, through its function 0.
const fwMLXConfigScript = needTool + `t=$(need mlxconfig mstconfig) || exit 127
` + nicFunctions + `  case $b in *.0) ;; *) continue ;; esac
  echo "== $b"
  timeout 120 $t -d $b -e q </dev/null 2>&1
  timeout 60 $t -d $b q 'MODULE_SPLIT_M0[0..15]' </dev/null 2>&1
done
true
`

const fwFlintScript = needTool + `t=$(need flint mstflint) || exit 127
` + nicFunctions + `  case $b in *.0) ;; *) continue ;; esac
  echo "== $b"
  timeout 60 $t -d $b q </dev/null 2>&1
done
true
`

// cableMLXLinkScript prints each port's link and cable as the NIC sees them,
// without the colour codes mlxlink adds.
const cableMLXLinkScript = needTool + `t=$(need mlxlink mstlink) || exit 127
e=$(printf '\033')
` + nicFunctions + `  echo "== $b"
  timeout 60 $t -d $b -m </dev/null 2>&1 | sed "s/$e\[[0-9;]*m//g"
done
true
`
