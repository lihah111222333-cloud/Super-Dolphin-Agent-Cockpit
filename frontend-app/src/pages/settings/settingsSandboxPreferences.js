import { parseJsonObjectValue, rawTextValue } from "../shared/pageShared.js";
import { textValue } from "./settingsProviderConfig.js";
import { SETTINGS_DEFAULTS } from "./settingsRuntimeConstants.js";

function isPreferenceAbsent(value) {
  return (
    value === null ||
    value === undefined ||
    (typeof value === "string" && value.trim() === "")
  );
}

function isPreferenceTombstone(value) {
  return Boolean(
    value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    value.cleared === true,
  );
}

function normalizeSandboxMode(value) {
  const mode = textValue(value).trim();
  if (!mode) return SETTINGS_DEFAULTS.sandboxPolicy;
  if (mode === "workspace-write") return "workspaceWrite";
  if (mode === "read-only") return "readOnly";
  if (mode === "danger-full-access") return "dangerFullAccess";
  if (
    mode === "workspaceWrite" ||
    mode === "readOnly" ||
    mode === "dangerFullAccess"
  ) {
    return mode;
  }
  throw new Error(`invalid sandbox policy: ${mode}`);
}

function sandboxPreferenceFromRaw(value) {
  if (isPreferenceAbsent(value) || isPreferenceTombstone(value)) return null;
  if (typeof value === "string") {
    const text = value.trim();
    if (!text) return null;
    if (text.startsWith("{")) return parseSandboxPreferenceJson(text);
    return { type: normalizeSandboxMode(text) };
  }
  if (value && typeof value === "object" && !Array.isArray(value)) return value;
  throw new Error("加载 Sandbox 失败：sandbox preference must be an object");
}

function parseSandboxPreferenceJson(text) {
  try {
    return parseJsonObjectValue(text, "sandbox preference");
  } catch (error) {
    throw new Error("加载 Sandbox 失败：" + (error?.message || error), {
      cause: error,
    });
  }
}

function sandboxPolicyFromPreference(value) {
  if (value && typeof value === "object") {
    return normalizeSandboxMode(
      value.type || value.mode || SETTINGS_DEFAULTS.sandboxPolicy,
    );
  }
  return SETTINGS_DEFAULTS.sandboxPolicy;
}

function writableRootsFromPreference(value) {
  if (
    !value ||
    typeof value !== "object" ||
    !Array.isArray(value.writableRoots)
  ) {
    return "";
  }
  return value.writableRoots.join("\n");
}

function readOnlyModeFromPreference(value) {
  if (!value || typeof value !== "object" || value.type !== "readOnly") {
    return SETTINGS_DEFAULTS.readOnlyMode;
  }
  const access =
    value.access && typeof value.access === "object" ? value.access : {};
  return access.type === "restricted"
    ? "restricted"
    : SETTINGS_DEFAULTS.readOnlyMode;
}

function readableRootsFromPreference(value) {
  if (!value || typeof value !== "object" || value.type !== "readOnly") {
    return "";
  }
  const access =
    value.access && typeof value.access === "object" ? value.access : {};
  const roots = Array.isArray(access.readableRoots)
    ? access.readableRoots
    : access.readable_roots;
  return Array.isArray(roots) ? roots.join("\n") : "";
}

function pathsFromTextarea(value) {
  return rawTextValue(value)
    .toString()
    .split(/\r?\n/)
    .flatMap((item) => {
      const root = item.trim();
      return root ? [root] : [];
    });
}

function isAbsoluteRootPath(value) {
  const root = textValue(value).trim();
  return (
    root.startsWith("/") ||
    /^[a-zA-Z]:[\\/]/.test(root) ||
    /^\\\\[^\\]+\\[^\\]+/.test(root)
  );
}

function absolutePathsError(value, copy) {
  const paths = pathsFromTextarea(value);
  if (paths.length === 0) return copy.provider.missingRoot;
  const bad = paths.filter((root) => !isAbsoluteRootPath(root));
  return bad.length > 0
    ? copy.provider.absolutePathRequired + bad.join(", ")
    : "";
}

function sandboxPreferenceValue(
  policy,
  writableRootsText,
  networkAccess,
  readOnlyMode,
  readableRootsText,
) {
  if (policy === "readOnly") {
    if (readOnlyMode === "restricted") {
      return restrictedReadOnlyPreference(readableRootsText);
    }
    return { type: "readOnly" };
  }
  if (policy === "dangerFullAccess") return { type: "dangerFullAccess" };
  return {
    type: "workspaceWrite",
    writableRoots: pathsFromTextarea(writableRootsText),
    networkAccess: Boolean(networkAccess),
  };
}

function restrictedReadOnlyPreference(readableRootsText) {
  return {
    type: "readOnly",
    access: {
      type: "restricted",
      readableRoots: pathsFromTextarea(readableRootsText),
      includePlatformDefaults: true,
    },
  };
}

export {
  absolutePathsError,
  isPreferenceAbsent,
  isPreferenceTombstone,
  readableRootsFromPreference,
  readOnlyModeFromPreference,
  sandboxPreferenceFromRaw,
  sandboxPreferenceValue,
  sandboxPolicyFromPreference,
  writableRootsFromPreference,
};
