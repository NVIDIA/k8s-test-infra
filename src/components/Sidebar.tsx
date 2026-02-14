import { Link, useLocation } from 'react-router';

interface SidebarItem {
  to: string;
  label: string;
}

interface SidebarProps {
  items: SidebarItem[];
  title?: string;
}

export default function Sidebar({ items, title }: SidebarProps) {
  const location = useLocation();

  if (items.length === 0) return null;

  return (
    <aside className="w-64 shrink-0 bg-white border-r border-gray-200 p-4 hidden lg:block">
      {title && (
        <h3 className="text-sm font-semibold text-gray-500 uppercase tracking-wider mb-3">
          {title}
        </h3>
      )}
      <nav className="space-y-1">
        {items.map(({ to, label }) => {
          const active = location.pathname === to;
          return (
            <Link
              key={to}
              to={to}
              className={`block px-3 py-2 rounded-md text-sm font-medium transition-colors ${
                active
                  ? 'bg-nvidia-green/10 text-nvidia-green font-semibold'
                  : 'text-gray-700 hover:bg-gray-100'
              }`}
            >
              {label}
            </Link>
          );
        })}
      </nav>
    </aside>
  );
}
