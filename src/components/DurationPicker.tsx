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

export default function DurationPicker({ value, onChange }: Props) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function handleClick(e: MouseEvent) {
      if (!rootRef.current?.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  function selectPreset(p: PresetDuration) {
    onChange({ kind: 'preset', value: p });
    setOpen(false);
  }

  return (
    <div ref={rootRef} className="relative inline-block">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Range: ${formatDurationLabel(value)}`}
        className="flex items-center gap-1 px-3 py-1 text-xs rounded bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 hover:bg-gray-200 dark:hover:bg-gray-600"
      >
        <span>Range: {formatDurationLabel(value)}</span>
        <ChevronDown size={12} />
      </button>

      {open && (
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
            disabled
            className="block w-full text-left px-3 py-1.5 text-gray-400 dark:text-gray-500 cursor-not-allowed"
          >
            Custom range…
          </button>
        </div>
      )}
    </div>
  );
}
