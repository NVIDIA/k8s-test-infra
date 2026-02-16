import type { VelocityWeek } from '../types';

export const AGE_COLORS: Record<string, string> = {
  fresh: '#22c55e',
  recent: '#84cc16',
  aging: '#eab308',
  stale: '#f97316',
  ancient: '#ef4444',
};

export const CATEGORY_COLORS: Record<string, string> = {
  bug: '#ef4444',
  critical: '#f97316',
  'feature-request': '#3b82f6',
  feature: '#3b82f6',
  enhancement: '#22c55e',
  'bug-fix': '#ef4444',
  chore: '#8b5cf6',
  docs: '#06b6d4',
  question: '#a855f7',
  'good-first-issue': '#10b981',
  other: '#6b7280',
};

export function getCategoryColor(category: string): string {
  return CATEGORY_COLORS[category] ?? '#6b7280';
}

export function getChartStyles(dark: boolean) {
  return {
    tooltipStyle: {
      backgroundColor: dark ? '#1f2937' : '#fff',
      borderColor: dark ? '#374151' : '#e5e7eb',
      color: dark ? '#f3f4f6' : '#111827',
      fontSize: 12,
    },
    tickStyle: { fontSize: 11, fill: dark ? '#9ca3af' : '#6b7280' },
    gridStroke: dark ? '#374151' : '#e5e7eb',
  };
}

export function formatWeekTick(v: string): string {
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

export type Trend = 'growing' | 'shrinking' | 'stable';

export function computeTrend(velocity: VelocityWeek[]): Trend {
  if (velocity.length < 3) return 'stable';
  const last4 = velocity.slice(-4);
  const opened = last4.reduce((s, w) => s + w.opened, 0);
  const closed = last4.reduce((s, w) => s + w.closed, 0);
  if (opened > closed * 1.1) return 'growing';
  if (closed > opened * 1.1) return 'shrinking';
  return 'stable';
}
