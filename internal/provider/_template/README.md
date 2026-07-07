# Provider Template

Copy this directory when adding a provider. Replace the `template` package name,
provider name, event translator, prompt capture, and driver implementation, but
keep the contract shape intact.

Required checks before integration:

- `Module` registers a `contract.DriverFactory` in `group:"drivers"`.
- `NewDriverFactory` calls `ValidateProviderDependencies` before returning.
- Production profile rejects missing runtime reporter, toolbridge/proxy, mirror,
  session recovery, and dependency profile.
- `NativeTools` are declared with explicit governance.
- `provider_contract_test.go` calls `contracttest.Run` and covers event
  translation, prompt parity, approval or approval policy, interrupt,
  force-complete, resume identity, toolbridge/proxy, and runtime report.
