import React, { useRef, useState } from 'react';

export function TagInput({ modelValue = [], placeholder = '输入标签后按回车', disabled = false, onChange }) {
  const [inputText, setInputText] = useState('');
  const inputRef = useRef(null);

  const remainingPlaceholder = modelValue.length > 0 ? '' : placeholder;

  function focusInput() {
    inputRef.current?.focus();
  }

  function add(textValue) {
    const raw = textValue.replace(/[,，]/g, '').trim();
    if (!raw) {
      setInputText('');
      return;
    }
    if (modelValue.some((t) => t.trim() === raw)) {
      setInputText('');
      return;
    }
    onChange?.([...modelValue, raw]);
    setInputText('');
  }

  function remove(tag) {
    if (disabled) return;
    onChange?.(modelValue.filter((t) => t !== tag));
  }

  return (
    <div
      className={`sp-tag-input ${disabled ? 'disabled' : ''}`}
      onClick={focusInput}
    >
      {modelValue.map((tag) => (
        <span className="sp-tag-chip" key={tag}>
          {tag}
          <button
            type="button"
            className="sp-tag-remove"
            disabled={disabled}
            onClick={(e) => {
              e.stopPropagation();
              remove(tag);
            }}
          >
            ×
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        value={inputText}
        placeholder={remainingPlaceholder}
        disabled={disabled}
        className="sp-tag-input-field"
        onChange={(e) => setInputText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            add(inputText);
          }
          if (e.key === ',' || e.key === '，') {
            e.preventDefault();
            add(inputText);
          }
        }}
      />
    </div>
  );
}
