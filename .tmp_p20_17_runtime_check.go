package main

import (
  "context"
  "fmt"
  "os"

  skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

func printSample(label, projectRoot, cwd string) error {
  svc := skillpkg.NewService(projectRoot)
  skills, err := svc.ListSkills(skillpkg.WithCWD(context.Background(), cwd))
  if err != nil {
    return err
  }
  fmt.Printf("%s\ncwd=%q\ncount=%d\n", label, cwd, len(skills))
  limit := 5
  if len(skills) < limit { limit = len(skills) }
  for i := 0; i < limit; i++ {
    fmt.Printf("- %s\n", skills[i].Name)
  }
  fmt.Println("--")
  return nil
}

func main() {
  projectRoot, err := os.Getwd()
  if err != nil { panic(err) }
  checks := []struct{ label, cwd string }{
    {label: "global fallback", cwd: ""},
    {label: "current project scoped", cwd: projectRoot},
    {label: "langgraph scoped", cwd: "/Users/mima0000/Desktop/wj/langgraph"},
  }
  for _, item := range checks {
    if err := printSample(item.label, projectRoot, item.cwd); err != nil {
      panic(err)
    }
  }
}
