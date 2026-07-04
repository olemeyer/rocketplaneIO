// Client-sichere Demo-Zugangsdaten (nur für die lokale Scheibe; werden auf der
// Login-Seite als Hinweis angezeigt). Getrennt von users.ts, damit die
// Credential-Prüflogik nicht ins Client-Bundle wandert.
export const DEMO_CREDENTIALS = {
  email: 'demo@rocketplane.io',
  password: 'rocketplane',
} as const;
