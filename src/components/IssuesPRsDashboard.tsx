import { Fragment, useMemo, useState } from 'react';
import { Link } from 'react-router';
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
import { ChevronDown, ChevronRight, ArrowUp, ArrowDown, ArrowRight } from 'lucide-react';
import { useTheme } from './ThemeProvider';
import VelocitySparkline from './VelocitySparkline';
import { AGE_COLORS, getCategoryColor, getChartStyles, formatWeekTick, computeTrend } from '../utils/chartStyles';
import type { Trend } from '../utils/chartStyles';
import { projects } from '../data/projects';
import type { IssuesPRsData, RepoIssuesPRs } from '../types';

type TimeRange = 4 | 8 | 12;

interface Props {
  data: IssuesPRsData;
}

function TrendArrow({ trend }: { trend: Trend }) {
  if (trend === 'growing') return <ArrowUp size={16} className="text-red-500" />;
  if (trend === 'shrinking') return <ArrowDown size={16} className="text-green-500" />;
  return <ArrowRight size={16} className="text-gray-400" />;
}

interface RowData {
  slug: string;
  name: string;
  repo: string;
  repoData: RepoIssuesPRs;
  trend: Trend;
}

function ExpandedRowDetail({
  repoData,
  timeRange,
  tooltipStyle,
  tickStyle,
  gridStroke,
}: {
  repoData: RepoIssuesPRs;
  timeRange: TimeRange;
  tooltipStyle: React.CSSProperties;
  tickStyle: { fontSize: number; fill: string };
  gridStroke: string;
}) {
  const issueVelocity = useMemo(
    () => repoData.issues.velocity.slice(-timeRange),
    [repoData.issues.velocity, timeRange],
  );
  const prVelocity = useMemo(
    () => repoData.pullRequests.velocity.slice(-timeRange),
    [repoData.pullRequests.velocity, timeRange],
  );
  const issueCategories = useMemo(
    () =>
      Object.entries(repoData.issues.categories)
        .map(([name, count]) => ({ name, count }))
        .sort((a, b) => b.count - a.count)
        .slice(0, 8),
    [repoData.issues.categories],
  );
  const ageData = useMemo(
    () =>
      (['fresh', 'recent', 'aging', 'stale', 'ancient'] as const).map((bucket) => ({
        bucket,
        issues: repoData.issues.ageBuckets[bucket],
        prs: repoData.pullRequests.ageBuckets[bucket],
      })),
    [repoData.issues.ageBuckets, repoData.pullRequests.ageBuckets],
  );

  return (
    <div className="bg-gray-50 dark:bg-gray-900/50 p-4">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Issue Categories */}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h4 className="text-xs font-medium text-gray-600 dark:text-gray-400 mb-2">
            Top Issue Categories
          </h4>
          {issueCategories.length > 0 ? (
            <ResponsiveContainer
              width="100%"
              height={Math.max(issueCategories.length * 28, 80)}
            >
              <BarChart
                data={issueCategories}
                layout="vertical"
                margin={{ top: 0, right: 16, bottom: 0, left: 70 }}
              >
                <CartesianGrid
                  strokeDasharray="3 3"
                  stroke={gridStroke}
                  horizontal={false}
                />
                <XAxis type="number" tick={tickStyle} allowDecimals={false} />
                <YAxis
                  type="category"
                  dataKey="name"
                  tick={tickStyle}
                  width={65}
                />
                <Tooltip contentStyle={tooltipStyle} />
                <Bar dataKey="count" name="Count" radius={[0, 4, 4, 0]}>
                  {issueCategories.map((entry) => (
                    <Cell
                      key={entry.name}
                      fill={getCategoryColor(entry.name)}
                    />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-xs text-gray-400 py-2">No category data.</p>
          )}
        </div>

        {/* Issue Velocity */}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h4 className="text-xs font-medium text-gray-600 dark:text-gray-400 mb-2">
            Issue Velocity ({timeRange}w)
          </h4>
          {issueVelocity.length > 0 ? (
            <ResponsiveContainer width="100%" height={160}>
              <LineChart
                data={issueVelocity}
                margin={{ top: 4, right: 16, bottom: 0, left: 0 }}
              >
                <CartesianGrid
                  strokeDasharray="3 3"
                  stroke={gridStroke}
                />
                <XAxis
                  dataKey="week"
                  tick={tickStyle}
                  tickFormatter={formatWeekTick}
                />
                <YAxis tick={tickStyle} allowDecimals={false} />
                <Tooltip contentStyle={tooltipStyle} />
                <Legend />
                <Line
                  type="monotone"
                  dataKey="opened"
                  stroke="#ef4444"
                  name="Opened"
                  strokeWidth={2}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="closed"
                  stroke="#22c55e"
                  name="Closed"
                  strokeWidth={2}
                  dot={false}
                />
              </LineChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-xs text-gray-400 py-2">No velocity data.</p>
          )}
        </div>

        {/* Age Distribution */}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h4 className="text-xs font-medium text-gray-600 dark:text-gray-400 mb-2">
            Age Distribution
          </h4>
          <ResponsiveContainer width="100%" height={140}>
            <BarChart
              data={ageData}
              margin={{ top: 4, right: 16, bottom: 0, left: 0 }}
            >
              <CartesianGrid
                strokeDasharray="3 3"
                stroke={gridStroke}
              />
              <XAxis dataKey="bucket" tick={tickStyle} />
              <YAxis tick={tickStyle} allowDecimals={false} />
              <Tooltip contentStyle={tooltipStyle} />
              <Legend />
              <Bar dataKey="issues" name="Issues" radius={[4, 4, 0, 0]}>
                {ageData.map((entry) => (
                  <Cell
                    key={`issue-${entry.bucket}`}
                    fill={AGE_COLORS[entry.bucket]}
                  />
                ))}
              </Bar>
              <Bar dataKey="prs" name="PRs" radius={[4, 4, 0, 0]}>
                {ageData.map((entry) => (
                  <Cell
                    key={`pr-${entry.bucket}`}
                    fill={AGE_COLORS[entry.bucket]}
                    opacity={0.6}
                  />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>

        {/* PR Review Metrics + PR Velocity */}
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4">
          <h4 className="text-xs font-medium text-gray-600 dark:text-gray-400 mb-2">
            PR Review Metrics
          </h4>
          <div className="grid grid-cols-2 gap-3 mb-3">
            <div className="text-center">
              <p className="text-lg font-bold text-gray-900 dark:text-white">
                {repoData.pullRequests.review.awaitingReview}
              </p>
              <p className="text-xs text-gray-500 dark:text-gray-400">
                Awaiting Review
              </p>
            </div>
            <div className="text-center">
              <p className="text-lg font-bold text-gray-900 dark:text-white">
                {repoData.pullRequests.review.noReviewer}
              </p>
              <p className="text-xs text-gray-500 dark:text-gray-400">
                No Reviewer
              </p>
            </div>
            <div className="text-center">
              <p className="text-lg font-bold text-gray-900 dark:text-white">
                {repoData.pullRequests.review.avgDaysToFirstReview.toFixed(1)}d
              </p>
              <p className="text-xs text-gray-500 dark:text-gray-400">
                Avg First Review
              </p>
            </div>
            <div className="text-center">
              <p className="text-lg font-bold text-gray-900 dark:text-white">
                {repoData.pullRequests.review.avgDaysToMerge.toFixed(1)}d
              </p>
              <p className="text-xs text-gray-500 dark:text-gray-400">
                Avg Time to Merge
              </p>
            </div>
          </div>
          <h4 className="text-xs font-medium text-gray-600 dark:text-gray-400 mb-2">
            PR Velocity ({timeRange}w)
          </h4>
          {prVelocity.length > 0 ? (
            <ResponsiveContainer width="100%" height={100}>
              <LineChart
                data={prVelocity}
                margin={{ top: 4, right: 16, bottom: 0, left: 0 }}
              >
                <XAxis
                  dataKey="week"
                  tick={tickStyle}
                  tickFormatter={formatWeekTick}
                />
                <YAxis tick={tickStyle} allowDecimals={false} />
                <Tooltip contentStyle={tooltipStyle} />
                <Line
                  type="monotone"
                  dataKey="opened"
                  stroke="#3b82f6"
                  name="Opened"
                  strokeWidth={1.5}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="merged"
                  stroke="#8b5cf6"
                  name="Merged"
                  strokeWidth={1.5}
                  dot={false}
                />
              </LineChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-xs text-gray-400 py-2">No PR velocity data.</p>
          )}
        </div>
      </div>
    </div>
  );
}

export default function IssuesPRsDashboard({ data }: Props) {
  const { resolved } = useTheme();
  const dark = resolved === 'dark';
  const [timeRange, setTimeRange] = useState<TimeRange>(12);
  const [expandedSlug, setExpandedSlug] = useState<string | null>(null);

  const rows = useMemo<RowData[]>(() => {
    return projects
      .filter((p) => data.repos[p.repo.toLowerCase()])
      .map((p) => {
        const repoData = data.repos[p.repo.toLowerCase()];
        return {
          slug: p.slug,
          name: p.name,
          repo: p.repo,
          repoData,
          trend: computeTrend(repoData.issues.velocity),
        };
      });
  }, [data]);

  const { tooltipStyle, tickStyle, gridStroke } = getChartStyles(dark);

  const ranges: TimeRange[] = [4, 8, 12];

  return (
    <section id="issues-prs" className="mb-6">
      {/* Header with time range selector */}
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-gray-200">
          Issues &amp; PRs Overview
        </h2>
        <div className="flex gap-1">
          {ranges.map((r) => (
            <button
              key={r}
              onClick={() => setTimeRange(r)}
              aria-pressed={timeRange === r}
              aria-label={`Show ${r} weeks of data`}
              className={`px-2 py-1 text-xs rounded ${
                timeRange === r
                  ? 'bg-nvidia-green text-white'
                  : 'bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-400'
              }`}
            >
              {r}w
            </button>
          ))}
        </div>
      </div>

      {/* Comparison Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-gray-200 dark:border-gray-700 text-left">
              <th className="p-3 w-8" />
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300">Project</th>
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300 text-right">Open Issues</th>
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300 text-right">Open PRs</th>
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300 text-center">Velocity</th>
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300 text-right">Stale Issues</th>
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300 text-right">Review Backlog</th>
              <th className="p-3 font-medium text-gray-700 dark:text-gray-300 text-center">Trend</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const expanded = expandedSlug === row.slug;
              const staleCount =
                row.repoData.issues.ageBuckets.stale + row.repoData.issues.ageBuckets.ancient;

              return (
                <Fragment key={row.slug}>
                  {/* Main row */}
                  <tr
                    onClick={() => setExpandedSlug(expanded ? null : row.slug)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        setExpandedSlug(expanded ? null : row.slug);
                      }
                    }}
                    role="button"
                    tabIndex={0}
                    className="border-b border-gray-100 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-700/50 cursor-pointer"
                  >
                    <td className="p-3">
                      {expanded ? (
                        <ChevronDown size={16} className="text-gray-500" />
                      ) : (
                        <ChevronRight size={16} className="text-gray-500" />
                      )}
                    </td>
                    <td className="p-3 font-medium text-gray-900 dark:text-white">
                      <Link
                        to={`/projects/${row.slug}#issues-prs`}
                        onClick={(e) => e.stopPropagation()}
                        className="hover:text-nvidia-green hover:underline"
                      >
                        {row.name}
                      </Link>
                    </td>
                    <td className="p-3 text-right text-gray-700 dark:text-gray-300">
                      {row.repoData.issues.total}
                    </td>
                    <td className="p-3 text-right text-gray-700 dark:text-gray-300">
                      {row.repoData.pullRequests.total}
                    </td>
                    <td className="p-3 text-center">
                      <VelocitySparkline data={row.repoData.issues.velocity} weeks={timeRange} />
                    </td>
                    <td className="p-3 text-right">
                      <span
                        className={
                          staleCount > 0
                            ? 'text-orange-500 font-medium'
                            : 'text-gray-500 dark:text-gray-400'
                        }
                      >
                        {staleCount}
                      </span>
                    </td>
                    <td className="p-3 text-right">
                      <span
                        className={
                          row.repoData.pullRequests.review.awaitingReview > 0
                            ? 'text-yellow-600 dark:text-yellow-400 font-medium'
                            : 'text-gray-500 dark:text-gray-400'
                        }
                      >
                        {row.repoData.pullRequests.review.awaitingReview}
                      </span>
                    </td>
                    <td className="p-3 text-center">
                      <TrendArrow trend={row.trend} />
                    </td>
                  </tr>

                  {/* Expanded detail */}
                  {expanded && (
                    <tr className="border-b border-gray-100 dark:border-gray-700">
                      <td colSpan={8} className="p-0">
                        <ExpandedRowDetail
                          repoData={row.repoData}
                          timeRange={timeRange}
                          tooltipStyle={tooltipStyle}
                          tickStyle={tickStyle}
                          gridStroke={gridStroke}
                        />
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
