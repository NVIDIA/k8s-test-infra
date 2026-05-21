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

  it('Escape returns focus to the trigger', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    const trigger = screen.getByRole('button', { name: /range/i });
    await user.click(trigger);
    expect(screen.getByRole('menuitem', { name: 'Last 7 days' })).toBeInTheDocument();

    await user.keyboard('{Escape}');

    expect(trigger).toHaveFocus();
  });

  it('selecting a preset returns focus to the trigger', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    const trigger = screen.getByRole('button', { name: /range/i });
    await user.click(trigger);
    await user.click(screen.getByRole('menuitem', { name: 'Last 6 months' }));

    expect(trigger).toHaveFocus();
  });
});

describe('DurationPicker (custom range)', () => {
  it('clicking Custom range… swaps the popover to the date form', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));

    expect(screen.getByLabelText('From')).toBeInTheDocument();
    expect(screen.getByLabelText('To')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Apply' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    // Preset items are no longer visible
    expect(screen.queryByRole('menuitem', { name: 'Last 7 days' })).not.toBeInTheDocument();
  });

  it('Apply with valid range fires onChange and closes', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DurationPicker value={preset('12w')} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    await user.type(screen.getByLabelText('From'), '2025-10-06');
    await user.type(screen.getByLabelText('To'), '2025-10-12');
    await user.click(screen.getByRole('button', { name: 'Apply' }));

    expect(onChange).toHaveBeenCalledWith({
      kind: 'custom',
      from: '2025-10-06',
      to: '2025-10-12',
    });
    expect(screen.queryByLabelText('From')).not.toBeInTheDocument();
  });

  it('Apply is disabled when from > to', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DurationPicker value={preset('12w')} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    await user.type(screen.getByLabelText('From'), '2025-10-12');
    await user.type(screen.getByLabelText('To'), '2025-10-06');

    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled();
    expect(screen.getByText(/From must be on or before To/i)).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('Apply is disabled until both dates are filled', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled();

    await user.type(screen.getByLabelText('From'), '2025-10-06');
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled();
    await user.type(screen.getByLabelText('To'), '2025-10-12');
    expect(screen.getByRole('button', { name: 'Apply' })).toBeEnabled();
  });

  it('Cancel returns to the preset list without firing onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DurationPicker value={preset('12w')} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('menuitem', { name: 'Last 7 days' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /Custom range/i })).toBeInTheDocument();
  });

  it('Enter inside an input submits when range is valid', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DurationPicker value={preset('12w')} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    await user.type(screen.getByLabelText('From'), '2025-10-06');
    await user.type(screen.getByLabelText('To'), '2025-10-12');
    // Enter inside the focused To input
    await user.keyboard('{Enter}');

    expect(onChange).toHaveBeenCalledWith({
      kind: 'custom',
      from: '2025-10-06',
      to: '2025-10-12',
    });
    expect(screen.queryByLabelText('From')).not.toBeInTheDocument();
  });

  it('pre-fills inputs from the current custom value', async () => {
    const user = userEvent.setup();
    const value: Duration = { kind: 'custom', from: '2025-10-06', to: '2025-10-12' };
    render(<DurationPicker value={value} onChange={() => {}} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));

    expect(screen.getByLabelText('From')).toHaveValue('2025-10-06');
    expect(screen.getByLabelText('To')).toHaveValue('2025-10-12');
  });

  it('Apply returns focus to the trigger', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    const trigger = screen.getByRole('button', { name: /range/i });
    await user.click(trigger);
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));
    await user.type(screen.getByLabelText('From'), '2025-10-06');
    await user.type(screen.getByLabelText('To'), '2025-10-12');
    await user.click(screen.getByRole('button', { name: 'Apply' }));

    expect(trigger).toHaveFocus();
  });

  it('custom form has role=dialog with an accessible label', async () => {
    const user = userEvent.setup();
    render(<DurationPicker value={preset('12w')} onChange={() => {}} />);

    await user.click(screen.getByRole('button', { name: /range/i }));
    await user.click(screen.getByRole('menuitem', { name: /Custom range/i }));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAccessibleName('Custom date range');
  });
});
