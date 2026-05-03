import { describe, it, expect } from 'vitest';
import { formatDayTick, formatWeekTick } from '../chartStyles';

describe('formatDayTick', () => {
  it('formats a YYYY-MM-DD UTC string as "MMM dd"', () => {
    // 2026-04-23 UTC should render the same MMM dd regardless of viewer TZ
    // because the formatter pins timeZone: 'UTC'.
    expect(formatDayTick('2026-04-23')).toBe('Apr 23');
  });

  it('formats early-month dates with zero-padded day', () => {
    // Catches a bug where day: 'numeric' would render "Jan 1" instead of "Jan 01".
    expect(formatDayTick('2026-01-01')).toBe('Jan 01');
  });

  it('crosses month boundaries correctly under UTC', () => {
    // 2026-12-31 UTC must format as "Dec 31", not Jan 1 (which would happen
    // if the formatter let the local-tz boundary slide a day).
    expect(formatDayTick('2026-12-31')).toBe('Dec 31');
  });
});

describe('formatWeekTick', () => {
  it('formats a parseable date string as M/D', () => {
    // Sanity check against the existing helper to lock its contract while
    // formatDayTick lives next to it.
    expect(formatWeekTick('2026-04-23')).toMatch(/^4\/2[23]$/);
  });

  it('returns the input verbatim when the date is unparseable', () => {
    expect(formatWeekTick('not-a-date')).toBe('not-a-date');
  });
});
