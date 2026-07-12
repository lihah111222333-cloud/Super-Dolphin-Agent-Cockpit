import { AlertTriangle, Sparkles, Wrench, X } from 'lucide-react';

const CAPABILITY_ICONS = Object.freeze({
  skill: Sparkles,
  mcp_tool: Wrench,
});

function requiredCapabilityText(value, field) {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new TypeError(`composer capability ${field} must be a non-empty string`);
  }
  return value.trim();
}

function uniqueCapabilities(items) {
  if (!Array.isArray(items)) {
    throw new TypeError('composer capabilities must be an array');
  }
  const keys = new Set();
  return items.filter((item) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new TypeError('composer capability must be an object');
    }
    const key = requiredCapabilityText(item.key, 'key');
    requiredCapabilityText(item.label, 'label');
    if (keys.has(key)) return false;
    keys.add(key);
    return true;
  });
}

function availabilityReason(item, copy) {
  if (item.availability === 'ready') return '';
  if (item.availability === 'stale') return copy.staleCapability;
  if (item.availability === 'unverified') return copy.unverifiedCapability;
  throw new TypeError(`unsupported composer capability availability ${item.availability}`);
}

function CapabilityChip({ copy, item, onRemove }) {
  const Icon = CAPABILITY_ICONS[item.kind];
  if (!Icon) throw new TypeError(`unsupported composer capability kind ${item.kind}`);
  const label = requiredCapabilityText(item.label, 'label');
  const reason = availabilityReason(item, copy);
  const removeLabel = `${copy.removeCapability}: ${label}`;
  return (
    <li
      className={`composer-capability-chip is-${item.availability}`}
      title={reason || undefined}
    >
      <Icon aria-hidden="true" size={14} strokeWidth={1.8} />
      <span className="composer-capability-chip__label">{label}</span>
      {reason ? (
        <>
          <AlertTriangle
            className="composer-capability-chip__warning"
            aria-hidden="true"
            size={13}
            strokeWidth={2}
          />
          <span className="composer-capability-chip__status">{reason}</span>
        </>
      ) : null}
      <button
        type="button"
        className="composer-capability-chip__remove"
        aria-label={removeLabel}
        title={removeLabel}
        onClick={() => onRemove(item.key)}
      >
        <X aria-hidden="true" size={13} strokeWidth={2} />
      </button>
    </li>
  );
}

export function ComposerCapabilityChips({ copy, items, onRemove }) {
  const capabilities = uniqueCapabilities(items);
  if (capabilities.length === 0) return null;
  return (
    <ul className="composer-capability-list" role="list">
      {capabilities.map((item) => (
        <CapabilityChip key={item.key} copy={copy} item={item} onRemove={onRemove} />
      ))}
    </ul>
  );
}
