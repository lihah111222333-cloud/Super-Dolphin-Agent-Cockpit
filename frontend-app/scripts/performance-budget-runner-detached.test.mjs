import {
  existsSync,
  mkdirSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import {
  collectDetachedP01P02Evidence,
  collectDetachedStopFeedbackBudget,
  validateP03SubjectRuntime,
  verifyPerformanceEvidence,
} from './performance-budget-runner.mjs';
import { P03_SUBJECT_FEEDBACK_COMPONENT_PATH } from './stop-feedback-benchmark.mjs';
import {
  baseline,
  createP03SubjectClosure,
  evidence,
  p03SubjectContentFiles,
  p03SubjectContentHash,
  SUBJECT_SHA,
  SUBJECT_TREE,
} from './performance-budget-runner.test-helper.mjs';

describe('P01/P02 detached subject closure', () => {
  it('runs the P01 probe and P02 workload against the detached requested subject, not runner imports', async () => {
    const commands = [];
    let temporaryRoot = '';
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        temporaryRoot = args[3];
        mkdirSync(join(temporaryRoot, 'frontend-app', 'scripts'), { recursive: true });
        writeFileSync(join(temporaryRoot, 'frontend-app', 'scripts', 'render-isolation-probe.test.jsx'), 'subject probe');
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error('unexpected command: ' + command + ' ' + args.join(' '));
    };
    const runCommand = vi.fn(async (command, args, options) => {
      commands.push([command, ...args]);
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options.cwd).toBe(join(temporaryRoot, 'frontend-app'));
      return {
        error: undefined,
        outputTruncated: false,
        status: 0,
        stderr: '',
        stdout: '',
        timedOut: false,
      };
    });
    const target = Object.freeze({
      provenance: Object.freeze({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }),
    });
    const collectRender = vi.fn(({ frontendRoot }) => {
      expect(frontendRoot).toBe(join(temporaryRoot, 'frontend-app'));
      return { metricId: 'P01-render-isolation', updateCount: 20 };
    });
    const loadHistoryTarget = vi.fn(async (options) => {
      expect(options).toEqual({
        subjectRoot: temporaryRoot,
        subjectSha: SUBJECT_SHA,
        subjectTree: SUBJECT_TREE,
      });
      return target;
    });
    const runHistory = vi.fn(({ commit, target: loadedTarget }) => {
      expect(loadedTarget).toBe(target);
      return {
        metricId: 'P02-history-budget',
        subjectProduct: target.provenance,
        subjectSha: commit,
      };
    });

    const measured = await collectDetachedP01P02Evidence({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      collectRender,
      loadHistoryTarget,
      runCommand,
      runHistory,
    });

    expect(measured.renderIsolation).toEqual(expect.objectContaining({
      metricId: 'P01-render-isolation',
      subjectProduct: expect.objectContaining({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }),
      updateCount: 20,
    }));
    expect(measured.historyBudget.subjectSha).toBe(SUBJECT_SHA);
    expect(collectRender).toHaveBeenCalledOnce();
    expect(loadHistoryTarget).toHaveBeenCalledOnce();
    expect(runHistory).toHaveBeenCalledOnce();
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'worktree', 'add'],
      ['git', 'rev-parse', 'HEAD'],
      ['git', 'rev-parse', 'HEAD^{tree}'],
      ['git', 'status', '--porcelain'],
      ['npm', 'ci'],
      ['git', 'status', '--porcelain'],
      ['git', 'status', '--porcelain'],
      ['git', 'worktree', 'remove'],
    ]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });
});

describe('P03 detached subject runtime', () => {
  it('uses a bounded managed npm command and cleans the detached worktree after timeout', async () => {
    const commands = [];
    let temporaryRoot = '';
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        temporaryRoot = args[3];
        createP03SubjectClosure(temporaryRoot);
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error('unexpected command: ' + command + ' ' + args.join(' '));
    };
    const runCommand = vi.fn(async (command, args, options) => {
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options).toEqual(expect.objectContaining({
        cwd: join(temporaryRoot, 'frontend-app'),
        killGraceMs: expect.any(Number),
        maxBuffer: expect.any(Number),
        timeoutMs: expect.any(Number),
      }));
      return {
        error: new Error('managed command timed out after 1ms'),
        outputTruncated: false,
        signal: 'SIGTERM',
        status: null,
        stderr: '',
        stdout: 'bounded output',
        timedOut: true,
      };
    });

    await expect(collectDetachedStopFeedbackBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      runCommand,
    })).rejects.toThrow(/timed out/);
    expect(runCommand).toHaveBeenCalledOnce();
    expect(commands).toContainEqual(['git', 'worktree', 'remove', '--force', temporaryRoot]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });

  function subjectTarget() {
    return {
      attachRuntime() {},
      feedbackProbe() {},
      provenance: {
        subjectSha: SUBJECT_SHA,
        subjectTree: SUBJECT_TREE,
        runtimePath: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
        feedbackComponentPath: P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
        content: {
          contentHash: p03SubjectContentHash(p03SubjectContentFiles()),
          files: p03SubjectContentFiles(),
        },
      },
    };
  }

  it('loads the requested clean subject worktree after npm ci and records its target provenance', async () => {
    const commands = [];
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        createP03SubjectClosure(args[3]);
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error('unexpected command: ' + command + ' ' + args.join(' '));
    };
    const runCommand = vi.fn(async (command, args, options) => {
      commands.push([command, ...args]);
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options.cwd).toMatch(/frontend-app$/);
      return {
        error: undefined,
        outputTruncated: false,
        status: 0,
        stderr: '',
        stdout: '',
        timedOut: false,
      };
    });
    const target = subjectTarget();
    const metric = await collectDetachedStopFeedbackBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      runCommand,
      loadTarget: vi.fn(async (options) => {
        expect(options).toEqual(expect.objectContaining({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }));
        return target;
      }),
      runBenchmark: vi.fn(async ({ subjectSha, target: loadedTarget }) => {
        expect(loadedTarget).toBe(target);
        return {
          metricId: 'P03-feedback-budget',
          subjectFeedbackComponent: { path: P03_SUBJECT_FEEDBACK_COMPONENT_PATH, source: 'subject' },
          subjectSha,
          subjectRuntime: loadedTarget.provenance,
        };
      }),
    });
    expect(metric.subjectRuntime).toEqual(expect.objectContaining({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      installArgv: ['npm', 'ci'],
      worktreeClean: true,
      worktreeStatus: [],
    }));
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'worktree', 'add'],
      ['git', 'rev-parse', 'HEAD'],
      ['git', 'rev-parse', 'HEAD^{tree}'],
      ['git', 'status', '--porcelain'],
      ['npm', 'ci'],
      ['git', 'status', '--porcelain'],
      ['git', 'worktree', 'remove'],
    ]);
  });

  it('removes the detached worktree and temporary directory when Git identity differs from the requested subject', async () => {
    const commands = [];
    let temporaryRoot = '';
    const mismatchedSha = '9'.repeat(40);
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        temporaryRoot = args[3];
        mkdirSync(join(temporaryRoot, 'frontend-app'), { recursive: true });
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return mismatchedSha;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error('unexpected command: ' + command + ' ' + args.join(' '));
    };
    await expect(collectDetachedStopFeedbackBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
    })).rejects.toThrow(/Git identity/);
    expect(commands).toContainEqual(['git', 'worktree', 'remove', '--force', temporaryRoot]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });

  it('cleans up when npm ci, target loading, or benchmark execution fails', async () => {
    for (const stage of ['npm ci', 'target load', 'benchmark']) {
      const commands = [];
      let temporaryRoot = '';
      const execute = (command, args) => {
        commands.push([command, ...args]);
        if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
          temporaryRoot = args[3];
          createP03SubjectClosure(temporaryRoot);
          return '';
        }
        if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
        if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
        if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
        if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
        throw new Error('unexpected command: ' + command + ' ' + args.join(' '));
      };
      const runCommand = async () => (stage === 'npm ci'
        ? {
          error: new Error(stage),
          outputTruncated: false,
          status: null,
          stderr: '',
          stdout: '',
          timedOut: false,
        }
        : {
          error: undefined,
          outputTruncated: false,
          status: 0,
          stderr: '',
          stdout: '',
          timedOut: false,
        });
      await expect(collectDetachedStopFeedbackBudget({
        subjectSha: SUBJECT_SHA,
        subjectTree: SUBJECT_TREE,
        repositoryRoot: '/repository',
        execute,
        runCommand,
        loadTarget: stage === 'target load' ? async () => { throw new Error(stage); } : async () => subjectTarget(),
        runBenchmark: stage === 'benchmark'
          ? async () => { throw new Error(stage); }
          : async ({ subjectSha, target }) => ({
            metricId: 'P03-feedback-budget',
            subjectSha,
            subjectRuntime: target.provenance,
            subjectFeedbackComponent: { path: P03_SUBJECT_FEEDBACK_COMPONENT_PATH, source: 'subject' },
          }),
      })).rejects.toThrow(stage);
      expect(commands).toContainEqual(['git', 'worktree', 'remove', '--force', temporaryRoot]);
      expect(existsSync(temporaryRoot)).toBe(false);
    }
  });

  it('fails closed when P03 target provenance is absent or bound to another subject tree', () => {
    const absent = evidence();
    delete absent.metrics['P03-feedback-budget'].subjectRuntime;
    expect(verifyPerformanceEvidence(absent, baseline()).verdicts
      .find(({ metricId }) => metricId === 'P03-feedback-budget'))
      .toEqual(expect.objectContaining({ status: 'NOT_VERIFIED', reason: expect.stringMatching(/detached subject runtime/) }));

    const wrongTree = evidence();
    wrongTree.metrics['P03-feedback-budget'].subjectRuntime.subjectTree = '9'.repeat(40);
    expect(verifyPerformanceEvidence(wrongTree, baseline()).verdicts
      .find(({ metricId }) => metricId === 'P03-feedback-budget').status).toBe('NOT_VERIFIED');

    const missingSubjectComponent = evidence();
    delete missingSubjectComponent.metrics['P03-feedback-budget'].subjectFeedbackComponent;
    expect(verifyPerformanceEvidence(missingSubjectComponent, baseline()).verdicts
      .find(({ metricId }) => metricId === 'P03-feedback-budget').status).toBe('NOT_VERIFIED');
  });

  it('requires the exact ordered P03 production closure with no missing, extra, duplicated, or reordered paths', () => {
    const current = evidence();
    const runtime = current.metrics['P03-feedback-budget'];
    expect(() => validateP03SubjectRuntime(runtime, SUBJECT_SHA, SUBJECT_TREE)).not.toThrow();

    const mutations = [
      (files) => files.slice(1),
      (files) => [...files, { path: 'frontend-app/src/untracked.js', sha256: '9'.repeat(64) }],
      (files) => [...files].reverse(),
      (files) => files.map((file, index) => (index === 1 ? { ...file, path: files[0].path } : file)),
    ];
    for (const mutate of mutations) {
      const evidenceWithForgedClosure = evidence();
      const forged = evidenceWithForgedClosure.metrics['P03-feedback-budget'];
      forged.subjectRuntime.content.files = mutate(forged.subjectRuntime.content.files);
      expect(() => validateP03SubjectRuntime(forged, SUBJECT_SHA, SUBJECT_TREE))
        .toThrow(/incomplete production closure/);
    }
  });

  it('rejects a tampered P03 production file hash or aggregate hash', () => {
    const mutations = [
      (runtime) => { runtime.subjectRuntime.content.files[0].sha256 = 'f'.repeat(64); },
      (runtime) => { runtime.subjectRuntime.content.contentHash = 'e'.repeat(64); },
    ];
    for (const mutate of mutations) {
      const forged = evidence();
      mutate(forged.metrics['P03-feedback-budget']);
      expect(() => validateP03SubjectRuntime(
        forged.metrics['P03-feedback-budget'], SUBJECT_SHA, SUBJECT_TREE,
      )).toThrow(/content hash mismatch/);
      expect(verifyPerformanceEvidence(forged, baseline()).verdicts
        .find(({ metricId }) => metricId === 'P03-feedback-budget'))
        .toEqual(expect.objectContaining({ status: 'NOT_VERIFIED' }));
    }
  });
});
