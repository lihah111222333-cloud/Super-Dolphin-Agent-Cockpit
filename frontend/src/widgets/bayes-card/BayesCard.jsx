import React, { useState } from 'react';

export default function BayesCard() {
  const [prior, setPrior] = useState(30);
  const [evidence, setEvidence] = useState(50); // slider control

  // Simple Bayesian update simulation for visual interaction:
  // posterior = (prior * evidence) / [prior * evidence + (1-prior)*(1-evidence)]
  const p = prior / 100;
  const e = evidence / 100;
  const denominator = p * e + (1 - p) * (1 - e);
  const posterior = denominator === 0 ? 0 : Math.round(((p * e) / denominator) * 100);

  // Convert percentages into radar coordinate vectors
  const getRadarPoints = () => {
    const cx = 100;
    const cy = 100;
    const scale = 0.8;

    // 3 axes angles (Prior at 90deg, Evidence at 210deg, Posterior at 330deg)
    const priorRad = -Math.PI / 2;
    const evidenceRad = (7 * Math.PI) / 6;
    const posteriorRad = (11 * Math.PI) / 6;

    const x1 = cx + prior * scale * Math.cos(priorRad);
    const y1 = cy + prior * scale * Math.sin(priorRad);

    const x2 = cx + evidence * scale * Math.cos(evidenceRad);
    const y2 = cy + evidence * scale * Math.sin(evidenceRad);

    const x3 = cx + posterior * scale * Math.cos(posteriorRad);
    const y3 = cy + posterior * scale * Math.sin(posteriorRad);

    return `${x1},${y1} ${x2},${y2} ${x3},${y3}`;
  };

  return (
    <div className="glass-panel p-4 flex flex-col gap-4 relative overflow-hidden transition-premium hover-glow">
      <div className="flex justify-between items-center border-b border-sd-border pb-2">
        <div>
          <h3 className="text-sm font-semibold text-sd-text-primary">贝叶斯思维</h3>
          <p className="text-xs text-sd-text-secondary mt-0.5">用概率思维看问题，在不确定中做更好决策。</p>
        </div>
        <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-sd-accent/10 text-sd-accent border border-sd-accent/20 font-mono">
          SVC-CARD
        </span>
      </div>

      <div className="grid grid-cols-3 gap-2 text-xs border-b border-sd-border/50 pb-3">
        <div className="flex flex-col gap-1">
          <span className="text-sd-text-muted">先验</span>
          <span className="text-sm font-medium text-sd-text-primary">雨天概率 {prior}%</span>
          <input
            type="range"
            min="0"
            max="100"
            value={prior}
            onChange={(e) => setPrior(Number(e.target.value))}
            className="w-full accent-sd-accent h-1 rounded bg-sd-border cursor-pointer"
          />
        </div>
        <div className="flex flex-col gap-1 border-l border-sd-border pl-2">
          <span className="text-sd-text-muted">证据</span>
          <span className="text-sm font-medium text-sd-text-primary">看到乌云 ({evidence}%)</span>
          <input
            type="range"
            min="0"
            max="100"
            value={evidence}
            onChange={(e) => setEvidence(Number(e.target.value))}
            className="w-full accent-sd-accent h-1 rounded bg-sd-border cursor-pointer"
          />
        </div>
        <div className="flex flex-col gap-1 border-l border-sd-border pl-2">
          <span className="text-sd-text-muted">后验</span>
          <span className="text-sm font-medium text-sd-text-primary">雨天概率 {posterior}%</span>
          <div className="h-1 rounded bg-sd-accent/30 overflow-hidden">
            <div className="bg-sd-accent h-full transition-premium" style={{ width: `${posterior}%` }}></div>
          </div>
        </div>
      </div>

      {/* Radar Chart Section */}
      <div className="flex justify-center items-center py-2">
        <svg width="200" height="200" className="overflow-visible">
          {/* Concentric grid circles */}
          <circle cx="100" cy="100" r="80" fill="none" stroke="currentColor" className="text-sd-border" strokeWidth="1" strokeDasharray="3 3" />
          <circle cx="100" cy="100" r="60" fill="none" stroke="currentColor" className="text-sd-border" strokeWidth="1" />
          <circle cx="100" cy="100" r="40" fill="none" stroke="currentColor" className="text-sd-border" strokeWidth="1" strokeDasharray="3 3" />
          <circle cx="100" cy="100" r="20" fill="none" stroke="currentColor" className="text-sd-border" strokeWidth="1" />

          {/* Core Axes */}
          <line x1="100" y1="100" x2="100" y2="20" stroke="currentColor" className="text-sd-border" strokeWidth="1" />
          <line x1="100" y1="100" x2="31" y2="140" stroke="currentColor" className="text-sd-border" strokeWidth="1" />
          <line x1="100" y1="100" x2="169" y2="140" stroke="currentColor" className="text-sd-border" strokeWidth="1" />

          {/* Active Probability Polygon */}
          <polygon
            points={getRadarPoints()}
            fill="url(#radarGradient)"
            stroke="var(--sd-accent)"
            strokeWidth="1.5"
            className="transition-all duration-300"
          />

          {/* Node pointers */}
          {/* Prior */}
          <circle cx="100" cy={100 - prior * 0.8} r="4" fill="var(--sd-accent)" className="transition-all duration-300" />
          {/* Evidence */}
          <circle cx={100 + evidence * 0.8 * Math.cos((7 * Math.PI) / 6)} cy={100 + evidence * 0.8 * Math.sin((7 * Math.PI) / 6)} r="4" fill="var(--sd-accent)" className="transition-all duration-300" />
          {/* Posterior */}
          <circle cx={100 + posterior * 0.8 * Math.cos((11 * Math.PI) / 6)} cy={100 + posterior * 0.8 * Math.sin((11 * Math.PI) / 6)} r="4" fill="var(--sd-accent)" className="transition-all duration-300" />

          <defs>
            <radialGradient id="radarGradient">
              <stop offset="0%" stopColor="var(--sd-accent)" stopOpacity="0.1" />
              <stop offset="100%" stopColor="var(--sd-accent)" stopOpacity="0.4" />
            </radialGradient>
          </defs>
        </svg>
      </div>

      {/* Bayes Workflow Nodes */}
      <div className="flex justify-between items-center bg-sd-bg/40 border border-sd-border rounded p-2 text-[10px] text-sd-text-secondary mt-1">
        <div className="flex flex-col items-center gap-1">
          <div className="w-5 h-5 rounded-full border border-sd-border flex items-center justify-center font-mono">
            {prior}%
          </div>
          <span>先验</span>
        </div>
        <svg className="w-4 h-4 text-sd-text-muted" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14 5l7 7m0 0l-7 7m7-7H3" />
        </svg>
        <div className="flex flex-col items-center gap-1">
          <div className="w-5 h-5 rounded-full border border-sd-border flex items-center justify-center font-mono">
            {evidence}%
          </div>
          <span>证据</span>
        </div>
        <svg className="w-4 h-4 text-sd-text-muted animate-spin-slow" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 1121.21 8H18" />
        </svg>
        <div className="flex flex-col items-center gap-1">
          <div className="w-5 h-5 rounded-full border border-sd-accent bg-sd-accent/10 text-sd-accent flex items-center justify-center font-mono font-bold">
            {posterior}%
          </div>
          <span>后验</span>
        </div>
      </div>
    </div>
  );
}
