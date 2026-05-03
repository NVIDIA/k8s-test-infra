import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import VelocitySparkline from '../VelocitySparkline';
import type { Velocity } from '../../types';

function makeFixture(): Velocity {
  const daily = Array.from({ length: 365 }, (_, i) => ({
    date: `2026-${String((i % 12) + 1).padStart(2, '0')}-01`,
    opened: 1000 + i,
    closed: 0,
  }));
  const weekly = Array.from({ length: 260 }, (_, i) => ({
    week: `wk-${i}`,
    opened: i,
    closed: 0,
  }));
  return { daily, weekly };
}

describe('VelocitySparkline', () => {
  it('renders for 7d using daily data', () => {
    const v = makeFixture();
    render(<VelocitySparkline velocity={v} duration="7d" />);
    expect(screen.getByTestId('sparkline')).toBeInTheDocument();
  });

  it('renders for 5y using weekly data', () => {
    const v = makeFixture();
    render(<VelocitySparkline velocity={v} duration="5y" />);
    expect(screen.getByTestId('sparkline')).toBeInTheDocument();
  });

  it('renders the empty state when both arrays are empty', () => {
    render(<VelocitySparkline velocity={{ daily: [], weekly: [] }} duration="7d" />);
    expect(screen.getByTestId('sparkline-empty')).toBeInTheDocument();
  });
});
