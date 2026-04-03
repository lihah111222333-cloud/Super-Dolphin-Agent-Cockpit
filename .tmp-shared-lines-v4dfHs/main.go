package main

import (
  "fmt"
  "os"
  "path/filepath"
  "sort"

  arch "github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func main() {
  files, err := filepath.Glob("internal/platform/shared/*.go")
  if err != nil {
    panic(err)
  }
  sort.Strings(files)
  total := 0
  for _, f := range files {
    data, err := os.ReadFile(f)
    if err != nil {
      panic(err)
    }
    n := arch.CountEffectiveLines(data)
    total += n
    fmt.Printf("%s %d\n", f, n)
  }
  fmt.Printf("TOTAL %d\n", total)
}
