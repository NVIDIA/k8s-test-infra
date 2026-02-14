import { Link, useLocation } from 'react-router';
import { BarChart3, FolderOpen, Home } from 'lucide-react';

const navItems = [
  { to: '/', label: 'Home', icon: Home },
  { to: '/dashboard', label: 'Dashboard', icon: BarChart3 },
  { to: '/projects', label: 'Projects', icon: FolderOpen },
];

export default function Navbar() {
  const location = useLocation();

  return (
    <nav className="bg-nvidia-black text-white">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="flex items-center justify-between h-16">
          <Link to="/" className="flex items-center gap-3">
            <div className="w-8 h-8 bg-nvidia-green rounded flex items-center justify-center font-bold text-sm">
              NV
            </div>
            <span className="text-lg font-semibold hidden sm:block">
              Cloud Native Test Infrastructure
            </span>
          </Link>
          <div className="flex items-center gap-1">
            {navItems.map(({ to, label, icon: Icon }) => {
              const active =
                to === '/'
                  ? location.pathname === '/'
                  : location.pathname.startsWith(to);
              return (
                <Link
                  key={to}
                  to={to}
                  className={`flex items-center gap-2 px-3 py-2 rounded-md text-sm font-medium transition-colors ${
                    active
                      ? 'bg-nvidia-green text-white'
                      : 'text-gray-300 hover:bg-gray-700 hover:text-white'
                  }`}
                >
                  <Icon size={16} />
                  <span className="hidden sm:inline">{label}</span>
                </Link>
              );
            })}
          </div>
        </div>
      </div>
    </nav>
  );
}
