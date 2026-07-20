function exportRecoveryDiagnostics(failure) {
  const blob = new Blob([JSON.stringify(failure, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "super-dolphin-recovery-diagnostics.json";
  try {
    link.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}

export { exportRecoveryDiagnostics };
