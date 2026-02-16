import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import Pagination from '../Pagination';

describe('Pagination', () => {
  it('renders nothing when totalPages <= 1', () => {
    const { container } = render(
      <Pagination currentPage={1} totalPages={1} onPageChange={() => {}} />
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders prev/next buttons and page indicator', () => {
    render(
      <Pagination currentPage={1} totalPages={3} onPageChange={() => {}} />
    );
    expect(screen.getByRole('button', { name: /previous/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument();
    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument();
  });

  it('disables Previous button on first page', () => {
    render(
      <Pagination currentPage={1} totalPages={3} onPageChange={() => {}} />
    );
    expect(screen.getByRole('button', { name: /previous/i })).toBeDisabled();
  });

  it('disables Next button on last page', () => {
    render(
      <Pagination currentPage={3} totalPages={3} onPageChange={() => {}} />
    );
    expect(screen.getByRole('button', { name: /next/i })).toBeDisabled();
  });

  it('calls onPageChange with next page when Next clicked', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Pagination currentPage={2} totalPages={5} onPageChange={onChange} />
    );
    await user.click(screen.getByRole('button', { name: /next/i }));
    expect(onChange).toHaveBeenCalledWith(3);
  });

  it('calls onPageChange with previous page when Previous clicked', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Pagination currentPage={3} totalPages={5} onPageChange={onChange} />
    );
    await user.click(screen.getByRole('button', { name: /previous/i }));
    expect(onChange).toHaveBeenCalledWith(2);
  });

  it('enables both buttons on a middle page', () => {
    render(
      <Pagination currentPage={2} totalPages={3} onPageChange={() => {}} />
    );
    expect(screen.getByRole('button', { name: /previous/i })).toBeEnabled();
    expect(screen.getByRole('button', { name: /next/i })).toBeEnabled();
  });
});
