import { useEffect, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import {
  PRESET_DURATIONS,
  formatDurationLabel,
  type Duration,
  type PresetDuration,
} from '../utils/duration';

interface Props {
  value: Duration;
  onChange: (next: Duration) => void;
}

type Mode = 'closed' | 'presets' | 'custom';

export default function DurationPicker({ value, onChange }: Props) {
  const [mode, setMode] = useState<Mode>('closed');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  function closeAndRefocus() {
    setMode('closed');
    triggerRef.current?.focus();
  }

  useEffect(() => {
    if (mode === 'closed') return;
    function handleClick(e: MouseEvent) {
      if (!rootRef.current?.contains(e.target as Node)) {
        setMode('closed');
      }
    }
    function handleKeydown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        closeAndRefocus();
      }
    }
    document.addEventListener('mousedown', handleClick);
    document.addEventListener('keydown', handleKeydown);
    return () => {
      document.removeEventListener('mousedown', handleClick);
      document.removeEventListener('keydown', handleKeydown);
    };
  }, [mode]);

  function openPresets() {
    setMode((m) => (m === 'closed' ? 'presets' : 'closed'));
  }

  function selectPreset(p: PresetDuration) {
    onChange({ kind: 'preset', value: p });
    closeAndRefocus();
  }

  function enterCustom() {
    if (value.kind === 'custom') {
      setFrom(value.from);
      setTo(value.to);
    } else {
      setFrom('');
      setTo('');
    }
    setMode('custom');
  }

  function applyCustom() {
    onChange({ kind: 'custom', from, to });
    closeAndRefocus();
  }

  const invalidOrder = from !== '' && to !== '' && from > to;
  const canApply = from !== '' && to !== '' && !invalidOrder;

  return (
    <div ref={rootRef} className="relative inline-block">
      <button
        ref={triggerRef}
        type="button"
        onClick={openPresets}
        aria-haspopup={mode === 'custom' ? 'dialog' : 'menu'}
        aria-expanded={mode !== 'closed'}
        className="flex items-center gap-1 px-3 py-1 text-xs rounded bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 hover:bg-gray-200 dark:hover:bg-gray-600"
      >
        <span>Range: {formatDurationLabel(value)}</span>
        <ChevronDown size={12} />
      </button>

      {mode === 'presets' && (
        <div
          role="menu"
          className="absolute right-0 mt-1 z-10 min-w-[180px] bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded shadow-lg text-xs"
        >
          {PRESET_DURATIONS.map((p) => {
            const isActive = value.kind === 'preset' && value.value === p;
            const label = formatDurationLabel({ kind: 'preset', value: p });
            return (
              <button
                key={p}
                role="menuitem"
                type="button"
                onClick={() => selectPreset(p)}
                className={`block w-full text-left px-3 py-1.5 hover:bg-gray-100 dark:hover:bg-gray-700 ${
                  isActive ? 'text-nvidia-green font-medium' : 'text-gray-700 dark:text-gray-200'
                }`}
              >
                {label}
              </button>
            );
          })}
          <div className="border-t border-gray-200 dark:border-gray-700 my-1" />
          <button
            role="menuitem"
            type="button"
            onClick={enterCustom}
            className="block w-full text-left px-3 py-1.5 hover:bg-gray-100 dark:hover:bg-gray-700 text-gray-700 dark:text-gray-200"
          >
            Custom range…
          </button>
        </div>
      )}

      {mode === 'custom' && (
        <form
          role="dialog"
          aria-label="Custom date range"
          aria-modal="false"
          onSubmit={(e) => {
            e.preventDefault();
            if (canApply) applyCustom();
          }}
          className="absolute right-0 mt-1 z-10 min-w-[240px] bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded shadow-lg text-xs p-3"
        >
          <label className="block mb-2">
            <span className="block text-gray-600 dark:text-gray-400 mb-1">From</span>
            <input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="w-full px-2 py-1 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
            />
          </label>
          <label className="block mb-2">
            <span className="block text-gray-600 dark:text-gray-400 mb-1">To</span>
            <input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="w-full px-2 py-1 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100"
            />
          </label>
          {invalidOrder && (
            <p role="alert" className="text-red-600 dark:text-red-400 mb-2">From must be on or before To.</p>
          )}
          <div className="flex justify-end gap-2 mt-2">
            <button
              type="button"
              onClick={() => setMode('presets')}
              className="px-2 py-1 rounded bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 hover:bg-gray-200 dark:hover:bg-gray-600"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canApply}
              className="px-2 py-1 rounded bg-nvidia-green text-white disabled:opacity-50 disabled:cursor-not-allowed"
            >
              Apply
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
