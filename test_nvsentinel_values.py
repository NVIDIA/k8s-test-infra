import pathlib
import re
import unittest


class NvSentinelValuesTest(unittest.TestCase):
    def test_suppressed_error_codes_include_chart_defaults(self):
        values = pathlib.Path("docs/guides/nv-sentinel/nvsentinel-values.yaml").read_text()
        match = re.search(r"(?ms)^gpu-health-monitor:\n.*?^    suppressedErrorCodes:\n((?:      - .*\n)+)", values)
        self.assertIsNotNone(match)
        codes = {line.removeprefix("      - ").strip() for line in match.group(1).splitlines()}
        self.assertTrue({
            "DCGM_FR_CLOCK_THROTTLE_POWER",
            "DCGM_FR_CLOCKS_EVENT_POWER",
            "DCGM_FR_CLOCK_THROTTLE_THERMAL",
            "DCGM_FR_CLOCKS_EVENT_THERMAL",
            "DCGM_FR_NVLINK_EFFECTIVE_BER_THRESHOLD",
            "DCGM_FR_NVLINK_SYMBOL_BER_THRESHOLD",
        }.issubset(codes))


if __name__ == "__main__":
    unittest.main()
