import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import DurationPicker from '../DurationPicker';
import type { Duration } from '../../utils/duration';

function preset(value: '7d' | '4w' | '12w' | '6m' | '1y' | '5y'): Duration {
  return { kind: 'preset', value };
}

describe('DurationPicker (presets)', () => {
  it('renders the current preset label on the trigger', () => {
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);
    expect(screen.getByRole('button', { name: /range/i })).toHaveTextContent('Last 12 weeks');
  });

  it('opens the popover on trigger click and shows all six presets + Custom range…', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    await user.click(screen.getByRole('button', { name: /range/i }));

    expect(screen.getByRole('menuitem', { name: 'Last 7 days' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Last 4 weeks' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Last 12 weeks' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Last 6 months' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Last 1 year' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Last 5 years' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /Custom range/i })).toBeInTheDocument();
  });

  it('selecting a preset calls onChange and closes the popover', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DurationPicker value={preset('12w')} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: 'Last 6 months' }));

    expect(onChange).toHaveBeenCalledWith({ kind: 'preset', value: '6m' });
    expect(screen.queryByRole('menuitem', { name: 'Last 6 months' })).not.toBeInTheDocument();
  });

  it('clicking outside the popover closes it without firing onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <div>
        <DurationPicker value={preset('12w')} onChange={onChange} />
        <button>Outside</button>
      </div>,
    );

    await user.click(screen.getByRole('button', { name: /range/i }));
    expect(screen.getByRole('menuitem', { name: 'Last 7 days' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Outside' }));

    expect(screen.queryByRole('menuitem', { name: 'Last 7 days' })).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('Escape key closes the popover without firing onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DurationPicker value={preset('12w')} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    expect(screen.getByRole('menuitem', { name: 'Last 7 days' })).toBeInTheDocument();

    await user.keyboard('{Escape}');

    expect(screen.queryByRole('menuitem', { name: 'Last 7 days' })).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });
});
