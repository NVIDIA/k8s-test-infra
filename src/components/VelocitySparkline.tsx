import { useMemo } from 'react';
import { ResponsiveContainer, LineChart, Line } from 'recharts';
import type { Velocity } from '../types';
import { pickVelocity, type Duration } from '../utils/duration';

interface Props {
  velocity: Velocity;
  duration: Duration;
}

export default function VelocitySparkline({ velocity, duration }: Props) {
  const { points } = useMemo(() => pickVelocity(velocity, duration), [velocity, duration]);

  if (points.length === 0) {
    return <span className="text-gray-400 text-xs" data-testid="sparkline-empty">&mdash;</span>;
  }

  return (
    <div className="inline-block w-[60px] h-[20px]" data-testid="sparkline">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={points}>
          <Line type="monotone" dataKey="opened" stroke="#ef4444" strokeWidth={1} dot={false} isAnimationActive={false} />
          <Line type="monotone" dataKey="closed" stroke="#22c55e" strokeWidth={1} dot={false} isAnimationActive={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
