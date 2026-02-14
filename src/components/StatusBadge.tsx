import { CheckCircle, XCircle, Clock, HelpCircle } from 'lucide-react';

type Status = 'success' | 'failure' | 'in_progress' | 'unknown';

const config: Record<Status, { icon: typeof CheckCircle; color: string; label: string }> = {
  success: { icon: CheckCircle, color: 'text-status-pass', label: 'Passing' },
  failure: { icon: XCircle, color: 'text-status-fail', label: 'Failing' },
  in_progress: { icon: Clock, color: 'text-status-warn', label: 'Running' },
  unknown: { icon: HelpCircle, color: 'text-status-unknown', label: 'Unknown' },
};

export default function StatusBadge({ status }: { status: Status }) {
  const { icon: Icon, color, label } = config[status] ?? config.unknown;
  return (
    <span className={`inline-flex items-center gap-1 ${color}`}>
      <Icon size={16} />
      <span className="text-sm">{label}</span>
    </span>
  );
}
