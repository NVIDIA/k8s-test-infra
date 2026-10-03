// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The fixtures below are captured from a MIG-enabled h100 node — eight boards
// each carved into seven 1g.10gb partitions — and trimmed to GPUs 0 and 1. The
// column padding is the driver's own and is reproduced verbatim, since it is
// exactly what a naive field split would trip over.

const migGPUInstanceListing = `+---------------------------------------------------------+
| GPU instances:                                          |
| GPU   Name               Profile  Instance   Placement  |
|                            ID       ID       Start:Size |
|=========================================================|
|   0  MIG 1g.10gb            0        0          0:1     |
+---------------------------------------------------------+
|   0  MIG 1g.10gb            0        1          1:1     |
+---------------------------------------------------------+
|   0  MIG 1g.10gb            0        2          2:1     |
+---------------------------------------------------------+
|   0  MIG 1g.10gb            0        3          3:1     |
+---------------------------------------------------------+
|   0  MIG 1g.10gb            0        4          4:1     |
+---------------------------------------------------------+
|   0  MIG 1g.10gb            0        5          5:1     |
+---------------------------------------------------------+
|   0  MIG 1g.10gb            0        6          6:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        0          0:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        1          1:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        2          2:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        3          3:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        4          4:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        5          5:1     |
+---------------------------------------------------------+
|   1  MIG 1g.10gb            0        6          6:1     |
+---------------------------------------------------------+
`

// Note the "+me" profile and the two-line rows: the second line carries a
// row's remaining counters and must not be read as a row of its own.
const migProfileListing = `+-------------------------------------------------------------------------------+
| GPU instance profiles:                                                        |
| GPU   Name               ID    Instances   Memory     P2P    SM    DEC   ENC  |
|                                Free/Total   GiB              CE    JPEG  OFA  |
|===============================================================================|
|   0  MIG 1g.10gb          0     0/7        10.00      Yes    16     1     0   |
|                                                               1     0     0   |
+-------------------------------------------------------------------------------+
|   0  MIG 1g.10gb+me       7     0/1        10.00      Yes    16     1     0   |
|                                                               1     1     1   |
+-------------------------------------------------------------------------------+
|   0  MIG 1g.20gb          9     0/4        20.00      Yes    16     1     0   |
|                                                               1     0     0   |
+-------------------------------------------------------------------------------+
|   0  MIG 2g.20gb          1     0/3        20.00      Yes    32     1     0   |
|                                                               2     0     0   |
+-------------------------------------------------------------------------------+
|   0  MIG 3g.40gb          2     0/2        40.00      Yes    48     2     0   |
|                                                               3     0     0   |
+-------------------------------------------------------------------------------+
|   0  MIG 4g.40gb          3     0/1        40.00      Yes    64     2     0   |
|                                                               4     0     0   |
+-------------------------------------------------------------------------------+
|   0  MIG 7g.80gb          4     0/1        80.00      Yes    112    5     0   |
|                                                               7     0     0   |
+-------------------------------------------------------------------------------+
|   1  MIG 1g.10gb          0     0/7        10.00      Yes    16     1     0   |
|                                                               1     0     0   |
+-------------------------------------------------------------------------------+
|   1  MIG 1g.10gb+me       7     0/1        10.00      Yes    16     1     0   |
|                                                               1     1     1   |
+-------------------------------------------------------------------------------+
|   1  MIG 1g.20gb          9     0/4        20.00      Yes    16     1     0   |
|                                                               1     0     0   |
+-------------------------------------------------------------------------------+
|   1  MIG 2g.20gb          1     0/3        20.00      Yes    32     1     0   |
|                                                               2     0     0   |
+-------------------------------------------------------------------------------+
|   1  MIG 3g.40gb          2     0/2        40.00      Yes    48     2     0   |
|                                                               3     0     0   |
+-------------------------------------------------------------------------------+
|   1  MIG 4g.40gb          3     0/1        40.00      Yes    64     2     0   |
|                                                               4     0     0   |
+-------------------------------------------------------------------------------+
|   1  MIG 7g.80gb          4     0/1        80.00      Yes    112    5     0   |
|                                                               7     0     0   |
+-------------------------------------------------------------------------------+
`

// `nvidia-smi mig -lci`, GPU 0 only. Its extra GPU-instance column shifts every
// field along, which is why the row counter keys on the profile name rather
// than on a column position.
const migComputeInstanceListing = `+--------------------------------------------------------------------+
| Compute instances:                                                 |
| GPU     GPU       Name             Profile   Instance   Placement  |
|       Instance                       ID        ID       Start:Size |
|         ID                                                         |
|====================================================================|
|   0      0       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
|   0      1       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
|   0      2       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
|   0      3       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
|   0      4       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
|   0      5       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
|   0      6       MIG 1g.10gb          0         0          0:1     |
+--------------------------------------------------------------------+
`

// `nvidia-smi mig -lcip`, GPU 0 only. The profile ID carries a "*" marking the
// default profile, and the rows are two lines like -lgip's. This is a
// catalogue rather than an inventory: this board offers one compute instance
// profile per partition, others offer several rows for the same partition.
const migComputeProfileListing = `+--------------------------------------------------------------------------------------+
| Compute instance profiles:                                                           |
| GPU     GPU       Name             Profile  Instances   Exclusive       Shared       |
|       Instance                       ID     Free/Total     SM       DEC   ENC   OFA  |
|         ID                                                          CE    JPEG       |
|======================================================================================|
|   0      0       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
|   0      1       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
|   0      2       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
|   0      3       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
|   0      4       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
|   0      5       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
|   0      6       MIG 1g.10gb          0*     0/1           16        0     0     0   |
|                                                                      0     0         |
+--------------------------------------------------------------------------------------+
`

func TestListMigGPUInstances(t *testing.T) {
	t.Parallel()

	instances, err := ListMigGPUInstances(migGPUInstanceListing)
	require.NoError(t, err)
	require.Len(t, instances, 14)

	require.Equal(t, MigGPUInstance{
		GPU: 0, Profile: "1g.10gb", ProfileID: 0, InstanceID: 0, Placement: "0:1",
	}, instances[0])
	// The last partition of the second board: attribution has to survive the
	// instance IDs restarting at 0 on every GPU.
	require.Equal(t, MigGPUInstance{
		GPU: 1, Profile: "1g.10gb", ProfileID: 0, InstanceID: 6, Placement: "6:1",
	}, instances[13])

	// The profile name is stripped of its "MIG " prefix so it compares with
	// what `nvidia-smi -L` reports for the same partitions.
	require.Equal(t, "1g.10gb", ListMigDevices(
		"GPU 0: NVIDIA H100 80GB HBM3 (UUID: GPU-a)\n"+
			"  MIG 1g.10gb     Device  0: (UUID: MIG-b)\n")[0].Profile)
}

func TestMigInstancesOfGPU(t *testing.T) {
	t.Parallel()

	instances, err := ListMigGPUInstances(migGPUInstanceListing)
	require.NoError(t, err)

	gpu1 := MigInstancesOfGPU(instances, 1)
	require.Len(t, gpu1, 7)
	for _, i := range gpu1 {
		require.Equal(t, 1, i.GPU)
	}
	require.Empty(t, MigInstancesOfGPU(instances, 7),
		"a GPU absent from the listing has no partitions, not the whole listing's")
}

// The listing an unpartitioned but MIG-capable board produces. This is a real
// answer rather than a parse failure, so it must not be reported as one.
func TestListMigGPUInstances_NoInstances(t *testing.T) {
	t.Parallel()

	const out = `+---------------------------------------------------------+
| GPU instances:                                          |
| GPU   Name               Profile  Instance   Placement  |
|                            ID       ID       Start:Size |
|=========================================================|
+---------------------------------------------------------+
`
	instances, err := ListMigGPUInstances(out)
	require.NoError(t, err)
	require.Empty(t, instances)
}

// The negative control. Every e2e assertion built on this parser reads its
// result as "the partitions the board has", so output the parser does not
// understand has to surface as an error: returning an empty listing instead
// would quietly turn a count assertion into a comparison of 0 against 0 and a
// "for every partition" assertion into a loop over nothing.
func TestListMigGPUInstances_RejectsOutputItDoesNotUnderstand(t *testing.T) {
	t.Parallel()

	for name, out := range map[string]string{
		// What the command prints when NVML refuses the query: an error on
		// stderr, exit 255, and no table at all.
		"nvml error": "Failed to display GPU instances: Insufficient Size\n",
		"empty":      "",
		// A different listing. Handing -lgip's output to the -lgi parser is
		// the shape a mixed-up call site takes.
		"wrong listing": migProfileListing,
		// The banner survives but the columns move. A parser keyed only on
		// the banner would report an unpartitioned board here.
		"column layout changed": `+---------------------------------------------------------+
| GPU instances:                                          |
| GPU   Name               Profile  Instance   Placement  |
|=========================================================|
|   0  MIG 1g.10gb            0        0        unknown   |
+---------------------------------------------------------+
`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			instances, err := ListMigGPUInstances(out)
			require.Error(t, err, "parser accepted output it cannot have understood")
			require.Empty(t, instances)
		})
	}
}

func TestListMigProfileCapacity(t *testing.T) {
	t.Parallel()

	profiles, err := ListMigProfileCapacity(migProfileListing)
	require.NoError(t, err)
	require.Len(t, profiles, 14, "seven profiles offered on each of two GPUs")

	require.Equal(t, MigProfileCapacity{
		GPU: 0, Profile: "1g.10gb", ProfileID: 0, Free: 0, Total: 7,
	}, profiles[0], "a board carved into all seven 1g.10gb slots has none free")

	// The "+me" variant sits between the plain profiles and must be kept
	// distinct from them, or a lookup by name would find the wrong occupancy.
	require.Equal(t, MigProfileCapacity{
		GPU: 0, Profile: "1g.10gb+me", ProfileID: 7, Free: 0, Total: 1,
	}, profiles[1])
}

func TestMigCapacityFor(t *testing.T) {
	t.Parallel()

	profiles, err := ListMigProfileCapacity(migProfileListing)
	require.NoError(t, err)

	got, ok := MigCapacityFor(profiles, 1, "7g.80gb")
	require.True(t, ok)
	require.Equal(t, MigProfileCapacity{
		GPU: 1, Profile: "7g.80gb", ProfileID: 4, Free: 0, Total: 1,
	}, got)

	_, ok = MigCapacityFor(profiles, 0, "3g.20gb")
	require.False(t, ok, "a profile this board does not offer is reported missing, not zero-capacity")
	_, ok = MigCapacityFor(profiles, 5, "1g.10gb")
	require.False(t, ok, "a GPU absent from the listing must not match another GPU's row")
}

func TestListMigProfileCapacity_RejectsOutputItDoesNotUnderstand(t *testing.T) {
	t.Parallel()

	for name, out := range map[string]string{
		"nvml error":    "Failed to display GPU instance profiles: Insufficient Size\n",
		"empty":         "",
		"wrong listing": migGPUInstanceListing,
		// Free/Total has traded places with the memory column. The banner
		// still says -lgip, so only the row pattern can catch it, and a
		// parser that skipped the rows it could not read would report a
		// board offering no profiles at all.
		"column layout changed": `+-------------------------------------------------------------------------------+
| GPU instance profiles:                                                        |
| GPU   Name               ID    Memory     Instances    P2P    SM    DEC   ENC  |
|                                GiB        Free/Total          CE    JPEG  OFA  |
|===============================================================================|
|   0  MIG 1g.10gb          0     10.00      0/7        Yes    16     1     0   |
|                                                               1     0     0   |
+-------------------------------------------------------------------------------+
`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profiles, err := ListMigProfileCapacity(out)
			require.Error(t, err, "parser accepted output it cannot have understood")
			require.Empty(t, profiles)
		})
	}
}

func TestCountMigTableRows(t *testing.T) {
	t.Parallel()

	// The compute-instance listings are read only by row count, so the count
	// has to exclude the continuation lines -lcip pads its rows with.
	rows, err := CountMigTableRows(migComputeInstanceListing, MigComputeInstances)
	require.NoError(t, err)
	require.Equal(t, 7, rows)

	rows, err = CountMigTableRows(migComputeProfileListing, MigComputeInstanceProfiles)
	require.NoError(t, err)
	require.Equal(t, 7, rows)
}

// The negative control, and the reason the count takes the listing it is
// counting: on this board -lgi and -lci have the same number of rows, so a
// crossed flag would satisfy every assertion built on the count.
func TestCountMigTableRows_RejectsOutputItDoesNotUnderstand(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		out     string
		listing MigListing
	}{
		"nvml error": {
			"Failed to display compute instances: Insufficient Size\n", MigComputeInstances,
		},
		"empty": {"", MigComputeInstances},
		// -lci's rows are one per partition and so are -lgi's, which is what
		// makes the two tables interchangeable to a bare row count.
		"gpu instances for compute instances": {migGPUInstanceListing, MigComputeInstances},
		// The two compute listings differ only by the word "profiles".
		"compute profiles for compute instances": {migComputeProfileListing, MigComputeInstances},
		"compute instances for compute profiles": {migComputeInstanceListing, MigComputeInstanceProfiles},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rows, err := CountMigTableRows(tc.out, tc.listing)
			require.Error(t, err, "counted rows in output that is not the listing asked for")
			require.Zero(t, rows)
		})
	}
}
