import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
} from 'recharts';
import { useTheme } from './ThemeProvider';

interface TrendChartProps {
  data: { date: string; [key: string]: string | number }[];
  areas: { key: string; color: string; name: string }[];
  height?: number;
  stacked?: boolean;
}

export default function TrendChart({
  data,
  areas,
  height = 200,
  stacked = false,
}: TrendChartProps) {
  const { resolved } = useTheme();
  const dark = resolved === 'dark';

  if (data.length === 0) {
    return (
      <p className="text-sm text-gray-500 dark:text-gray-400 py-4">
        No trend data available yet. Data accumulates over time.
      </p>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: 0 }}>
        <CartesianGrid
          strokeDasharray="3 3"
          stroke={dark ? '#374151' : '#e5e7eb'}
        />
        <XAxis
          dataKey="date"
          tick={{ fontSize: 11, fill: dark ? '#9ca3af' : '#6b7280' }}
          tickFormatter={(v: string) => {
            const d = new Date(v);
            return `${d.getMonth() + 1}/${d.getDate()}`;
          }}
        />
        <YAxis
          tick={{ fontSize: 11, fill: dark ? '#9ca3af' : '#6b7280' }}
          allowDecimals={false}
        />
        <Tooltip
          contentStyle={{
            backgroundColor: dark ? '#1f2937' : '#fff',
            borderColor: dark ? '#374151' : '#e5e7eb',
            color: dark ? '#f3f4f6' : '#111827',
            fontSize: 12,
          }}
        />
        {areas.map((a) => (
          <Area
            key={a.key}
            type="monotone"
            dataKey={a.key}
            name={a.name}
            stroke={a.color}
            fill={a.color}
            fillOpacity={0.15}
            stackId={stacked ? 'stack' : undefined}
          />
        ))}
      </AreaChart>
    </ResponsiveContainer>
  );
}
