// workflow-frontmatter.test.ts — locks the front-matter grammar and the lint
// rules (incl. the enforced snapshot-coherence checks) so the editor's Save-gate
// stays trustworthy.
import { describe, expect, it } from 'vitest';
import { parseFrontmatter, lintWorkflow, hasBlockingError } from './workflow-frontmatter';

const errs = (src: string) => lintWorkflow(src).diags.filter((d) => d.severity === 'error').map((d) => d.message);
const warns = (src: string) => lintWorkflow(src).diags.filter((d) => d.severity === 'warning').map((d) => d.message);

describe('parseFrontmatter', () => {
  it('reads scalar keys, targets and timeout', () => {
    const m = parseFrontmatter(
      ['# @name safe-scale', '# @summary scale it', '# @risk medium', '# @reversible snapshot', '# @targets Deployment,StatefulSet', '# @timeout 300', '#', 'x = 1'].join('\n'),
    );
    expect(m.name).toBe('safe-scale');
    expect(m.summary).toBe('scale it');
    expect(m.risk).toBe('medium');
    expect(m.reversible).toBe('snapshot');
    expect(m.targets).toEqual(['Deployment', 'StatefulSet']);
    expect(m.timeout).toBe(300);
  });

  it('parses typed params: int range, enum options, required, default', () => {
    const m = parseFrontmatter(
      ['# @param replicas int 0 50 required', '# @param mode enum a b c', '# @param ns namespace =shop'].join('\n'),
    );
    expect(m.params).toEqual([
      { name: 'replicas', type: 'int', min: 0, max: 50, required: true },
      { name: 'mode', type: 'enum', options: ['a', 'b', 'c'] },
      { name: 'ns', type: 'namespace', default: 'shop' },
    ]);
  });

  it('ignores non-directive comments and code', () => {
    const m = parseFrontmatter(['# just a comment', '# @name x', 'k8s.scale(a,b,c,1)'].join('\n'));
    expect(m.name).toBe('x');
    expect(m.params).toEqual([]);
  });
});

const FM = (rev: string, extra = '') => `# @name w\n# @summary s\n# @reversible ${rev}\n${extra}#\n`;

describe('lintWorkflow — front-matter completeness', () => {
  it('flags a missing name as an error', () => {
    expect(errs('# @reversible snapshot\n#\n')).toContain('missing `# @name` — every workflow needs a kebab-case name');
  });
  it('flags a non-kebab name', () => {
    expect(errs('# @name Bad_Name\n# @reversible readonly\n#\nx=1')).toEqual(expect.arrayContaining([expect.stringContaining('kebab-case')]));
  });
  it('flags an invalid risk / reversible enum', () => {
    expect(errs('# @name w\n# @risk huge\n# @reversible snapshot\n#\nx=1').join()).toContain('@risk');
    expect(errs('# @name w\n# @reversible maybe\n#\nx=1').join()).toContain('@reversible');
  });
  it('warns on a missing summary', () => {
    expect(warns('# @name w\n# @reversible readonly\n#\nx=1')).toEqual(expect.arrayContaining([expect.stringContaining('@summary')]));
  });
});

describe('lintWorkflow — snapshot coherence (enforced)', () => {
  it('errors when a readonly workflow mutates', () => {
    const src = FM('readonly') + 'k8s.scale("n", "Deployment", "a", 2)';
    expect(errs(src)).toEqual(expect.arrayContaining([expect.stringContaining('readonly but the script mutates')]));
    expect(hasBlockingError(lintWorkflow(src).diags)).toBe(true);
  });

  it('errors when a snapshot workflow does a whole-object mutation without snapshot()', () => {
    const src = FM('snapshot') + 'k8s.patch("n", "Deployment", "a", {"spec": {"replicas": 2}})';
    expect(errs(src)).toEqual(expect.arrayContaining([expect.stringContaining('must be preceded by snapshot')]));
  });

  it('passes when snapshot() precedes the whole-object mutation', () => {
    const src = FM('snapshot') + 'snapshot("n", "Deployment", "a")\nk8s.patch("n", "Deployment", "a", {"spec": {"replicas": 2}})';
    expect(errs(src)).toEqual([]);
  });

  it('exempts field-scoped mutators (they name their path)', () => {
    const src = FM('snapshot') + 'k8s.set_field("n", "Deployment", "a", ["metadata", "annotations", "k"], "v")';
    expect(errs(src)).toEqual([]);
  });

  it('notes (info, non-blocking) a snapshot() in a none workflow', () => {
    const src = FM('none') + 'snapshot("n", "Pod", "p")\nk8s.delete("n", "Pod", "p")';
    const { diags } = lintWorkflow(src);
    expect(hasBlockingError(diags)).toBe(false);
    expect(diags.some((d) => d.severity === 'info' && /will not be offered as a revert/.test(d.message))).toBe(true);
  });

  it('a valid snapshot workflow has no blocking errors', () => {
    const src = FM('snapshot', '# @param replicas int 0 50 required\n') +
      'ns = args["namespace"]\nsnapshot(ns, "Deployment", args["name"])\nk8s.scale(ns, "Deployment", args["name"], int(args["replicas"]))';
    expect(hasBlockingError(lintWorkflow(src).diags)).toBe(false);
  });
});
