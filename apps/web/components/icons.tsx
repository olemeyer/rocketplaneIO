import type { SVGProps } from 'react';

// Feines, konsistentes Line-Icon-Set (24er-Grid, stroke 1.5, currentColor).
type IconProps = SVGProps<SVGSVGElement>;

function base(props: IconProps) {
  return {
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.5,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
    ...props,
  };
}

export const Compass = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="9" />
    <path d="m15.5 8.5-2 5-5 2 2-5 5-2Z" />
  </svg>
);

export const Cube = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M12 2.8 20 7v10l-8 4.2L4 17V7l8-4.2Z" />
    <path d="M4 7l8 4.2L20 7M12 11.2V21" />
  </svg>
);

export const Waterfall = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M4 6h10M4 12h14M4 18h7" />
    <path d="M17 5.5v3M20 11v3M12 17v3" opacity="0.5" />
  </svg>
);

export const Logs = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M8 6h12M8 12h12M8 18h12" />
    <path d="M4 6h.01M4 12h.01M4 18h.01" />
  </svg>
);

export const Metrics = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M4 15l4-5 3 3 5-7 4 5" />
    <path d="M3 20h18" opacity="0.4" />
  </svg>
);

export const Dashboards = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3.5" y="3.5" width="7" height="9" rx="1.5" />
    <rect x="13.5" y="3.5" width="7" height="5" rx="1.5" />
    <rect x="13.5" y="11.5" width="7" height="9" rx="1.5" />
    <rect x="3.5" y="15.5" width="7" height="5" rx="1.5" />
  </svg>
);

export const Bell = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M18 8.5a6 6 0 1 0-12 0c0 6-2.5 7.5-2.5 7.5h17S18 14.5 18 8.5Z" />
    <path d="M10.3 20a2 2 0 0 0 3.4 0" />
  </svg>
);

export const Search = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3.2-3.2" />
  </svg>
);

export const Gear = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="3.2" />
    <path d="M12 2.5v2.6M12 18.9v2.6M4.2 7l2.2 1.3M17.6 15.7l2.2 1.3M4.2 17l2.2-1.3M17.6 8.3l2.2-1.3" />
  </svg>
);

export const Github = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M9 19c-4 1.4-4-2-6-2.5m12 4.5v-3.6a3 3 0 0 0-.9-2.4c2.9-.3 6-1.4 6-6.4a4.8 4.8 0 0 0-1.3-3.4 4.5 4.5 0 0 0-.1-3.4s-1-.3-3.4 1.3a11.6 11.6 0 0 0-6 0C6.9 1.9 5.9 2.2 5.9 2.2a4.5 4.5 0 0 0-.1 3.4A4.8 4.8 0 0 0 4.5 9c0 5 3 6.1 5.9 6.4a3 3 0 0 0-.9 2.3V21" />
  </svg>
);

export const ArrowRight = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M5 12h14M13 6l6 6-6 6" />
  </svg>
);

export const CommandKey = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M9 6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6Z" />
  </svg>
);

export const Bolt = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M13 2 4.5 13.5H11l-1 8.5 8.5-11.5H12l1-8.5Z" />
  </svg>
);

export const Shield = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M12 2.5 4.5 5.5v6c0 4.5 3.2 7.7 7.5 9.5 4.3-1.8 7.5-5 7.5-9.5v-6L12 2.5Z" />
    <path d="m9 12 2 2 4-4" />
  </svg>
);

export const Layers = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m12 3 9 5-9 5-9-5 9-5Z" />
    <path d="m3 13 9 5 9-5" opacity="0.55" />
  </svg>
);

export const Terminal = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <path d="m7.5 9.5 2.5 2.5-2.5 2.5M12.5 15h4" />
  </svg>
);

export const ChevronDown = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m6 9 6 6 6-6" />
  </svg>
);

export const CornerReturn = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M9 10 5 14l4 4" />
    <path d="M5 14h11a3 3 0 0 0 3-3V6" />
  </svg>
);
