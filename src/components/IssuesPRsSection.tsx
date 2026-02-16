import { useMemo, useState } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  LineChart,
  Line,
  Legend,
  Cell,
} from 'recharts';
import { AlertCircle, GitPullRequest, Clock, Eye } from 'lucide-react';
import { useTheme } from './ThemeProvider';
import type { RepoIssuesPRs } from '../types';

const AGE_COLORS: Record<string, string> = {
  fresh: '#22c55e',
  recent: '#84cc16',
  aging: '#eab308',
  stale: '#f97316',
  ancient: '#ef4444',
};

const CATEGORY_COLORS: Record<string, string> = {
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

function getCategoryColor(category: string): string {
  return CATEGORY_COLORS[category] ?? '#6b7280';
}

interface Props {
  data: RepoIssuesPRs;
}

export default function IssuesPRsSection({ data }: Props) {
  const { resolved } = useTheme();
  const dark = resolved === 'dark';
  const [velocityView, setVelocityView] = useState<'issues' | 'prs'>('issues');

  const issueCategories = useMemo(() => {
    return Object.entries(data.issues.categories)
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count);
  }, [data.issues.categories]);

  const prCategories = useMemo(() => {
    return Object.entries(data.pullRequests.categories)
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count);
  }, [data.pullRequests.categories]);

  const ageData = useMemo(() => {
    const buckets = ['fresh', 'recent', 'aging', 'stale', 'ancient'] as const;
    return buckets.map((b) => ({
      bucket: b,
      issues: data.issues.ageBuckets[b],
      prs: data.pullRequests.ageBuckets[b],
    }));
  }, [data.issues.ageBuckets, data.pullRequests.ageBuckets]);

  const velocityData = useMemo(() => {
    const source = velocityView === 'issues' ? data.issues.velocity : data.pullRequests.velocity;
    return source.map((v) => ({
      week: v.week,
      opened: v.opened,
      closed: v.closed,
      ...(v.merged !== undefined ? { merged: v.merged } : {}),
    }));
  }, [velocityView, data.issues.velocity, data.pullRequests.velocity]);

  const tooltipStyle = {
    backgroundColor: dark ? '#1f2937' : '#fff',
    borderColor: dark ? '#374151' : '#e5e7eb',
    color: dark ? '#f3f4f6' : '#111827',
    fontSize: 12,
  };

  const tickStyle = { fontSize: 11, fill: dark ? '#9ca3af' : '#6b7280' };
  const gridStroke = dark ? '#374151' : '#e5e7eb';

  return (
    <section id="issues-prs" className="mb-6">
      <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200 mb-3">Issues &amp; Pull Requests</h2>

      {/* Summary Stats Bar */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-4">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 flex items-center gap-3">
          <AlertCircle size={24} className="text-orange-500" />
          <div>
            <p className="text-xl font-bold text-gray-900 dark:text-white">{data.issues.total}</p>
            <p className="text-xs text-gray-500 dark:text-gray-400">Open Issues</p>
          </div>
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 flex items-center gap-3">
          <GitPullRequest size={24} className="text-blue-500" />
          <div>
            <p className="text-xl font-bold text-gray-900 dark:text-white">{data.pullRequests.total}</p>
            <p className="text-xs text-gray-500 dark:text-gray-400">Open PRs</p>
          </div>
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 flex items-center gap-3">
          <Clock size={24} className="text-purple-500" />
          <div>
            <p className="text-xl font-bold text-gray-900 dark:text-white">
              {data.pullRequests.review.avgDaysToMerge.toFixed(1)}d
            </p>
            <p className="text-xs text-gray-500 dark:text-gray-400">Avg Time to Merge</p>
          </div>
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 flex items-center gap-3">
          <Eye size={24} className="text-yellow-500" />
          <div>
            <p className="text-xl font-bold text-gray-900 dark:text-white">{data.pullRequests.review.awaitingReview}</p>
            <p className="text-xs text-gray-500 dark:text-gray-400">Awaiting Review</p>
          </div>
        </div>
      </div>

      {/* Category Breakdown */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Issues by Category</h3>
          {issueCategories.length > 0 ? (
            <ResponsiveContainer width="100%" height={Math.max(issueCategories.length * 32, 100)}>
              <BarChart data={issueCategories} layout="vertical" margin={{ top: 0, right: 20, bottom: 0, left: 80 }}>
                <CartesianGrid strokeDasharray="3 3" stroke={gridStroke} horizontal={false} />
                <XAxis type="number" tick={tickStyle} allowDecimals={false} />
                <YAxis type="category" dataKey="name" tick={tickStyle} width={75} />
                <Tooltip contentStyle={tooltipStyle} />
                <Bar dataKey="count" name="Count" radius={[0, 4, 4, 0]}>
                  {issueCategories.map((entry) => (
                    <Cell key={entry.name} fill={getCategoryColor(entry.name)} />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-sm text-gray-400 py-4">No issue data available.</p>
          )}
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">PRs by Category</h3>
          {prCategories.length > 0 ? (
            <ResponsiveContainer width="100%" height={Math.max(prCategories.length * 32, 100)}>
              <BarChart data={prCategories} layout="vertical" margin={{ top: 0, right: 20, bottom: 0, left: 80 }}>
                <CartesianGrid strokeDasharray="3 3" stroke={gridStroke} horizontal={false} />
                <XAxis type="number" tick={tickStyle} allowDecimals={false} />
                <YAxis type="category" dataKey="name" tick={tickStyle} width={75} />
                <Tooltip contentStyle={tooltipStyle} />
                <Bar dataKey="count" name="Count" radius={[0, 4, 4, 0]}>
                  {prCategories.map((entry) => (
                    <Cell key={entry.name} fill={getCategoryColor(entry.name)} />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-sm text-gray-400 py-4">No PR data available.</p>
          )}
        </div>
      </div>

      {/* Age Distribution */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 mb-4">
        <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Age Distribution</h3>
        <ResponsiveContainer width="100%" height={160}>
          <BarChart data={ageData} margin={{ top: 4, right: 20, bottom: 0, left: 0 }}>
            <CartesianGrid strokeDasharray="3 3" stroke={gridStroke} />
            <XAxis dataKey="bucket" tick={tickStyle} />
            <YAxis tick={tickStyle} allowDecimals={false} />
            <Tooltip contentStyle={tooltipStyle} />
            <Legend />
            <Bar dataKey="issues" name="Issues" radius={[4, 4, 0, 0]}>
              {ageData.map((entry) => (
                <Cell key={`issue-${entry.bucket}`} fill={AGE_COLORS[entry.bucket]} />
              ))}
            </Bar>
            <Bar dataKey="prs" name="PRs" radius={[4, 4, 0, 0]}>
              {ageData.map((entry) => (
                <Cell key={`pr-${entry.bucket}`} fill={AGE_COLORS[entry.bucket]} opacity={0.6} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>

      {/* Velocity Chart */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
        <div className="flex items-center justify-between mb-2">
          <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">Velocity (12 weeks)</h3>
          <div className="flex gap-1">
            <button
              onClick={() => setVelocityView('issues')}
              aria-pressed={velocityView === 'issues'}
              aria-label="Show issues velocity"
              className={`px-2 py-1 text-xs rounded ${
                velocityView === 'issues'
                  ? 'bg-nvidia-green text-white'
                  : 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-400'
              }`}
            >
              Issues
            </button>
            <button
              onClick={() => setVelocityView('prs')}
              aria-pressed={velocityView === 'prs'}
              aria-label="Show pull requests velocity"
              className={`px-2 py-1 text-xs rounded ${
                velocityView === 'prs'
                  ? 'bg-nvidia-green text-white'
                  : 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-400'
              }`}
            >
              PRs
            </button>
          </div>
        </div>
        {velocityData.length > 0 ? (
          <ResponsiveContainer width="100%" height={180}>
            <LineChart data={velocityData} margin={{ top: 4, right: 20, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="3 3" stroke={gridStroke} />
              <XAxis
                dataKey="week"
                tick={tickStyle}
                tickFormatter={(v: string) => {
                  const d = new Date(v);
                  if (Number.isNaN(d.getTime())) return v;
                  return `${d.getMonth() + 1}/${d.getDate()}`;
                }}
              />
              <YAxis tick={tickStyle} allowDecimals={false} />
              <Tooltip contentStyle={tooltipStyle} />
              <Legend />
              <Line type="monotone" dataKey="opened" stroke="#ef4444" name="Opened" strokeWidth={2} dot={false} />
              <Line type="monotone" dataKey="closed" stroke="#22c55e" name="Closed" strokeWidth={2} dot={false} />
              {velocityView === 'prs' && (
                <Line type="monotone" dataKey="merged" stroke="#8b5cf6" name="Merged" strokeWidth={2} dot={false} />
              )}
            </LineChart>
          </ResponsiveContainer>
        ) : (
          <p className="text-sm text-gray-400 py-4">No velocity data available.</p>
        )}
      </div>
    </section>
  );
}
