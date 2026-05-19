import type { Velocity } from '../types';

export type PresetDuration = '7d' | '4w' | '12w' | '6m' | '1y' | '5y';

export type Duration =
  | { kind: 'preset'; value: PresetDuration }
  | { kind: 'custom'; from: string; to: string }; // ISO "YYYY-MM-DD"

export const PRESET_DURATIONS: PresetDuration[] = ['7d', '4w', '12w', '6m', '1y', '5y'];

export interface VelocityPoint {
  label: string;
  opened: number;
  closed: number;
  merged?: number;
}

export interface PickedVelocity {
  points: VelocityPoint[];
  granularity: 'day' | 'week';
  /** When the requested range was clamped to fit available data. */
  clamp?: { requestedFrom: string; actualFrom: string };
}

const PRESET_TABLE: Record<PresetDuration, { granularity: 'day' | 'week'; n: number }> = {
  '7d':  { granularity: 'day',  n: 7   },
  '4w':  { granularity: 'week', n: 4   },
  '12w': { granularity: 'week', n: 12  },
  '6m':  { granularity: 'week', n: 26  },
  '1y':  { granularity: 'week', n: 52  },
  '5y':  { granularity: 'week', n: 260 },
};

const ONE_DAY_MS = 24 * 60 * 60 * 1000;
const DAILY_RETENTION_DAYS = 365;
const DAILY_MAX_RANGE_DAYS = 90;

/**
 * pickVelocity selects the right velocity array (daily or weekly) for the
 * requested duration and slices it. For custom ranges, granularity is
 * chosen by the rule:
 *   daily if range_days ≤ 90 AND from ≥ today − 365d, else weekly.
 * Out-of-retention starts are clamped to the earliest available label.
 *
 * The optional `now` parameter exists so tests can pin "today" for the
 * 365-day retention check. Production callers omit it (defaults to
 * `new Date()`).
 */
export function pickVelocity(v: Velocity, d: Duration, now?: Date): PickedVelocity {
  if (d.kind === 'preset') {
    return pickPreset(v, d.value);
  }
  return pickCustom(v, d.from, d.to, now ?? new Date());
}

function pickCustom(v: Velocity, from: string, to: string, now: Date): PickedVelocity {
  if (from > to) {
    return { points: [], granularity: 'week' };
  }

  const rangeDays = daysBetweenInclusive(from, to);
  const fromDate = new Date(`${from}T00:00:00Z`);
  const ageDays = (now.getTime() - fromDate.getTime()) / ONE_DAY_MS;
  const wantDaily = rangeDays <= DAILY_MAX_RANGE_DAYS && ageDays <= DAILY_RETENTION_DAYS;

  if (wantDaily) {
    const points = v.daily
      .filter((entry) => entry.date >= from && entry.date <= to)
      .map((day) => ({
        label: day.date,
        opened: day.opened,
        closed: day.closed,
        merged: day.merged,
      }));
    return { points, granularity: 'day' };
  }

  const weekly = v.weekly;
  if (weekly.length === 0) {
    return { points: [], granularity: 'week' };
  }
  const earliest = weekly[0].week;
  const filtered = weekly.filter((entry) => entry.week >= from && entry.week <= to);
  const points = filtered.map((wk) => ({
    label: wk.week,
    opened: wk.opened,
    closed: wk.closed,
    merged: wk.merged,
  }));
  if (from < earliest && points.length > 0) {
    return {
      points,
      granularity: 'week',
      clamp: { requestedFrom: from, actualFrom: points[0].label },
    };
  }
  return { points, granularity: 'week' };
}

function daysBetweenInclusive(fromISO: string, toISO: string): number {
  const from = new Date(`${fromISO}T00:00:00Z`).getTime();
  const to = new Date(`${toISO}T00:00:00Z`).getTime();
  return Math.floor((to - from) / ONE_DAY_MS) + 1;
}

// en-US short month names. Update if i18n support is added.
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

type ParsedDate = { y: number; m: number; day: number };

function parseISO(d: string): ParsedDate {
  const [y, m, day] = d.split('-').map(Number);
  return { y, m, day };
}

function shortDate(p: ParsedDate, includeYear: boolean): string {
  const base = `${MONTHS[p.m - 1]} ${p.day}`;
  return includeYear ? `${base}, ${p.y}` : base;
}

const PRESET_LABELS: Record<PresetDuration, string> = {
  '7d':  'Last 7 days',
  '4w':  'Last 4 weeks',
  '12w': 'Last 12 weeks',
  '6m':  'Last 6 months',
  '1y':  'Last 1 year',
  '5y':  'Last 5 years',
};

export function formatDurationLabel(d: Duration): string {
  if (d.kind === 'preset') {
    return PRESET_LABELS[d.value];
  }
  const from = parseISO(d.from);
  if (d.from === d.to) {
    return shortDate(from, true);
  }
  const to = parseISO(d.to);
  if (from.y === to.y) {
    return `${shortDate(from, false)} – ${shortDate(to, true)}`;
  }
  return `${shortDate(from, true)} – ${shortDate(to, true)}`;
}

function pickPreset(v: Velocity, preset: PresetDuration): PickedVelocity {
  const cfg = PRESET_TABLE[preset];
  if (cfg.granularity === 'day') {
    const slice = v.daily.slice(-cfg.n);
    return {
      granularity: 'day',
      points: slice.map((day) => ({
        label: day.date,
        opened: day.opened,
        closed: day.closed,
        merged: day.merged,
      })),
    };
  }
  const slice = v.weekly.slice(-cfg.n);
  return {
    granularity: 'week',
    points: slice.map((wk) => ({
      label: wk.week,
      opened: wk.opened,
      closed: wk.closed,
      merged: wk.merged,
    })),
  };
}
