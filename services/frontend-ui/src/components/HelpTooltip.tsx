import React, { useState, useRef, useEffect, useCallback } from 'react';

export interface HelpTooltipProps {
  title: string;
  description: string;
  howToUse?: string;
  position?: 'top' | 'bottom' | 'left' | 'right';
  accentColor?: 'cyan' | 'ruby' | 'amber' | 'emerald' | 'violet' | 'gold';
}

export const HelpTooltip: React.FC<HelpTooltipProps> = ({
  title,
  description,
  howToUse,
  position = 'top',
  accentColor = 'cyan',
}) => {
  const [isVisible, setIsVisible] = useState<boolean>(false);
  const containerRef = useRef<HTMLSpanElement>(null);

  // Close on outside click or Esc key
  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      setIsVisible(false);
    }
  }, []);

  useEffect(() => {
    if (!isVisible) return;
    const handleOutsideClick = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsVisible(false);
      }
    };
    document.addEventListener('mousedown', handleOutsideClick);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('mousedown', handleOutsideClick);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [isVisible, handleKeyDown]);

  const toggleOpen = (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsVisible((prev) => !prev);
  };

  return (
    <span
      ref={containerRef}
      className={`help-tooltip-wrapper help-tooltip-accent-${accentColor}`}
      onMouseEnter={() => setIsVisible(true)}
      onMouseLeave={() => setIsVisible(false)}
    >
      <button
        type="button"
        className={`help-tooltip-trigger help-tooltip-trigger-${accentColor} ${isVisible ? 'is-active' : ''}`}
        aria-label={`Справка: ${title}`}
        aria-expanded={isVisible}
        onClick={toggleOpen}
        onFocus={() => setIsVisible(true)}
        onBlur={() => setIsVisible(false)}
      >
        ?
      </button>

      {isVisible && (
        <div
          role="tooltip"
          className={`help-tooltip-popover help-tooltip-pos-${position} help-tooltip-border-${accentColor}`}
          onClick={(e) => e.stopPropagation()}
        >
          <div className={`help-tooltip-top-bar help-tooltip-bg-${accentColor}`} />
          <div className="help-tooltip-header">
            <span className="help-tooltip-icon">ℹ️</span>
            <span className="help-tooltip-title">{title}</span>
          </div>
          <p className="help-tooltip-description">{description}</p>
          {howToUse && (
            <div className="help-tooltip-how-to-use">
              <span className="help-tooltip-sublabel">💡 Как использовать:</span>
              <p className="help-tooltip-subtext">{howToUse}</p>
            </div>
          )}
        </div>
      )}
    </span>
  );
};
