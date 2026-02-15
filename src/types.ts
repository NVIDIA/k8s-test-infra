export interface TestResult {
  project: string;
  repo: string;
  lastRun: string;
  passed: number;
  failed: number;
  skipped: number;
  actionRunUrl: string;
  source: 'ginkgo' | 'workflow_status';
}

export interface WorkflowStatus {
  repo: string;
  workflow: string;
  status: 'success' | 'failure' | 'in_progress' | 'unknown';
  conclusion: string;
  runUrl: string;
  updatedAt: string;
  commitSha: string;
  commitUrl: string;
}

export interface ImageBuild {
  repo: string;
  tag: string;
  pushedAt: string;
  htmlUrl: string;
}

export interface RepoInfo {
  name: string;
  fullName: string;
  description: string;
  stars: number;
  forks: number;
  language: string;
  license: string;
  htmlUrl: string;
  topics: string[];
  readme: string;
}

export interface Project {
  slug: string;
  name: string;
  repo: string;
  description: string;
  category: 'operator' | 'runtime' | 'driver' | 'testing' | 'library';
}

export interface TrafficDay {
  date: string;
  count: number;
  uniques: number;
}

export interface RepoTraffic {
  clones: TrafficDay[];
  views: TrafficDay[];
}

export interface RepoStatsEntry {
  date: string;
  stars: number;
  forks: number;
}

export interface HistorySnapshot {
  timestamp: string;
  workflows: Record<string, number>;
  perRepo: Record<string, Record<string, number>>;
}

export interface HistoryFile {
  snapshots: HistorySnapshot[];
  traffic: Record<string, RepoTraffic>;
  repoStats: Record<string, RepoStatsEntry[]>;
}
