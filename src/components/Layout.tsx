import { type ReactNode } from 'react';
import Navbar from './Navbar';
import Footer from './Footer';
import Sidebar from './Sidebar';

interface LayoutProps {
  children: ReactNode;
  sidebarItems?: { to: string; label: string }[];
  sidebarTitle?: string;
}

export default function Layout({
  children,
  sidebarItems = [],
  sidebarTitle,
}: LayoutProps) {
  return (
    <div className="min-h-screen flex flex-col bg-gray-50 dark:bg-gray-900">
      <Navbar />
      <div className="flex flex-1">
        <Sidebar items={sidebarItems} title={sidebarTitle} />
        <main className="flex-1 p-6 lg:p-8 max-w-7xl">{children}</main>
      </div>
      <Footer />
    </div>
  );
}
