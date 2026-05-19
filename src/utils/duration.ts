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

/**
 * pickVelocity selects the right velocity array (daily or weekly) for the
 * requested duration and slices it to the appropriate length. The labels
 * come from the underlying entry's `date` (daily) or `week` (weekly).
 *
 * Snapshot stats (Open Issues, age buckets, categories) deliberately do
 * NOT pass through this helper — they always reflect "right now".
 */
export function pickVelocity(v: Velocity, d: Duration): PickedVelocity {
  if (d.kind === 'preset') {
    return pickPreset(v, d.value);
  }
  // Custom case implemented in Task 3.
  return { points: [], granularity: 'week' };
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
