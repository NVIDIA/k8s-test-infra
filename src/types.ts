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
