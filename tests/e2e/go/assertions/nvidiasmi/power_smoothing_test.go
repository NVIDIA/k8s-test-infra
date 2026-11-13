// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two blocks are real `nvidia-smi -q -x` output: nvidia-smi 580.65.06
// against the mock on the gb200 profile, before any write and after
// `power-smoothing -p 2 -ptf 65.5 -rur 30000 -rdr 15000 -rdh 250`, `-spp 2` and
// `-s 1`.
const (
	smoothingDefaultBlock = `<power_smoothing>
			<power_smoothing_enabled>No</power_smoothing_enabled>
			<power_smoothing_priv_level>N/A</power_smoothing_priv_level>
			<power_smoothing_imm_ramp_down>Disabled</power_smoothing_imm_ramp_down>
			<power_smoothing_curr_tmp>1000.00 W</power_smoothing_curr_tmp>
			<power_smoothing_curr_tmp_floor>0.00 W</power_smoothing_curr_tmp_floor>
			<power_smoothing_max_percent_tmp_floor>100.00%</power_smoothing_max_percent_tmp_floor>
			<power_smoothing_min_percent_tmp_floor>0.00%</power_smoothing_min_percent_tmp_floor>
			<power_smoothing_hw_circuitry_percent_lifetime_remaining>100.000000000000%</power_smoothing_hw_circuitry_percent_lifetime_remaining>
			<power_smoothing_num_preset_profiles>5</power_smoothing_num_preset_profiles>
			<power_smoothing_current_profile>
				<power_smoothing_curr_profile_percent_tmp_floor>0.00%</power_smoothing_curr_profile_percent_tmp_floor>
				<power_smoothing_curr_profile_ramp_up_rate>0.00 W/s</power_smoothing_curr_profile_ramp_up_rate>
				<power_smoothing_curr_profile_ramp_down_rate>0.00 W/s</power_smoothing_curr_profile_ramp_down_rate>
				<power_smoothing_curr_profile_ramp_down_hysteresis>0 ms</power_smoothing_curr_profile_ramp_down_hysteresis>
			</power_smoothing_current_profile>
			<power_smoothing_active_profile>0</power_smoothing_active_profile>
			<power_smoothing_admin_overrides>
				<power_smoothing_admin_override_percent_tmp_floor>Not Set</power_smoothing_admin_override_percent_tmp_floor>
				<power_smoothing_admin_override_ramp_up_rate>Not Set</power_smoothing_admin_override_ramp_up_rate>
				<power_smoothing_admin_override_ramp_down_rate>Not Set</power_smoothing_admin_override_ramp_down_rate>
				<power_smoothing_admin_override_ramp_down_hysteresis>Not Set</power_smoothing_admin_override_ramp_down_hysteresis>
			</power_smoothing_admin_overrides>
		</power_smoothing>`

	smoothingTunedBlock = `<power_smoothing>
			<power_smoothing_enabled>Yes</power_smoothing_enabled>
			<power_smoothing_priv_level>N/A</power_smoothing_priv_level>
			<power_smoothing_imm_ramp_down>Disabled</power_smoothing_imm_ramp_down>
			<power_smoothing_curr_tmp>1000.00 W</power_smoothing_curr_tmp>
			<power_smoothing_curr_tmp_floor>655.00 W</power_smoothing_curr_tmp_floor>
			<power_smoothing_max_percent_tmp_floor>100.00%</power_smoothing_max_percent_tmp_floor>
			<power_smoothing_min_percent_tmp_floor>0.00%</power_smoothing_min_percent_tmp_floor>
			<power_smoothing_hw_circuitry_percent_lifetime_remaining>100.000000000000%</power_smoothing_hw_circuitry_percent_lifetime_remaining>
			<power_smoothing_num_preset_profiles>5</power_smoothing_num_preset_profiles>
			<power_smoothing_current_profile>
				<power_smoothing_curr_profile_percent_tmp_floor>65.50%</power_smoothing_curr_profile_percent_tmp_floor>
				<power_smoothing_curr_profile_ramp_up_rate>30.00 W/s</power_smoothing_curr_profile_ramp_up_rate>
				<power_smoothing_curr_profile_ramp_down_rate>15.00 W/s</power_smoothing_curr_profile_ramp_down_rate>
				<power_smoothing_curr_profile_ramp_down_hysteresis>250 ms</power_smoothing_curr_profile_ramp_down_hysteresis>
			</power_smoothing_current_profile>
			<power_smoothing_active_profile>2</power_smoothing_active_profile>
			<power_smoothing_admin_overrides>
				<power_smoothing_admin_override_percent_tmp_floor>Not Set</power_smoothing_admin_override_percent_tmp_floor>
				<power_smoothing_admin_override_ramp_up_rate>Not Set</power_smoothing_admin_override_ramp_up_rate>
				<power_smoothing_admin_override_ramp_down_rate>Not Set</power_smoothing_admin_override_ramp_down_rate>
				<power_smoothing_admin_override_ramp_down_hysteresis>Not Set</power_smoothing_admin_override_ramp_down_hysteresis>
			</power_smoothing_admin_overrides>
		</power_smoothing>`
)

// tunedPreset is the preset smoothingTunedBlock reports as current.
var tunedPreset = PowerSmoothingPreset{
	PercentTMPFloor: 65.5, RampUpWPerS: 30, RampDownWPerS: 15, RampDownHysteresisMS: 250,
}

// smoothingDoc wraps one GPU's block in a document, beside the power limit the
// ceiling is checked against.
func smoothingDoc(block, limit string) string {
	return `<?xml version="1.0" ?><nvidia_smi_log><attached_gpus>1</attached_gpus><gpu id="00000000:01:00.0">` +
		`<gpu_power_readings><current_power_limit>` + limit + `</current_power_limit></gpu_power_readings>` +
		block + `</gpu></nvidia_smi_log>`
}

func TestPowerSmoothingProblems_AcceptsDefaultBlock(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerSmoothingProblems(smoothingDoc(smoothingDefaultBlock, "1000.00 W"),
		PowerSmoothingExpectation{Supported: true}))
}

func TestPowerSmoothingProblems_AcceptsTunedBlock(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerSmoothingProblems(smoothingDoc(smoothingTunedBlock, "1000.00 W"),
		PowerSmoothingExpectation{Supported: true, Enabled: true, ActiveProfile: 2, Current: &tunedPreset}))
}

// qx-gb200-healthy.xml renders the block N/A on a Blackwell board, which is
// what a mock that does not model power smoothing reports.
func TestPowerSmoothingProblems_RejectsStubBehaviour(t *testing.T) {
	t.Parallel()
	problems := PowerSmoothingProblems(loadFixture(t, "qx-gb200-healthy.xml"),
		PowerSmoothingExpectation{Supported: true})
	require.Len(t, problems, 2, "one per GPU")
	require.Contains(t, problems[0], `= "N/A", want a populated block`)
}

func TestPowerSmoothingProblems_AcceptsUnsupportedBoard(t *testing.T) {
	t.Parallel()
	require.Empty(t, PowerSmoothingProblems(loadFixture(t, "qx-a100-healthy.xml"), PowerSmoothingExpectation{}))
}

// The negative control: a board that should decline must not render a block.
func TestPowerSmoothingProblems_RejectsBlockOnUnsupportedBoard(t *testing.T) {
	t.Parallel()
	problems := PowerSmoothingProblems(smoothingDoc(smoothingDefaultBlock, "1000.00 W"), PowerSmoothingExpectation{})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], `want "N/A" on a board without power smoothing`)
}

// The real Blackwell captures render the block as a permission error, which is
// not a populated block either.
func TestPowerSmoothingProblems_RejectsInsufficientPermissions(t *testing.T) {
	t.Parallel()
	problems := PowerSmoothingProblems(smoothingDoc("<power_smoothing>Insufficient Permissions</power_smoothing>",
		"1000.00 W"), PowerSmoothingExpectation{Supported: true})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], `"Insufficient Permissions"`)
}

func TestPowerSmoothingProblems_RejectsWrongState(t *testing.T) {
	t.Parallel()
	problems := PowerSmoothingProblems(smoothingDoc(smoothingTunedBlock, "1000.00 W"),
		PowerSmoothingExpectation{Supported: true, Enabled: false, ActiveProfile: 0,
			Current: &PowerSmoothingPreset{}})
	joined := strings.Join(problems, "\n")
	require.Len(t, problems, 3, joined)
	require.Contains(t, joined, "enabled = true, want false")
	require.Contains(t, joined, "active profile = 2, want 0")
	require.Contains(t, joined, "current profile =")
}

// The ceiling has to follow the power limit, which is what `nvidia-smi -pl`
// moves; a ceiling read from the profile instead would drift from it.
func TestPowerSmoothingProblems_RejectsCeilingOffThePowerLimit(t *testing.T) {
	t.Parallel()
	problems := PowerSmoothingProblems(smoothingDoc(smoothingDefaultBlock, "800.00 W"),
		PowerSmoothingExpectation{Supported: true})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "TMP ceiling = 1000.00 W, want the 800.00 W power limit")
}

// A floor that ignores the active preset is a write that was stored but never
// applied, which the state fields alone cannot reveal.
func TestPowerSmoothingProblems_RejectsUnappliedFloor(t *testing.T) {
	t.Parallel()
	out := strings.Replace(smoothingDoc(smoothingTunedBlock, "1000.00 W"), "655.00 W", "0.00 W", 1)
	problems := PowerSmoothingProblems(out,
		PowerSmoothingExpectation{Supported: true, Enabled: true, ActiveProfile: 2, Current: &tunedPreset})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "TMP floor = 0.00 W, want 655.00 W")
}

func TestPowerSmoothingProblems_RejectsAdminOverride(t *testing.T) {
	t.Parallel()
	out := strings.Replace(smoothingDoc(smoothingDefaultBlock, "1000.00 W"),
		"<power_smoothing_admin_override_ramp_up_rate>Not Set", "<power_smoothing_admin_override_ramp_up_rate>5.00 W/s", 1)
	problems := PowerSmoothingProblems(out, PowerSmoothingExpectation{Supported: true})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "an admin override is set")
}

// smoothingPPD is real `nvidia-smi power-smoothing -i 1 -ppd` output after the
// writes smoothingTunedBlock reflects.
const smoothingPPD = `    Preset Profile 0 Definition
            % Tmp Floor                 : 0.00%
            Ramp Up Rate                : 0.00 W/s
            Ramp Down Rate              : 0.00 W/s
            Ramp Down Hysteresis        : 0 ms
    Preset Profile 1 Definition
            % Tmp Floor                 : 0.00%
            Ramp Up Rate                : 0.00 W/s
            Ramp Down Rate              : 0.00 W/s
            Ramp Down Hysteresis        : 0 ms
    Preset Profile 2 Definition
            % Tmp Floor                 : 65.50%
            Ramp Up Rate                : 30.00 W/s
            Ramp Down Rate              : 15.00 W/s
            Ramp Down Hysteresis        : 250 ms
    Preset Profile 3 Definition
            % Tmp Floor                 : 0.00%
            Ramp Up Rate                : 0.00 W/s
            Ramp Down Rate              : 0.00 W/s
            Ramp Down Hysteresis        : 0 ms
    Preset Profile 4 Definition
            % Tmp Floor                 : 0.00%
            Ramp Up Rate                : 0.00 W/s
            Ramp Down Rate              : 0.00 W/s
            Ramp Down Hysteresis        : 0 ms
`

func TestParsePresetDefinitions_DecodesRealOutput(t *testing.T) {
	t.Parallel()
	presets, err := ParsePresetDefinitions(smoothingPPD)
	require.NoError(t, err)
	require.Equal(t, map[int]PowerSmoothingPreset{
		0: {}, 1: {}, 2: tunedPreset, 3: {}, 4: {},
	}, presets)
}

// Without -i nvidia-smi repeats the five presets per GPU with nothing between
// them, so a second GPU's block would silently replace the first's.
func TestParsePresetDefinitions_RejectsSeveralGPUs(t *testing.T) {
	t.Parallel()
	_, err := ParsePresetDefinitions(smoothingPPD + smoothingPPD)
	require.ErrorContains(t, err, "preset 0 defined twice")
}

func TestParsePresetDefinitions_RejectsRefusal(t *testing.T) {
	t.Parallel()
	_, err := ParsePresetDefinitions("Power Smoothing is not supported on this device.\n")
	require.ErrorContains(t, err, "no preset definitions")
}
