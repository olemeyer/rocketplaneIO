'use client';

import { useEffect, useState } from 'react';
import { CommandPalette } from '../command-palette';

// Kapselt den Cmd/Ctrl+K-Keybind für die Explore-Route; die Landing bleibt
// davon unberührt (die hat ihre eigene Verdrahtung in app/page.tsx).
export function CommandPaletteHost() {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setOpen((o) => !o);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return <CommandPalette open={open} onClose={() => setOpen(false)} />;
}
