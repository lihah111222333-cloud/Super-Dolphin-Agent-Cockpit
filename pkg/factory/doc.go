// Package factory provides cross-package DRY primitives (Zone A).
//
// Rules:
//   - No imports from internal/* business packages
//   - Each file ≤500 lines
//   - Directory total ≤2000 lines
//   - Only content reused by ≥2 packages
package factory
