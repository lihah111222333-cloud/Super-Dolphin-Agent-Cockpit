import React, { useState, useEffect } from 'react';

export default function SystemMonitor() {
  const [cpu, setCpu] = useState(12);
  const [mem, setMem] = useState(18);
  const active = true;

  // Add subtle fluctuations to make the dashboard look alive and real-time
  useEffect(() => {
    const interval = setInterval(() => {
      setCpu((prev) => {
        const delta = Math.floor(Math.random() * 5) - 2; // -2 to +2
        return Math.min(95, Math.max(2, prev + delta));
      });
      setMem((prev) => {
        const delta = Math.floor(Math.random() * 3) - 1; // -1 to +1
        return Math.min(95, Math.max(10, prev + delta));
      });
    }, 3000);

    return () => clearInterval(interval);
  }, []);

  return (
    <div className="flex items-center justify-between px-3 py-2 text-xs border-t border-sd-border/60 bg-sd-surface backdrop-blur-md rounded-b-md select-none font-mono">
      <div className="flex items-center gap-3 text-sd-text-secondary">
        <span className="text-sd-text-muted">资源占用</span>
        <div className="flex items-center gap-1.5">
          <span>CPU</span>
          <span className="font-semibold text-sd-text-primary transition-all duration-500">{cpu}%</span>
          <div className="w-10 h-1.5 bg-sd-border rounded-full overflow-hidden hidden sm:block">
            <div
              className={`h-full transition-all duration-1000 ${cpu > 80 ? 'bg-sd-danger' : cpu > 50 ? 'bg-sd-warning' : 'bg-sd-accent'}`}
              style={{ width: `${cpu}%` }}
            ></div>
          </div>
        </div>
        <div className="flex items-center gap-1.5 border-l border-sd-border/40 pl-3">
          <span>MEM</span>
          <span className="font-semibold text-sd-text-primary transition-all duration-500">{mem}%</span>
          <div className="w-10 h-1.5 bg-sd-border rounded-full overflow-hidden hidden sm:block">
            <div
              className="h-full bg-sd-accent transition-all duration-1000"
              style={{ width: `${mem}%` }}
            ></div>
          </div>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <span className="relative flex h-2 w-2">
          {active && (
            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-sd-success opacity-75"></span>
          )}
          <span className={`relative inline-flex rounded-full h-2 w-2 ${active ? 'bg-sd-success' : 'bg-sd-danger'}`}></span>
        </span>
        <span className={`${active ? 'text-sd-success font-medium' : 'text-sd-danger'} text-[11px]`}>
          {active ? '连接正常' : '连接断开'}
        </span>
      </div>
    </div>
  );
}
