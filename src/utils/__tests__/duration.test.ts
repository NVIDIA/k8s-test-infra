import { describe, it, expect } from 'vitest';
import { pickVelocity, PRESET_DURATIONS, formatDurationLabel, type Duration, type PresetDuration } from '../duration';
import type { Velocity, VelocityDay, VelocityWeek } from '../../types';

// Build distinguishable fixtures so daily and weekly arrays return DIFFERENT
// values for any given index. This catches a bug where pickVelocity maps a
// daily-granularity duration ('7d') to the weekly array. `closed` values
// are also distinct from `opened` to catch a swap regression.
//
// Daily: sequential ISO dates 2026-01-01 through 2026-12-31 (365 unique days).
// Weekly: sequential ISO Monday dates 2021-12-27 through 2026-12-21 (260 weeks).
// Both match production data shape (artifact_fetcher.go uses Format("2006-01-02")).
function makeFixture(): Velocity {
  const dailyStart = Date.UTC(2026, 0, 1); // 2026-01-01
  const daily: VelocityDay[] = Array.from({ length: 365 }, (_, i) => {
    const date = new Date(dailyStart + i * 24 * 60 * 60 * 1000)
      .toISOString()
      .slice(0, 10);
    return { date, opened: 1000 + i, closed: 500 + i };
  });
  const weeklyStart = Date.UTC(2021, 11, 27); // 2021-12-27 (Monday)
  const weekly: VelocityWeek[] = Array.from({ length: 260 }, (_, i) => {
    const week = new Date(weeklyStart + i * 7 * 24 * 60 * 60 * 1000)
      .toISOString()
      .slice(0, 10);
    return { week, opened: i, closed: 500 - i };
  });
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

describe('formatDurationLabel', () => {
  it.each<[PresetDuration, string]>([
    ['7d', 'Last 7 days'],
    ['4w', 'Last 4 weeks'],
    ['12w', 'Last 12 weeks'],
    ['6m', 'Last 6 months'],
    ['1y', 'Last 1 year'],
    ['5y', 'Last 5 years'],
  ])('preset %s → %s', (value, want) => {
    expect(formatDurationLabel({ kind: 'preset', value })).toBe(want);
  });

  it('custom range same year → "MMM d – MMM d, YYYY"', () => {
    expect(formatDurationLabel({ kind: 'custom', from: '2025-10-06', to: '2025-10-12' }))
      .toBe('Oct 6 – Oct 12, 2025');
  });

  it('custom range crossing years → "MMM d, YYYY – MMM d, YYYY"', () => {
    expect(formatDurationLabel({ kind: 'custom', from: '2024-12-29', to: '2025-01-04' }))
      .toBe('Dec 29, 2024 – Jan 4, 2025');
  });

  it('custom single-day range → "MMM d, YYYY"', () => {
    expect(formatDurationLabel({ kind: 'custom', from: '2025-10-06', to: '2025-10-06' }))
      .toBe('Oct 6, 2025');
  });
});

describe('pickVelocity (custom)', () => {
  // Daily fixture: 2026-01-01..2026-12-31 (365 unique sequential ISO dates).
  // Weekly fixture: 2021-12-27..2026-12-21 (260 sequential ISO Monday dates).
  // Both match production data shape (artifact_fetcher.go uses Format("2006-01-02")).
  // Use a fixed `now` to make 365-day daily-retention assertions deterministic.
  function custom(from: string, to: string): Duration {
    return { kind: 'custom', from, to };
  }

  it('range ≤ 90 days inside daily window → daily granularity', () => {
    const v = makeFixture();
    const now = new Date('2026-12-04T00:00:00Z');
    const got = pickVelocity(v, custom('2026-01-01', '2026-01-07'), now);

    expect(got.granularity).toBe('day');
    expect(got.points).toHaveLength(7);
    expect(got.points[0].label).toBe('2026-01-01');
    expect(got.points[6].label).toBe('2026-01-07');
    // Daily fixture starts at index 0 = 2026-01-01 with opened=1000+0, closed=500+0.
    expect(got.points[0].opened).toBe(1000);
    expect(got.points[0].closed).toBe(500);
    expect(got.points[6].opened).toBe(1006);
    expect(got.points[6].closed).toBe(506);
    expect(got.clamp).toBeUndefined();
  });

  it('range > 90 days inside daily window → weekly granularity', () => {
    const v = makeFixture();
    const now = new Date('2026-12-04T00:00:00Z');
    // Range 2026-01-01..2026-07-20 = 200+ days → falls through to weekly.
    // Exactly 29 Mondays in this range (verified via fixture: 2021-12-27 + 7n
    // for n = 213..241 land in [2026-01-01, 2026-07-20]).
    const got = pickVelocity(v, custom('2026-01-01', '2026-07-20'), now);

    expect(got.granularity).toBe('week');
    expect(got.points).toHaveLength(29);
    for (const p of got.points) {
      expect(p.label >= '2026-01-01').toBe(true);
      expect(p.label <= '2026-07-20').toBe(true);
    }
    expect(got.clamp).toBeUndefined();
  });

  it('short range starting earlier than 365d ago → weekly (age gate)', () => {
    const v = makeFixture();
    // Range = 7 days (≤ 90), but `from` is ~517 days before `now` → age gate
    // forces weekly. Deleting the `ageDays <= 365` clause from pickCustom
    // would let daily through, breaking this test.
    // 2024-01-01 is a Monday and exists in the weekly fixture.
    const got = pickVelocity(
      v,
      custom('2024-01-01', '2024-01-07'),
      new Date('2025-06-01T00:00:00Z'),
    );

    expect(got.granularity).toBe('week');
    expect(got.points).toHaveLength(1);
    expect(got.points[0].label).toBe('2024-01-01');
    expect(got.clamp).toBeUndefined();
  });

  it('range partially before weekly retention → clamps', () => {
    const v = makeFixture();
    // Earliest weekly Monday is 2021-12-27. Request a range that starts before.
    const got = pickVelocity(
      v,
      custom('2020-06-01', '2022-03-01'),
      new Date('2027-01-01T00:00:00Z'),
    );

    expect(got.granularity).toBe('week');
    expect(got.points.length).toBeGreaterThan(0);
    expect(got.points[0].label).toBe('2021-12-27');
    expect(got.clamp).toEqual({ requestedFrom: '2020-06-01', actualFrom: '2021-12-27' });
  });

  it('range entirely before retention → empty', () => {
    const v = makeFixture();
    const got = pickVelocity(
      v,
      custom('2019-01-01', '2019-12-31'),
      new Date('2027-01-01T00:00:00Z'),
    );

    expect(got.points).toEqual([]);
    expect(got.granularity).toBe('week');
  });

  it('single-day range → one daily point', () => {
    const v = makeFixture();
    const now = new Date('2026-12-04T00:00:00Z');
    const got = pickVelocity(v, custom('2026-01-15', '2026-01-15'), now);

    expect(got.granularity).toBe('day');
    expect(got.points).toHaveLength(1);
    expect(got.points[0].label).toBe('2026-01-15');
    // 2026-01-15 is at index 14 in the daily fixture (0=2026-01-01).
    expect(got.points[0].opened).toBe(1014);
    expect(got.points[0].closed).toBe(514);
  });

  it('from > to → empty (defensive)', () => {
    const v = makeFixture();
    const now = new Date('2026-12-04T00:00:00Z');
    const got = pickVelocity(v, custom('2026-02-01', '2026-01-01'), now);

    expect(got.points).toEqual([]);
  });

  it('rejects malformed from (empty string)', () => {
    const v = makeFixture();
    const got = pickVelocity(v, custom('', '2026-01-07'), new Date('2026-12-04T00:00:00Z'));

    expect(got.points).toEqual([]);
    expect(got.granularity).toBe('week');
    expect(got.clamp).toBeUndefined();
  });

  it('rejects malformed to (not ISO)', () => {
    const v = makeFixture();
    const got = pickVelocity(v, custom('2026-01-01', 'not-a-date'), new Date('2026-12-04T00:00:00Z'));

    expect(got.points).toEqual([]);
    expect(got.granularity).toBe('week');
  });
});
