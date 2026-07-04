import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import Page from '../app/page';

describe('<Page> — Landing', () => {
  it('zeigt die Hero-Headline', () => {
    render(<Page />);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/speed of thought/i);
  });

  it('nennt die drei Kernversprechen', () => {
    render(<Page />);
    for (const t of ['Keyboard-first', 'OpenTelemetry-native', 'Self-hostable']) {
      expect(screen.getByRole('heading', { name: t })).toBeInTheDocument();
    }
  });

  it('rendert die App-Shell mit Service-Health', () => {
    render(<Page />);
    expect(screen.getByText('payment-gateway')).toBeInTheDocument();
    expect(screen.getByText('cart-service')).toBeInTheDocument();
  });

  it('öffnet die Command-Palette per Ctrl/Cmd+K', () => {
    render(<Page />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    const dialog = screen.getByRole('dialog', { name: /command menu/i });
    expect(within(dialog).getByPlaceholderText(/type a command/i)).toBeInTheDocument();
  });
});
