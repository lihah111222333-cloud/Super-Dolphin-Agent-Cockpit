import { useCallback, useEffect, useState } from "react";
import {
  Download,
  Power,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  Siren,
} from "lucide-react";

import { exportRecoveryDiagnostics } from "./recoveryDiagnostics.js";
import {
  recoveryPublicErrorForFailure,
  recoveryPublicReasonForDisplay,
} from "./recoveryClient.js";

const EMPTY_STATE = Object.freeze({
  status: "loading",
  value: null,
  error: null,
});

function fieldValue(value) {
  return value === "" ? "None" : value;
}

function safeFailurePayload(failure) {
  return {
    code: failure.code,
    retryable: failure.retryable,
    action: failure.action,
    transaction_id: failure.transactionId,
  };
}

function viewForValue(value) {
  return { status: value.failure.code ? "failure" : "ready", value, error: "" };
}

async function refreshAfterActionFailure(client, cause) {
  try {
    const value = await client.state();
    if (value.failure.code) return viewForValue(value);
    return { status: "error", value, error: recoveryPublicErrorForFailure(cause) };
  } catch {
    return {
      status: "error",
      value: null,
      error: recoveryPublicErrorForFailure(cause),
    };
  }
}

function recoveryStatusLabel(busy, failure, projection) {
  if (busy) return "Checking";
  if (failure?.code) return "Action required";
  return projection?.state || "Recovery";
}

function RecoveryFailureAction({ failure, busy, onRestart, onExport }) {
  if (!failure?.code) return null;
  let actionButton = null;
  if (failure.action === "restart_application") {
    actionButton = (
      <button type="button" onClick={onRestart} disabled={busy}>
        <Power size={18} /> Show Manual Restart Steps
      </button>
    );
  } else if (failure.action === "preserve_state_export_diagnostics") {
    actionButton = (
      <button type="button" onClick={onExport} disabled={busy}>
        <Download size={18} /> Preserve State &amp; Export Diagnostics
      </button>
    );
  }
  return (
    <section className="recovery-summary" aria-label="Required recovery action">
      <p>Action required</p>
      <strong>{failure.code}</strong>
      {actionButton}
    </section>
  );
}

function RecoveryFooter(props) {
  const { actions, busy, activeAction, onAction, onRestore } = props;
  return (
    <footer className="recovery-actions">
      <button
        type="button"
        onClick={() => void onAction("check")}
        disabled={busy || !actions?.check}
      >
        <ShieldCheck size={18} /> Check
      </button>
      <button
        type="button"
        onClick={() => void onAction("retry")}
        disabled={busy || !actions?.retry}
      >
        <RefreshCw
          size={18}
          className={busy && activeAction === "retry" ? "spin" : ""}
        />{" "}
        Retry
      </button>
      <button
        type="button"
        className="restore"
        onClick={onRestore}
        disabled={busy || !actions?.restore}
      >
        <RotateCcw size={18} /> Restore
      </button>
    </footer>
  );
}

function RecoveryApp({
  client,
  confirmRestore = window.confirm,
  exportDiagnostics = exportRecoveryDiagnostics,
}) {
  const [view, setView] = useState(EMPTY_STATE);
  const [activeAction, setActiveAction] = useState("state");
  const [restartInstruction, setRestartInstruction] = useState("");

  const runAction = useCallback(
    async (action) => {
      setActiveAction(action);
        setView((current) => ({ ...current, status: "loading", error: null }));
      try {
        const value = await client[action]();
        setView(viewForValue(value));
      } catch (error) {
        const failedView = await refreshAfterActionFailure(client, error);
        setView(failedView);
      }
    },
    [client],
  );

  useEffect(() => {
    void runAction("state");
  }, [runAction]);

  const restore = useCallback(() => {
    if (!confirmRestore("Restore the previous release?")) return;
    void runAction("restore");
  }, [confirmRestore, runAction]);

  const projection = view.value?.projection;
  const actions = view.value?.actions;
  const failure = view.value?.failure;
	const recoveryReason = recoveryPublicReasonForDisplay(projection?.reason);
  const busy = view.status === "loading";
  const statusLabel = recoveryStatusLabel(busy, failure, projection);
  const restart = useCallback(() => {
    setRestartInstruction(
      "Quit and reopen Super Dolphin manually. Recovery state will remain preserved.",
    );
  }, []);
  const exportFailure = useCallback(() => {
    if (failure?.code) exportDiagnostics(safeFailurePayload(failure));
  }, [exportDiagnostics, failure]);

  return (
    <main className="recovery-shell">
      <header className="recovery-header">
        <div className="recovery-brand">
          <span className="recovery-mark" aria-hidden="true">
            <Siren size={22} />
          </span>
          <div>
            <p className="recovery-kicker">Safe Mode</p>
            <h1>Super Dolphin Recovery</h1>
          </div>
        </div>
        <span className="recovery-status" data-status={view.status}>
          {statusLabel}
        </span>
      </header>

      {view.error && (
        <div className="recovery-error" role="alert">
          <strong>{view.error.title}</strong>
          <p>{view.error.publicMessage}</p>
          <p>Diagnostic ID: {view.error.diagnosticId}</p>
        </div>
      )}
      {restartInstruction && (
        <div className="recovery-error" role="status">
          {restartInstruction}
        </div>
      )}

      <RecoveryFailureAction
        failure={failure}
        busy={busy}
        onRestart={restart}
        onExport={exportFailure}
      />

      {projection && (
        <>
          <section
            className="recovery-summary"
            aria-labelledby="recovery-reason-title"
          >
            <p id="recovery-reason-title">Recovery reason</p>
            <strong>{recoveryReason ? recoveryReason.publicMessage : "None"}</strong>
            {recoveryReason && <p>Diagnostic ID: {recoveryReason.diagnosticId}</p>}
          </section>

          <section className="recovery-details" aria-label="Recovery state">
            <dl>
              <div>
                <dt>Transaction</dt>
                <dd>{fieldValue(projection.transactionId)}</dd>
              </div>
              <div>
                <dt>Attempt</dt>
                <dd>{fieldValue(projection.attemptId)}</dd>
              </div>
              <div>
                <dt>State</dt>
                <dd>{fieldValue(projection.state)}</dd>
              </div>
              <div>
                <dt>Lease owner</dt>
                <dd>{fieldValue(projection.leaseOwner)}</dd>
              </div>
              <div>
                <dt>Lease generation</dt>
                <dd>{projection.leaseGeneration}</dd>
              </div>
              <div>
                <dt>Candidate SHA-256</dt>
                <dd title={projection.candidateSHA256}>
                  {fieldValue(projection.candidateSHA256)}
                </dd>
              </div>
              <div>
                <dt>Last action</dt>
                <dd>{fieldValue(view.value.lastAction)}</dd>
              </div>
            </dl>
          </section>
        </>
      )}

      <RecoveryFooter
        actions={actions}
        busy={busy}
        activeAction={activeAction}
        onAction={runAction}
        onRestore={restore}
      />
    </main>
  );
}

export { RecoveryApp };
