import { ResponsiveContainer, LineChart, Line } from 'recharts';
import type { VelocityWeek } from '../types';

interface Props {
  data: VelocityWeek[];
  weeks?: number;
}

export default function VelocitySparkline({ data, weeks = 12 }: Props) {
  const sliced = data.slice(-weeks);
  if (sliced.length === 0) return <span className="text-gray-400 text-xs">&mdash;</span>;

  return (
    <div className="inline-block w-[60px] h-[20px]">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={sliced}>
          <Line type="monotone" dataKey="opened" stroke="#ef4444" strokeWidth={1} dot={false} isAnimationActive={false} />
          <Line type="monotone" dataKey="closed" stroke="#22c55e" strokeWidth={1} dot={false} isAnimationActive={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
