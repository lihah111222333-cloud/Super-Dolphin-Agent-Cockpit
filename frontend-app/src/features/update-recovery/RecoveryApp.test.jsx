import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { RecoveryApp } from "./RecoveryApp.jsx";

function recoveryState(overrides = {}) {
  return {
    mode: "recovery",
    lastAction: "state",
    actions: { check: true, retry: true, restore: true },
    failure: { code: "", retryable: false, action: "", transactionId: "" },
    projection: {
      transactionId: "transaction-1",
      attemptId: "attempt-1",
      state: "probation",
      leasePresent: true,
      leaseOwner: "guard-1",
      leaseGeneration: 2,
      candidateSHA256: "abcdef0123456789",
      reason: "normal preflight failed",
    },
    ...overrides,
  };
}

describe("RecoveryApp", () => {
  it("shows typed Recovery state and exposes only Recovery actions", async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState()),
      check: vi.fn().mockResolvedValue(recoveryState({ lastAction: "check" })),
      retry: vi.fn().mockResolvedValue(recoveryState({ lastAction: "retry" })),
      restore: vi
        .fn()
        .mockResolvedValue(recoveryState({ lastAction: "restore" })),
    };

    render(<RecoveryApp client={client} confirmRestore={() => true} />);

    expect(
      await screen.findByRole("heading", { name: "Super Dolphin Recovery" }),
    ).toBeVisible();
    expect(screen.getByText("normal preflight failed")).toBeVisible();
    expect(screen.getByText("transaction-1")).toBeVisible();
    expect(screen.queryByText(/normal ready/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    await waitFor(() => expect(client.check).toHaveBeenCalledTimes(1));
    expect(screen.getByText("check")).toBeVisible();
  });

  it("requires explicit confirmation before restore", async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState()),
      check: vi.fn(),
      retry: vi.fn(),
      restore: vi.fn(),
    };
    render(<RecoveryApp client={client} confirmRestore={() => false} />);
    await screen.findByText("normal preflight failed");

    fireEvent.click(screen.getByRole("button", { name: "Restore" }));

    expect(client.restore).not.toHaveBeenCalled();
  });

  it("disables transaction actions for normal-preflight Recovery", async () => {
    const client = {
      state: vi.fn().mockResolvedValue(
        recoveryState({
          actions: { check: false, retry: false, restore: false },
          projection: {
            ...recoveryState().projection,
            transactionId: "",
            attemptId: "",
            state: "",
            leasePresent: false,
            leaseOwner: "",
            leaseGeneration: 0,
            candidateSHA256: "",
          },
        }),
      ),
      check: vi.fn(),
      retry: vi.fn(),
      restore: vi.fn(),
    };
    render(<RecoveryApp client={client} />);
    await screen.findByText("normal preflight failed");

    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Retry" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Restore" })).toBeDisabled();
  });

  it("disables every action after restore returns rolled_back state", async () => {
    const client = {
      state: vi.fn().mockResolvedValue(recoveryState()),
      check: vi.fn(),
      retry: vi.fn(),
      restore: vi.fn().mockResolvedValue(
        recoveryState({
          lastAction: "restore",
          actions: { check: false, retry: false, restore: false },
          projection: {
            ...recoveryState().projection,
            state: "rolled_back",
            leasePresent: false,
            leaseOwner: "",
            leaseGeneration: 0,
          },
        }),
      ),
    };
    render(<RecoveryApp client={client} confirmRestore={() => true} />);
    await screen.findByText("normal preflight failed");
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(client.restore).toHaveBeenCalledTimes(1));

    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Retry" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Restore" })).toBeDisabled();
  });

  it.each([
    ["wait_then_retry", "Retry"],
    ["restart_application", "Restart App"],
    [
      "preserve_state_export_diagnostics",
      "Preserve State & Export Diagnostics",
    ],
  ])(
    "renders an explicit %s recovery action without running it automatically",
    async (action, label) => {
      const onRestartRequested = vi.fn();
      const exportDiagnostics = vi.fn();
      const client = {
        state: vi.fn().mockResolvedValue(
          recoveryState({
            failure: {
              code: "SAFE_FAILURE",
              retryable: action === "wait_then_retry",
              action,
              transactionId: "transaction-1",
            },
          }),
        ),
        check: vi.fn(),
        retry: vi.fn(),
        restore: vi.fn(),
      };
      render(
        <RecoveryApp
          client={client}
          onRestartRequested={onRestartRequested}
          exportDiagnostics={exportDiagnostics}
        />,
      );
      const button = await screen.findByRole("button", { name: label });
      expect(client.retry).not.toHaveBeenCalled();
      expect(onRestartRequested).not.toHaveBeenCalled();
      expect(exportDiagnostics).not.toHaveBeenCalled();
      fireEvent.click(button);
      if (action === "wait_then_retry")
        await waitFor(() => expect(client.retry).toHaveBeenCalledTimes(1));
      if (action === "restart_application")
        expect(onRestartRequested).toHaveBeenCalledTimes(1);
      if (action === "preserve_state_export_diagnostics") {
        expect(exportDiagnostics).toHaveBeenCalledWith({
          code: "SAFE_FAILURE",
          retryable: false,
          action,
          transaction_id: "transaction-1",
        });
      }
    },
  );

  it("refreshes State after Retry rejection and never renders the raw backend error", async () => {
    const secret =
      "postgres://admin:password@localhost/db PRIVATE KEY /Users/alice/private.db";
    const failure = {
      code: "UPDATE_TRANSACTION_AMBIGUOUS",
      retryable: false,
      action: "preserve_state_export_diagnostics",
      transactionId: "transaction-1",
    };
    const client = {
      state: vi
        .fn()
        .mockResolvedValueOnce(recoveryState())
        .mockResolvedValueOnce(recoveryState({ failure })),
      check: vi.fn(),
      retry: vi.fn().mockRejectedValue(new Error(secret)),
      restore: vi.fn(),
    };
    render(<RecoveryApp client={client} exportDiagnostics={vi.fn()} />);
    await screen.findByText("normal preflight failed");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("button", {
        name: "Preserve State & Export Diagnostics",
      }),
    ).toBeVisible();
    expect(client.state).toHaveBeenCalledTimes(2);
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
    expect(
      screen.getByText("Action required", { selector: ".recovery-status" }),
    ).toBeVisible();
  });

  it("uses a fixed generic message when an unknown action and State refresh both fail", async () => {
    const secret = "sk-live-secret postgres://password /Users/alice/private.db";
    const client = {
      state: vi
        .fn()
        .mockResolvedValueOnce(recoveryState())
        .mockRejectedValueOnce(new Error(secret)),
      check: vi.fn().mockRejectedValue(new Error(secret)),
      retry: vi.fn(),
      restore: vi.fn(),
    };
    render(<RecoveryApp client={client} />);
    await screen.findByText("normal preflight failed");
    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Recovery action failed. Sensitive diagnostics remain preserved internally.",
    );
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
  });
});
