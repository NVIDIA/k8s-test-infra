import { describe, it, expect } from 'vitest';
import { pickVelocity, PRESET_DURATIONS, type Duration, type PresetDuration } from '../duration';
import type { Velocity, VelocityDay, VelocityWeek } from '../../types';

// Build distinguishable fixtures so daily and weekly arrays return DIFFERENT
// values for any given index. This catches a bug where pickVelocity maps a
// daily-granularity duration ('7d') to the weekly array. `closed` values
// are also distinct from `opened` to catch a swap regression.
function makeFixture(): Velocity {
  const daily: VelocityDay[] = Array.from({ length: 365 }, (_, i) => ({
    date: `2026-${String(Math.floor(i / 31) + 1).padStart(2, '0')}-${String((i % 28) + 1).padStart(2, '0')}`,
    opened: 1000 + i,
    closed: 500 + i,
  }));
  const weekly: VelocityWeek[] = Array.from({ length: 260 }, (_, i) => ({
    week: `2026-week-${i}`,
    opened: i,
    closed: 500 - i,
  }));
  return { daily, weekly };
}

function preset(value: PresetDuration): Duration {
  return { kind: 'preset', value };
}

describe('pickVelocity (preset)', () => {
  it.each<[PresetDuration, 'day' | 'week', number]>([
    ['7d', 'day', 7],
    ['4w', 'week', 4],
    ['12w', 'week', 12],
    ['6m', 'week', 26],
    ['1y', 'week', 52],
    ['5y', 'week', 260],
  ])('preset=%s → granularity=%s, length=%d', (value, wantGranularity, wantLength) => {
    const v = makeFixture();
    const got = pickVelocity(v, preset(value));

    expect(got.granularity).toBe(wantGranularity);
    expect(got.points).toHaveLength(wantLength);

    if (wantGranularity === 'day') {
      expect(got.points[0].opened).toBe(1000 + (365 - wantLength));
      expect(got.points[wantLength - 1].opened).toBe(1000 + 364);
      expect(got.points[0].closed).toBe(500 + (365 - wantLength));
      expect(got.points[wantLength - 1].closed).toBe(500 + 364);
    } else {
      expect(got.points[0].opened).toBe(260 - wantLength);
      expect(got.points[wantLength - 1].opened).toBe(259);
      expect(got.points[0].closed).toBe(500 - (260 - wantLength));
      expect(got.points[wantLength - 1].closed).toBe(500 - 259);
    }
  });

  it('exposes PRESET_DURATIONS in display order', () => {
    expect(PRESET_DURATIONS).toEqual(['7d', '4w', '12w', '6m', '1y', '5y']);
  });
});
