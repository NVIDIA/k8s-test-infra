import { ChevronLeft, ChevronRight } from 'lucide-react';

interface PaginationProps {
  currentPage: number;
  totalPages: number;
  onPageChange: (page: number) => void;
}

export default function Pagination({ currentPage, totalPages, onPageChange }: PaginationProps) {
  if (totalPages <= 1) return null;

  return (
    <div className="flex items-center justify-between px-4 py-3 border-t border-gray-200 dark:border-gray-700">
      <button
        onClick={() => onPageChange(currentPage - 1)}
        disabled={currentPage <= 1}
        aria-label="Previous page"
        className="inline-flex items-center gap-1 px-3 py-1.5 text-sm font-medium rounded-md transition-colors disabled:opacity-40 disabled:cursor-not-allowed text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700"
      >
        <ChevronLeft size={16} />
        Previous
      </button>
      <span className="text-sm text-gray-600 dark:text-gray-400">
        Page {currentPage} of {totalPages}
      </span>
      <button
        onClick={() => onPageChange(currentPage + 1)}
        disabled={currentPage >= totalPages}
        aria-label="Next page"
        className="inline-flex items-center gap-1 px-3 py-1.5 text-sm font-medium rounded-md transition-colors disabled:opacity-40 disabled:cursor-not-allowed text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700"
      >
        Next
        <ChevronRight size={16} />
      </button>
    </div>
  );
}
