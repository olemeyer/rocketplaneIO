// workflow-frontmatter.ts — the single source for a workflow's metadata: it lives
// in the script itself as `# @key value` front-matter, co-evaluated by the editor.
// Technicians write ONE thing (the script); name/risk/reversibility/targets/params
// all derive from it. This module parses that front-matter and lints the workflow
// (incl. whether snapshots are used correctly) so Save can block broken/incoherent
// workflows.

export type WorkflowParam = {
  name: string;
  type: string; // string|int|bool|enum|namespace|workload|node|secret
  options?: string[];
  min?: number;
  max?: number;
  required?: boolean;
  default?: string;
};

export type WorkflowMeta = {
  name?: string;
  summary?: string;
  risk?: string; // low|medium|high
  reversible?: string; // snapshot|readonly|none
  targets: string[];
  params: WorkflowParam[];
  timeout?: number;
};

export const RISK_VALUES = ['low', 'medium', 'high'];
export const REVERSIBLE_VALUES = ['snapshot', 'readonly', 'none'];
export const PARAM_TYPES = ['string', 'int', 'bool', 'enum', 'namespace', 'workload', 'node', 'secret'];
const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$/;

// mutating builtins on the snapshot surface (whole-object capture)
const WHOLE_OBJECT_MUT = /\bk8s\.(patch|scale|delete|create|apply)\s*\(/;
// field-scoped mutators — self-capturing, no explicit snapshot() needed
const FIELD_MUT = /\bk8s\.(set_field|set_fields|patch_configmap)\s*\(/;
// any cluster mutation — used to reject mutators in a @reversible readonly workflow.
// evict is final (@reversible none): a mutation, but never whole-object-snapshotted.
const ANY_MUT = /\bk8s\.(patch|scale|delete|create|apply|set_field|set_fields|patch_configmap|evict)\s*\(/;
const SNAPSHOT_CALL = /\bsnapshot\s*\(/;

export type Diag = { line: number; severity: 'error' | 'warning' | 'info'; message: string };

// parseFrontmatter reads the leading `# @key value` block (and @param lines).
export function parseFrontmatter(source: string): WorkflowMeta {
  const m: WorkflowMeta = { targets: [], params: [] };
  for (const raw of source.split('\n')) {
    const t = raw.trim();
    if (!t.startsWith('# @')) continue;
    const rest = t.slice(3);
    const sp = rest.indexOf(' ');
    const key = sp < 0 ? rest : rest.slice(0, sp);
    const val = sp < 0 ? '' : rest.slice(sp + 1).trim();
    switch (key) {
      case 'name': m.name = val; break;
      case 'summary': m.summary = val; break;
      case 'risk': m.risk = val; break;
      case 'reversible': m.reversible = val; break;
      case 'timeout': m.timeout = Number(val) || undefined; break;
      case 'targets':
        m.targets = val.split(',').map((s) => s.trim()).filter(Boolean);
        break;
      case 'param': {
        const p = parseParam(val);
        if (p) m.params.push(p);
        break;
      }
    }
  }
  return m;
}

// `# @param <name> <type> [min max | opt1 opt2 …] [required] [=default]`
function parseParam(val: string): WorkflowParam | null {
  const toks = val.split(/\s+/).filter(Boolean);
  const name = toks[0];
  if (!name) return null;
  const p: WorkflowParam = { name, type: toks[1] ?? 'string' };
  const extra: string[] = [];
  for (const tk of toks.slice(2)) {
    if (tk === 'required' || tk === 'req') p.required = true;
    else if (tk.startsWith('=')) p.default = tk.slice(1);
    else extra.push(tk);
  }
  if (p.type === 'int' && extra.length >= 2) {
    p.min = Number(extra[0]);
    p.max = Number(extra[1]);
  } else if (p.type === 'enum' && extra.length) {
    p.options = extra;
  }
  return p;
}

// lintWorkflow returns editor diagnostics. blocking = there is at least one error.
export function lintWorkflow(source: string): { meta: WorkflowMeta; diags: Diag[] } {
  const meta = parseFrontmatter(source);
  const lines = source.split('\n');
  const diags: Diag[] = [];
  const lineOf = (re: RegExp): number => {
    const i = lines.findIndex((l) => re.test(l) && l.trim().startsWith('# @'));
    return i >= 0 ? i : 0;
  };

  // ── front-matter completeness ──
  if (!meta.name) diags.push({ line: 0, severity: 'error', message: 'missing `# @name` — every workflow needs a kebab-case name' });
  else if (!NAME_RE.test(meta.name)) diags.push({ line: lineOf(/@name/), severity: 'error', message: `@name "${meta.name}" must be kebab-case (a-z, 0-9, -)` });
  if (!meta.summary) diags.push({ line: 0, severity: 'warning', message: 'missing `# @summary` — a one-line description shown in the library' });
  if (!meta.reversible) diags.push({ line: 0, severity: 'warning', message: 'missing `# @reversible` (snapshot | readonly | none) — declare how this run can be undone' });
  else if (!REVERSIBLE_VALUES.includes(meta.reversible)) diags.push({ line: lineOf(/@reversible/), severity: 'error', message: `@reversible "${meta.reversible}" must be one of: ${REVERSIBLE_VALUES.join(', ')}` });
  if (meta.risk && !RISK_VALUES.includes(meta.risk)) diags.push({ line: lineOf(/@risk/), severity: 'error', message: `@risk "${meta.risk}" must be one of: ${RISK_VALUES.join(', ')}` });

  // ── snapshot / reversibility coherence (the "does it use snapshot correctly?" check) ──
  const bodyLine = (re: RegExp): number => {
    const i = lines.findIndex((l) => re.test(l) && !l.trim().startsWith('#'));
    return i >= 0 ? i : 0;
  };
  const hasWhole = WHOLE_OBJECT_MUT.test(source);
  const hasField = FIELD_MUT.test(source);
  const hasMut = ANY_MUT.test(source);
  const hasSnap = SNAPSHOT_CALL.test(stripFrontmatter(source));

  if (meta.reversible === 'readonly' && hasMut) {
    diags.push({ line: bodyLine(ANY_MUT), severity: 'error', message: 'declared @reversible readonly but the script mutates the cluster — use a mutator only in a snapshot/none workflow' });
  }
  // Enforced (not just advised): a snapshot-reversible whole-object mutation MUST
  // call snapshot(namespace, kind, name) first, so the capture is load-bearing and
  // visible — not left to the mutator's implicit auto-capture. Field mutators
  // (set_field/patch_configmap) name their exact path, so they are exempt.
  if (meta.reversible === 'snapshot' && hasWhole && !hasSnap && !hasField) {
    diags.push({ line: bodyLine(WHOLE_OBJECT_MUT), severity: 'error', message: 'a whole-object mutation in a @reversible snapshot workflow must be preceded by snapshot(namespace, kind, name)' });
  }
  if (meta.reversible === 'none' && hasSnap) {
    diags.push({ line: bodyLine(SNAPSHOT_CALL), severity: 'info', message: '@reversible none but snapshot() is called — the capture will not be offered as a revert' });
  }
  if ((meta.reversible === 'snapshot' || !meta.reversible) && !hasMut && !SNAPSHOT_CALL.test(source)) {
    diags.push({ line: 0, severity: 'info', message: 'no mutation detected — if this only reads, mark it `# @reversible readonly`' });
  }

  return { meta, diags };
}

export function hasBlockingError(diags: Diag[]): boolean {
  return diags.some((d) => d.severity === 'error');
}

// stripFrontmatter removes the `# @key` directive lines (used before checking for
// snapshot() in the body, so an @-comment mentioning snapshot doesn't count).
export function stripFrontmatter(source: string): string {
  return source
    .split('\n')
    .filter((l) => !l.trim().startsWith('# @'))
    .join('\n');
}
