import React from 'react';

export function McButton({
  children,
  onClick,
  disabled = false,
  variant = 'default', // 'default', 'primary', 'danger', 'warning', 'success'
  className = '',
  ...props
}) {
  const baseStyle = "px-4 py-2 font-mono text-sm border-2 border-stone-800 rounded-sm font-bold transition-all duration-150 ease-out select-none outline-none active:translate-y-[2px] active:shadow-none hover:-translate-y-[2px] focus:ring-2 focus:ring-stone-500/50";
  
  const variants = {
    default: "bg-[#282828] text-[#b0b0b0] hover:bg-[#333333] hover:text-[#ffffff] shadow-[2px_2px_0px_#141615] hover:shadow-[4px_4px_0px_#141615]",
    primary: "bg-[#b4b9c3] text-[#1b1e1d] hover:bg-[#c4c9d3] shadow-[2px_2px_0px_#141615] hover:shadow-[4px_4px_0px_#141615]",
    danger: "bg-[#ff6764]/20 text-[#ff6764] border-[#ff6764] hover:bg-[#ff6764]/30 shadow-[2px_2px_0px_#ff6764] hover:shadow-[4px_4px_0px_#ff6764]",
    warning: "bg-[#ff8549]/20 text-[#ff8549] border-[#ff8549] hover:bg-[#ff8549]/30 shadow-[2px_2px_0px_#ff8549] hover:shadow-[4px_4px_0px_#ff8549]",
    success: "bg-[#40c977]/20 text-[#40c977] border-[#40c977] hover:bg-[#40c977]/30 shadow-[2px_2px_0px_#40c977] hover:shadow-[4px_4px_0px_#40c977]"
  };

  const disabledStyle = "opacity-50 cursor-not-allowed pointer-events-none transform-none shadow-none active:translate-none";

  const btnClass = `${baseStyle} ${disabled ? disabledStyle : variants[variant] || variants.default} ${className}`;

  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={btnClass}
      {...props}
    >
      {children}
    </button>
  );
}
