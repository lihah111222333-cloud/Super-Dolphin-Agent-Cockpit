import React from 'react';

export function McBadge({
  children,
  variant = 'default', // 'default', 'primary', 'warning', 'success', 'danger'
  className = '',
  ...props
}) {
  const baseStyle = "inline-flex items-center px-2 py-0.5 border text-xs font-mono font-bold rounded-sm select-none uppercase tracking-wider";
  
  const variants = {
    default: "bg-[#282828] text-stone-400 border-stone-700",
    primary: "bg-[#b4b9c3]/10 text-[#b4b9c3] border-[#b4b9c3]/30",
    success: "bg-[#40c977]/10 text-[#40c977] border-[#40c977]/30",
    warning: "bg-[#ff8549]/10 text-[#ff8549] border-[#ff8549]/30",
    danger: "bg-[#ff6764]/10 text-[#ff6764] border-[#ff6764]/30"
  };

  return (
    <span
      className={`${baseStyle} ${variants[variant] || variants.default} ${className}`}
      {...props}
    >
      {children}
    </span>
  );
}
