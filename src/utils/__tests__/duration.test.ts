import { describe, it, expect } from 'vitest';
import { pickVelocity, DURATIONS, type Duration } from '../duration';
import type { Velocity, VelocityDay, VelocityWeek } from '../../types';

// Build distinguishable fixtures so daily and weekly arrays return DIFFERENT
// values for any given index. This catches a bug where pickVelocity maps a
// daily-granularity duration ('7d') to the weekly array. `closed` values
// are also distinct from `opened` to catch a swap regression.
function makeFixture(): Velocity {
  const daily: VelocityDay[] = Array.from({ length: 365 }, (_, i) => ({
    date: `2026-${String(Math.floor(i / 31) + 1).padStart(2, '0')}-${String((i % 28) + 1).padStart(2, '0')}`,
    opened: 1000 + i, // distinct values starting at 1000
    closed: 500 + i, // distinct from opened
  }));
  const weekly: VelocityWeek[] = Array.from({ length: 260 }, (_, i) => ({
    week: `2026-week-${i}`,
    opened: i, // distinct values starting at 0
    closed: 500 - i, // distinct from opened
  }));
  return { daily, weekly };
}

describe('pickVelocity', () => {
  it.each<[Duration, 'day' | 'week', number]>([
    ['7d', 'day', 7],
    ['4w', 'week', 4],
    ['12w', 'week', 12],
    ['6m', 'week', 26],
    ['1y', 'week', 52],
    ['5y', 'week', 260],
  ])('duration=%s → granularity=%s, length=%d', (duration, wantGranularity, wantLength) => {
    const v = makeFixture();
    const got = pickVelocity(v, duration);

    expect(got.granularity).toBe(wantGranularity);
    expect(got.points).toHaveLength(wantLength);

    // Each duration's points must come from the matching array; spot-check
    // the first and last point to catch off-by-one slicing bugs. Also assert
    // on `closed` to catch an opened/closed swap regression.
    if (wantGranularity === 'day') {
      // 7d → daily.slice(-7); first point's `opened` is 1000 + (365-7)
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

  it('exposes DURATIONS in display order', () => {
    expect(DURATIONS).toEqual(['7d', '4w', '12w', '6m', '1y', '5y']);
  });
});
