'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';

// Theme-System: ein Design, zwei Modi (dark/light). FOUC-frei über ein Pre-Paint-
// Script, Persistenz in localStorage.

export type Mode = 'dark' | 'light';

const MODE_KEY = 'rp-theme';

/** Inline-Script für <head>: setzt data-theme VOR dem ersten Paint. */
export const THEME_INIT_SCRIPT = `(function(){try{
var m=localStorage.getItem('${MODE_KEY}');if(m!=='light'&&m!=='dark'){m=window.matchMedia('(prefers-color-scheme: light)').matches?'light':'dark';}
document.documentElement.setAttribute('data-theme',m);
}catch(err){document.documentElement.setAttribute('data-theme','dark');}})();`;

type ThemeContextValue = {
  mode: Mode;
  setMode: (m: Mode) => void;
  toggleMode: () => void;
  // Legacy-Aliasse für bestehende Aufrufer:
  theme: Mode;
  toggle: () => void;
};

const ThemeContext = createContext<ThemeContextValue>({
  mode: 'dark',
  setMode: () => {},
  toggleMode: () => {},
  theme: 'dark',
  toggle: () => {},
});

function persist(value: string) {
  try {
    localStorage.setItem(MODE_KEY, value);
  } catch {
    /* privater Modus */
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<Mode>('dark');

  useEffect(() => {
    const m = document.documentElement.getAttribute('data-theme');
    if (m === 'light' || m === 'dark') setModeState(m);
  }, []);

  const setMode = useCallback((m: Mode) => {
    setModeState(m);
    document.documentElement.setAttribute('data-theme', m);
    persist(m);
  }, []);

  const toggleMode = useCallback(() => {
    setModeState((prev) => {
      const next: Mode = prev === 'dark' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', next);
      persist(next);
      return next;
    });
  }, []);

  return (
    <ThemeContext.Provider value={{ mode, setMode, toggleMode, theme: mode, toggle: toggleMode }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  return useContext(ThemeContext);
}
