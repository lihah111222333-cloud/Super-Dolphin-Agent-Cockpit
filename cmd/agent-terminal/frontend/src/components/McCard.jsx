import React from 'react';

export function McCard({
  children,
  title,
  headerRight,
  onClick,
  hoverLift = false,
  className = '',
  ...props
}) {
  const isClickable = !!onClick || hoverLift;

  const baseStyle = "border-2 border-stone-800 bg-[#212121] p-5 shadow-[4px_4px_0px_#141615] rounded-sm font-mono text-sm";
  const hoverStyle = isClickable 
    ? "cursor-pointer transition-all duration-150 ease-out hover:-translate-y-[2px] hover:shadow-[6px_6px_0px_#141615] active:translate-y-[2px] active:shadow-[2px_2px_0px_#141615]"
    : "";

  return (
    <div
      onClick={onClick}
      className={`${baseStyle} ${hoverStyle} ${className}`}
      {...props}
    >
      {(title || headerRight) && (
        <div className="flex items-center justify-between border-b border-stone-800/80 pb-3 mb-4">
          {title && (
            <h4 className="text-sm font-extrabold text-[#dcdfdc] uppercase tracking-wider">
              {title}
            </h4>
          )}
          {headerRight && (
            <div className="text-xs text-stone-400">
              {headerRight}
            </div>
          )}
        </div>
      )}
      <div>{children}</div>
    </div>
  );
}
