#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const {spawnSync} = require("child_process");
const root = path.resolve(__dirname, "..");

function updateEventSnapshot() {
  const file = path.join(root, "internal/apiserver/notifications.go");
  const src = fs.readFileSync(file, "utf8");
  const start = src.indexOf("var eventMethodMap = map[string]string{");
  const end = src.indexOf("
}", start);
  const body = src.slice(start, end);
  const rows = [];
  for (const line of body.split("
")) {
    const m = line.match(/^s*(.+?):s*"([^"]+)",/);
    if (!m) continue;
    let key = m[1].trim().replace(/^agentcore./, "").replace(/^"|"$/g, "");
    const method = m[2];
    const replay = ["turn/completed", "turn/aborted", "error", "agent/event/stream_error"].includes(method);
    rows.push(`		"${key}=${method}|${replay}",`);
  }
  rows.sort();
  console.log("Event snapshot rows:
" + rows.join("
"));
}

function updateMethodSnapshot() {
  const tmp = path.join(root, `tmp_apiserver_methods_${Date.now()}.go`);
  const src = `package main
import (
  "fmt"
  "reflect"
  "sort"
  apiserver "github.com/multi-agent/go-agent-v2/internal/apiserver"
)
func main(){
  s:=apiserver.New(apiserver.Deps{ProcessCwd:"/tmp"})
  v:=reflect.ValueOf(s).Elem().FieldByName("methods")
  names:=make([]string,0,v.Len())
  for _,k:=range v.MapKeys(){ names=append(names,k.String()) }
  sort.Strings(names)
  for _,n:= range names { fmt.Println(n) }
}`;
  fs.writeFileSync(tmp, src);
  const p = spawnSync("go", ["run", path.basename(tmp)], {cwd: root, encoding: "utf8", maxBuffer: 20*1024*1024});
  fs.unlinkSync(tmp);
  if (p.status !== 0) throw new Error(p.stderr || p.stdout);
  const rows = p.stdout.split("
").filter(Boolean).filter(l => !l.startsWith("{"time""));
  console.log("Method snapshot rows:
" + rows.map(name => `		"${name}",`).join("
"));
}

updateEventSnapshot();
updateMethodSnapshot();
